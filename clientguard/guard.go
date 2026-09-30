package clientguard

import (
	"context"
	"errors"
	"sync"
	"time"
)

// This file's Guard is the call-admission guard of package clientguard. It is
// unrelated to server/guard.go (StrictArgs / ValidateSchema), which validates
// tool arguments on the server side.

// ErrCircuitOpen is returned when the key's circuit breaker refuses the call:
// the circuit is open and its cooldown has not elapsed, or a half-open probe
// is already in flight. fn was not called. It maps naturally onto
// budget.ErrCodeUnavailable for a caller building a protocol-level response;
// clientguard does not import budget, so that mapping is prose, not a type.
var ErrCircuitOpen = errors.New("clientguard: circuit open")

// ErrRateLimited is returned (RateLimitReject mode) when the key has no call
// budget left. fn was not called. It maps onto budget.ErrCodeRateLimited.
var ErrRateLimited = errors.New("clientguard: rate limit exceeded")

// RateLimitMode says what Guard.Do does when the key's budget is exhausted.
type RateLimitMode uint8

const (
	// RateLimitReject (the zero value) fails fast with ErrRateLimited.
	RateLimitReject RateLimitMode = iota
	// RateLimitBlock waits for budget (RateLimiter.Wait), honoring ctx.
	RateLimitBlock
)

type guardConfig struct {
	clk clock

	breaker   bool
	threshold int
	cooldown  time.Duration

	limiter bool
	limit   int
	period  time.Duration
	mode    RateLimitMode

	classify func(error) bool
}

// Option configures a Guard.
type Option func(*guardConfig)

// WithCircuitBreaker enables a per-key CircuitBreaker(threshold, cooldown).
// Without it Guard.Do never refuses a call for a broken upstream: options are
// opt-in and the zero value is a passthrough, like the rest of go-mcp.
func WithCircuitBreaker(threshold int, cooldown time.Duration) Option {
	return func(c *guardConfig) {
		c.breaker, c.threshold, c.cooldown = true, threshold, cooldown
	}
}

// WithRateLimit enables a per-key RateLimiter(limit, period) applied in mode.
func WithRateLimit(limit int, period time.Duration, mode RateLimitMode) Option {
	return func(c *guardConfig) {
		c.limiter, c.limit, c.period, c.mode = true, limit, period, mode
	}
}

// WithFailureClassifier overrides which errors from fn count as a breaker
// failure; it is only consulted for a non-nil error. Returning false records
// the call as a success (the upstream answered); returning true records a
// failure. The default counts every non-nil error.
//
// For go-mcp/client's Pool.CallTool the default is nearly right: a tool-level
// failure comes back as a CallToolResult with IsError set and a nil error, so
// the Go errors are connection failures, timeouts, dial errors and JSON-RPC
// protocol errors. Override it to exempt errors that say nothing about the
// upstream's health, such as a bad-arguments JSON-RPC error or Pool's
// "is not registered" error.
//
// Whatever the classifier says, an error is never counted when the caller
// itself canceled ctx (context.Canceled with ctx done): an abandoned call
// says nothing about the upstream. A ctx deadline that expired IS passed to
// the classifier and counted by default, since a slow upstream is a failing
// one.
func WithFailureClassifier(fn func(error) bool) Option {
	return func(c *guardConfig) { c.classify = fn }
}

// Guard composes one CircuitBreaker and one RateLimiter per key, the same
// per-key shape as client.Pool's entries map: one Guard covers every upstream
// a Pool knows, keyed by the server name the caller registered with. The key
// is any caller-chosen string; Guard does not import client. A Guard with no
// options is a passthrough. Safe for concurrent use.
type Guard struct {
	cfg guardConfig

	mu   sync.Mutex
	keys map[string]*keyState
}

type keyState struct {
	breaker *CircuitBreaker
	limiter *RateLimiter
}

// New returns a Guard configured by opts.
func New(opts ...Option) *Guard {
	cfg := guardConfig{clk: realClock}
	for _, o := range opts {
		o(&cfg)
	}
	return &Guard{cfg: cfg, keys: make(map[string]*keyState)}
}

func (g *Guard) enabled() bool { return g.cfg.breaker || g.cfg.limiter }

// state returns key's state, creating it if create is set.
func (g *Guard) state(key string, create bool) *keyState {
	g.mu.Lock()
	defer g.mu.Unlock()
	ks := g.keys[key]
	if ks == nil && create {
		ks = &keyState{}
		if g.cfg.breaker {
			ks.breaker = newCircuitBreaker(g.cfg.threshold, g.cfg.cooldown, g.cfg.clk)
		}
		if g.cfg.limiter {
			ks.limiter = newRateLimiter(g.cfg.limit, g.cfg.period, g.cfg.clk)
		}
		g.keys[key] = ks
	}
	return ks
}

