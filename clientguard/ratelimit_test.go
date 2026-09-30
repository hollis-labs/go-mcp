package clientguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestLimiter(limit int, period time.Duration) (*RateLimiter, *fakeClock) {
	f := newFakeClock()
	return newRateLimiter(limit, period, f.clock()), f
}

func TestLimiter_AllowWithinAndOverBudget(t *testing.T) {
	r, _ := newTestLimiter(3, time.Minute)
	for i := range 3 {
		if !r.Allow() {
			t.Fatalf("call %d rejected within budget", i)
		}
	}
	if r.Allow() {
		t.Fatal("call over budget allowed")
	}
}

// A rejected Allow must not consume budget. The discriminating shape: a
// rejection at t=5s that (wrongly) recorded itself would still occupy the
// window at t=10s, when the two real calls from t=0 have expired.
func TestLimiter_RejectedAllowConsumesNothing(t *testing.T) {
	r, clk := newTestLimiter(2, 10*time.Second)
	r.Allow()
	r.Allow()
	clk.advance(5 * time.Second)
	for range 3 {
		if r.Allow() {
			t.Fatal("over-budget call allowed")
		}
	}
	if got := r.Available(); got != 0 {
		t.Fatalf("Available = %d, want 0", got)
	}
	clk.advance(5 * time.Second) // t=10s: the two calls from t=0 have left the window
	first, second := r.Allow(), r.Allow()
	if !first || !second {
		t.Fatal("budget not fully restored: a rejected Allow consumed budget")
	}
}

func TestLimiter_WindowExpiryFreesBudget(t *testing.T) {
	r, clk := newTestLimiter(1, 10*time.Second)
	r.Allow()
	clk.advance(9*time.Second + 999*time.Millisecond)
	if r.Allow() {
		t.Fatal("allowed before the entry expired")
	}
	clk.advance(time.Millisecond)
	if !r.Allow() {
		t.Fatal("not allowed after the entry expired")
	}
}

func TestLimiter_WaitTime(t *testing.T) {
	r, clk := newTestLimiter(2, 10*time.Second)
	if got := r.WaitTime(); got != 0 {
		t.Fatalf("empty WaitTime = %v", got)
	}
	r.Allow()
	clk.advance(3 * time.Second)
	r.Allow()
	if got := r.WaitTime(); got != 7*time.Second {
		t.Fatalf("WaitTime = %v, want 7s (oldest entry frees at t=10s)", got)
	}
	clk.advance(7 * time.Second)
	if got := r.WaitTime(); got != 0 {
		t.Fatalf("WaitTime after expiry = %v", got)
	}
	if !r.Allow() {
		t.Fatal("Allow false though WaitTime was 0")
	}
}

func TestLimiter_AvailableNeverNegative(t *testing.T) {
	r, _ := newTestLimiter(2, time.Minute)
	for range 10 {
		r.Allow()
	}
	if got := r.Available(); got != 0 {
		t.Fatalf("Available = %d, want 0", got)
	}
}

func TestLimiter_ClampsNonPositiveArgs(t *testing.T) {
	r := NewRateLimiter(0, 0)
	if r.limit != 1 || r.period != DefaultRateLimitWindow {
		t.Fatalf("limit=%d period=%v", r.limit, r.period)
	}
}

func TestLimiter_WaitBlocksThenProceeds(t *testing.T) {
	r, clk := newTestLimiter(1, 10*time.Second)
	r.Allow()
	done := make(chan error, 1)
	go func() { done <- r.Wait(context.Background()) }()

	<-clk.timerSet // Wait is parked on its timer
	select {
	case err := <-done:
		t.Fatalf("Wait returned before budget freed: %v", err)
	default:
	}
	clk.advance(10 * time.Second)
	if err := <-done; err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
	if r.Available() != 0 {
		t.Fatal("Wait did not record its call")
	}
}

func TestLimiter_WaitHonorsContextCancellation(t *testing.T) {
	r, clk := newTestLimiter(1, 10*time.Second)
	r.Allow()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Wait(ctx) }()
	<-clk.timerSet
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v, want context.Canceled", err)
	}
	clk.advance(10 * time.Second)
	if got := r.Available(); got != 1 {
		t.Fatalf("canceled Wait consumed budget: Available = %d", got)
	}
}

func TestLimiter_WaitWithDoneContextTouchesNothing(t *testing.T) {
	r, _ := newTestLimiter(1, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Wait(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait = %v", err)
	}
	if r.Available() != 1 {
		t.Fatal("Wait on a done ctx consumed budget")
	}
}

// Several waiters race for one freed slot: after each wait they must re-check,
// so only one proceeds per freed slot.
func TestLimiter_WaitersRecheckAfterWaking(t *testing.T) {
	r, clk := newTestLimiter(1, 10*time.Second)
	r.Allow()
	const n = 4
	var proceeded atomic.Int32
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if r.Wait(ctx) == nil {
				proceeded.Add(1)
			}
		}()
	}
	for range n {
		<-clk.timerSet
	}
	clk.advance(10 * time.Second) // frees exactly one slot
	<-clk.timerSet                // the n-1 losers re-armed after re-checking
	if got := proceeded.Load(); got > 1 {
		t.Fatalf("%d waiters proceeded on one freed slot", got)
	}
	cancel()
	wg.Wait()
	if got := proceeded.Load(); got != 1 {
		t.Fatalf("proceeded = %d, want exactly 1", got)
	}
}

func TestLimiter_ConcurrentUseRace(t *testing.T) {
	r := NewRateLimiter(50, 5*time.Millisecond)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			for range 200 {
				r.Allow()
				_ = r.Available()
				_ = r.WaitTime()
			}
			_ = r.Wait(ctx)
		}()
	}
	wg.Wait()
}
