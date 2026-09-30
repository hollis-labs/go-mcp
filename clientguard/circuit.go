package clientguard

import (
	"sync"
	"time"
)

// CircuitState is a CircuitBreaker's state.
type CircuitState int

const (
	// CircuitClosed is normal operation: calls are admitted.
	CircuitClosed CircuitState = iota
	// CircuitOpen means the breaker tripped: calls are refused until the
	// cooldown elapses.
	CircuitOpen
	// CircuitHalfOpen means the cooldown elapsed and one probe call has been
	// (or may be) admitted to test recovery.
	CircuitHalfOpen
)

// String returns "closed", "open" or "half-open".
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// DefaultCooldown and DefaultThreshold are starting points, numerically
// matching go-llm-contracts' own defaults (a different domain: LLM provider
// HTTP calls). They are not evidenced against any real MCP upstream; tune
// them through NewCircuitBreaker / WithCircuitBreaker.
const (
	DefaultCooldown  = 30 * time.Second
	DefaultThreshold = 5
)

// CircuitBreaker tracks consecutive failures for ONE key and trips after
// Threshold of them, then half-opens after Cooldown. It is safe for
// concurrent use.
//
// The state machine and method names are ported from
// go-llm-contracts.CircuitBreaker (not imported). One deliberate difference:
// the source's half-open state admits EVERY caller that asks (its IsOpen
// returns false for all of them once half-open), so a burst of concurrent
// callers all reach the recovering upstream. This breaker admits exactly one
// in-flight probe: while a probe is outstanding, Allow returns false for
// everyone else.
//
// A result only counts if no state transition happened since its call was
// admitted (an internal epoch), so a slow call admitted before the circuit
// tripped cannot close or re-open it when it finally reports, and a Reset
// during an in-flight probe cannot be overridden by that probe's late result.
type CircuitBreaker struct {
	clk       clock
	threshold int
	cooldown  time.Duration

	mu       sync.Mutex
	state    CircuitState
	fails    int
	openedAt time.Time
	probing  bool   // a half-open probe is in flight
	epoch    uint64 // bumped on every state transition and Reset
}

// NewCircuitBreaker returns a closed breaker that trips after threshold
// consecutive failures and half-opens after cooldown. A threshold below 1
// selects DefaultThreshold; a cooldown at or below zero selects
// DefaultCooldown.
func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return newCircuitBreaker(threshold, cooldown, realClock)
}

func newCircuitBreaker(threshold int, cooldown time.Duration, clk clock) *CircuitBreaker {
	if threshold < 1 {
		threshold = DefaultThreshold
	}
	if cooldown <= 0 {
		cooldown = DefaultCooldown
	}
	return &CircuitBreaker{clk: clk, threshold: threshold, cooldown: cooldown}
}

type outcome uint8

const (
	outcomeSuccess outcome = iota
	outcomeFailure
	// outcomeAbandoned means the call never ran, or the caller gave up: it
	// says nothing about upstream health and only releases a probe slot.
	outcomeAbandoned
)

// admission identifies one admitted call so its result can be matched to the
// state it was admitted under.
type admission struct {
	epoch uint64
	probe bool
}

// Allow reports whether a call may proceed. It is false while the circuit is
// open and the cooldown has not elapsed, and false while a half-open probe is
// already in flight.
//
// Allow has a side effect: once the cooldown has elapsed, the first call moves
// Open to HalfOpen and returns true, and that caller is THE probe. Every
// other caller gets false until the probe reports (RecordSuccess or
// RecordFailure) or gives up (Release). Do not call Allow speculatively:
// every true from a half-open breaker must be followed by exactly one of
// those three, or the breaker stays half-open and refuses everything.
func (cb *CircuitBreaker) Allow() bool {
	_, ok := cb.admit()
	return ok
}

func (cb *CircuitBreaker) admit() (admission, bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case CircuitOpen:
		if cb.clk.now().Sub(cb.openedAt) < cb.cooldown {
			return admission{}, false
		}
		cb.state = CircuitHalfOpen
		cb.epoch++
		cb.probing = true
		return admission{epoch: cb.epoch, probe: true}, true
	case CircuitHalfOpen:
		if cb.probing {
			return admission{}, false
		}
		// The previous probe was released without a verdict.
		cb.probing = true
		return admission{epoch: cb.epoch, probe: true}, true
	default:
		return admission{epoch: cb.epoch}, true
	}
}

// RecordFailure records a failed attempt. It returns true only on a fresh
// closed to open trip; a failed half-open probe re-opens the circuit (and
// restarts the cooldown) but reports false, as in go-llm-contracts.
func (cb *CircuitBreaker) RecordFailure() (tripped bool) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.doneLocked(cb.currentLocked(), outcomeFailure)
}

// RecordSuccess resets the failure count and closes the circuit from any
// state; a successful half-open probe closes it.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.doneLocked(cb.currentLocked(), outcomeSuccess)
}

// Release gives up a half-open probe slot without a verdict, for a caller
// that was admitted by Allow but never made the call. The next Allow becomes
// the probe. It does nothing in any other state.
func (cb *CircuitBreaker) Release() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.doneLocked(cb.currentLocked(), outcomeAbandoned)
}

// Reset forces the breaker closed and clears its counters, for a
// caller-driven manual reset. Results of calls admitted before the Reset are
// ignored when they arrive.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.fails = 0
	cb.probing = false
	cb.epoch++
}

// State returns the current state. It has no side effects: an open circuit
// whose cooldown has elapsed still reports CircuitOpen until a call is
// admitted as the probe.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// currentLocked is the admission that public Record*/Release calls act as:
// the breaker's present state, with the probe role if half-open.
func (cb *CircuitBreaker) currentLocked() admission {
	return admission{epoch: cb.epoch, probe: cb.state == CircuitHalfOpen}
}

func (cb *CircuitBreaker) done(a admission, o outcome) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.doneLocked(a, o)
}

func (cb *CircuitBreaker) doneLocked(a admission, o outcome) (tripped bool) {
	if a.epoch != cb.epoch {
		return false // a transition happened since admission: stale result
	}
	switch o {
	case outcomeAbandoned:
		if a.probe && cb.state == CircuitHalfOpen {
			cb.probing = false
		}
	case outcomeSuccess:
		cb.fails = 0
		if cb.state != CircuitClosed {
			cb.state = CircuitClosed
			cb.probing = false
			cb.epoch++
		}
	case outcomeFailure:
		cb.fails++
		switch cb.state {
		case CircuitHalfOpen:
			cb.state = CircuitOpen
			cb.openedAt = cb.clk.now()
			cb.probing = false
			cb.epoch++
		case CircuitOpen:
			// A late failure while open only bumps the count; it does not
			// restart the cooldown.
		case CircuitClosed:
			if cb.fails >= cb.threshold {
				cb.state = CircuitOpen
				cb.openedAt = cb.clk.now()
				cb.epoch++
				return true
			}
		}
	}
	return false
}