// Do runs fn under key's guards, in this order:
//
//  1. If a breaker is enabled and refuses, return ErrCircuitOpen. fn is not
//     called and the rate limiter is not touched.
//  2. If a limiter is enabled, admit per RateLimitMode: ErrRateLimited
//     (reject) or wait (block; ctx.Err() if ctx ends first). A rejected or
//     abandoned wait consumes no budget.
//  3. Call fn. No lock is held while it runs.
//  4. Record fn's outcome on the breaker (see WithFailureClassifier).
//
// Half-open: after the cooldown exactly one caller is admitted as the probe;
// concurrent callers get ErrCircuitOpen until it reports. The probe's success
// closes the circuit and its failure re-opens it. If the probe never reaches
// a verdict (the limiter refused it after the breaker admitted it, ctx ended
// while waiting or was canceled during fn, or fn panicked) its slot is
// released and the next caller becomes the probe, so the breaker cannot stay
// half-open forever. In RateLimitBlock mode the probe holds its slot while it
// waits for budget.
//
// With no options, Do is exactly fn(ctx). A nil Guard is also a passthrough.
func (g *Guard) Do(ctx context.Context, key string, fn func(context.Context) error) error {
	if g == nil || !g.enabled() {
		return fn(ctx)
	}
	ks := g.state(key, true)

	var (
		adm admission
		br  = ks.breaker
	)
	if br != nil {
		var ok bool
		if adm, ok = br.admit(); !ok {
			return ErrCircuitOpen
		}
	}
	// From here until a verdict is reached, a probe slot must be handed back
	// on every exit path, including a panic in fn.
	verdict := false
	if br != nil {
		defer func() {
			if !verdict {
				br.done(adm, outcomeAbandoned)
			}
		}()
	}

	if lim := ks.limiter; lim != nil {
		if g.cfg.mode == RateLimitBlock {
			if err := lim.Wait(ctx); err != nil {
				return err
			}
		} else if !lim.Allow() {
			return ErrRateLimited
		}
	}

	err := fn(ctx)

	if br != nil {
		verdict = true
		br.done(adm, g.classify(ctx, err))
	}
	return err
}

func (g *Guard) classify(ctx context.Context, err error) outcome {
	switch {
	case err == nil:
		return outcomeSuccess
	case errors.Is(err, context.Canceled) && ctx.Err() != nil:
		return outcomeAbandoned
	case g.cfg.classify != nil && !g.cfg.classify(err):
		// The caller says this error is not upstream ill-health: the
		// upstream answered, so it records as a success.
		return outcomeSuccess
	default:
		return outcomeFailure
	}
}

// Do is Guard.Do for a function that returns a value (a method cannot take
// its own type parameter). It applies identical admission and recording. When
// fn ran, its value and error are returned unchanged; when a guard refused
// the call the value is the zero T.
func Do[T any](ctx context.Context, g *Guard, key string, fn func(context.Context) (T, error)) (T, error) {
	var out T
	err := g.Do(ctx, key, func(ctx context.Context) error {
		var ferr error
		out, ferr = fn(ctx)
		return ferr
	})
	return out, err
}

// State returns key's circuit state without side effects; CircuitClosed for a
// key that was never used or has no breaker configured. An open circuit whose
// cooldown has elapsed still reads CircuitOpen until a call becomes the probe.
func (g *Guard) State(key string) CircuitState {
	if g == nil {
		return CircuitClosed
	}
	if ks := g.state(key, false); ks != nil && ks.breaker != nil {
		return ks.breaker.State()
	}
	return CircuitClosed
}

// Available returns how many calls key's limiter would admit right now, or -1
// when no limiter is configured. A key never used reports its full limit.
func (g *Guard) Available(key string) int {
	if g == nil || !g.cfg.limiter {
		return -1
	}
	if ks := g.state(key, false); ks != nil && ks.limiter != nil {
		return ks.limiter.Available()
	}
	return newRateLimiter(g.cfg.limit, g.cfg.period, g.cfg.clk).Available()
}

// Reset drops key's breaker and limiter state, for a caller pairing it with
// Pool.Deregister. Calls already in flight are unaffected and their results
// land on the discarded state; the next Do starts fresh. Safe to call
// concurrently with Do.
func (g *Guard) Reset(key string) {
	if g == nil {
		return
	}
	g.mu.Lock()
	delete(g.keys, key)
	g.mu.Unlock()
}
