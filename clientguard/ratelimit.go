package clientguard

import (
	"context"
	"sync"
	"time"
)

// DefaultRateLimitWindow is the period NewRateLimiter uses when given a
// period at or below zero.
const DefaultRateLimitWindow = 60 * time.Second

// RateLimiter is a sliding-window call-count limiter for ONE key: at most
// Limit calls in any trailing Period. It generalizes go-llm-contracts'
// TokenRateTracker (ported, not imported) from a per-call token weight to a
// flat count: every call costs 1. Safe for concurrent use.
type RateLimiter struct {
	clk    clock
	limit  int
	period time.Duration

	mu     sync.Mutex
	window []time.Time // admitted call times, oldest first
}

// NewRateLimiter returns a limiter admitting at most limit calls per period.
// A limit below 1 is clamped to 1 (a limiter that admits nothing would make
// Wait block forever); a period at or below zero selects
// DefaultRateLimitWindow.
func NewRateLimiter(limit int, period time.Duration) *RateLimiter {
	return newRateLimiter(limit, period, realClock)
}

func newRateLimiter(limit int, period time.Duration, clk clock) *RateLimiter {
	if limit < 1 {
		limit = 1
	}
	if period <= 0 {
		period = DefaultRateLimitWindow
	}
	return &RateLimiter{clk: clk, limit: limit, period: period}
}

// expireLocked drops entries that have left the trailing period.
func (r *RateLimiter) expireLocked(now time.Time) {
	i := 0
	for i < len(r.window) && now.Sub(r.window[i]) >= r.period {
		i++
	}
	if i > 0 {
		r.window = append(r.window[:0], r.window[i:]...)
	}
}

// Allow reports whether a call fits the budget and, only if it does, records
// it. Unlike go-llm-contracts' TokenRateTracker.Record (unconditional), a
// rejected Allow consumes nothing, so a caller hammering a full limiter does
// not extend its own lockout.
func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clk.now()
	r.expireLocked(now)
	if len(r.window) >= r.limit {
		return false
	}
	r.window = append(r.window, now)
	return true
}

// Available returns how many more calls fit right now; it never goes negative.
func (r *RateLimiter) Available() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.clk.now())
	return max(r.limit-len(r.window), 0)
}

// WaitTime reports how long until one more call fits the budget; 0 if one
// fits now. It does not reserve anything.
func (r *RateLimiter) WaitTime() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.clk.now()
	r.expireLocked(now)
	if len(r.window) < r.limit {
		return 0
	}
	// The window is full (len == limit): the oldest entry's expiry frees a slot.
	return max(r.window[0].Add(r.period).Sub(now), 0)
}

// Reset clears the recorded calls.
func (r *RateLimiter) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.window = r.window[:0]
}

// Wait blocks until the call fits the budget, records it and returns nil, or
// returns ctx.Err() when ctx ends first (consuming nothing). It re-runs Allow
// after every wait instead of trusting one WaitTime reading, so concurrent
// waiters racing for the same freed slot do not all proceed. A context that
// is already done is reported before any budget is touched.
func (r *RateLimiter) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.Allow() {
			return nil
		}
		d := r.WaitTime()
		if d <= 0 {
			continue // a slot freed between Allow and WaitTime; retry now
		}
		ch, stop := r.clk.after(d)
		select {
		case <-ctx.Done():
			stop()
			return ctx.Err()
		case <-ch:
		}
	}
}
