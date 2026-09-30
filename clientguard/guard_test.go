package clientguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errBoom = errors.New("boom")

func failing(context.Context) error { return errBoom }
func ok(context.Context) error      { return nil }

func TestGuard_NoOptionsIsPassthrough(t *testing.T) {
	g := New()
	if err := g.Do(context.Background(), "k", failing); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the exact fn error", err)
	}
	v, err := Do(context.Background(), g, "k", func(context.Context) (int, error) { return 7, errBoom })
	if v != 7 || !errors.Is(err, errBoom) {
		t.Fatalf("got (%v, %v), want fn's own (7, boom)", v, err)
	}
	var nilG *Guard
	if err := nilG.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("nil Guard: %v", err)
	}
	if g.State("k") != CircuitClosed || g.Available("k") != -1 {
		t.Fatal("unconfigured State/Available not zero-valued")
	}
}

func TestGuard_BreakerTripsAndSkipsFn(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk), WithCircuitBreaker(3, time.Minute))
	for range 3 {
		if err := g.Do(context.Background(), "k", failing); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
	}
	var called bool
	err := g.Do(context.Background(), "k", func(context.Context) error { called = true; return nil })
	if !errors.Is(err, ErrCircuitOpen) || called {
		t.Fatalf("err=%v called=%v, want ErrCircuitOpen without calling fn", err, called)
	}
	if g.State("k") != CircuitOpen {
		t.Fatalf("State = %v", g.State("k"))
	}
}

// Breaker first, limiter second: a call refused by an open breaker must not
// consume rate budget.
func TestGuard_OrderBreakerBeforeLimiter(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk),
		WithCircuitBreaker(1, time.Minute),
		WithRateLimit(5, time.Hour, RateLimitReject))
	_ = g.Do(context.Background(), "k", failing) // uses 1 budget, trips
	before := g.Available("k")
	for range 3 {
		if err := g.Do(context.Background(), "k", ok); !errors.Is(err, ErrCircuitOpen) {
			t.Fatalf("err = %v, want ErrCircuitOpen", err)
		}
	}
	if after := g.Available("k"); after != before {
		t.Fatalf("Available %d -> %d: refused calls touched the limiter", before, after)
	}
}

func TestGuard_LimiterRejectDoesNotCallFnOrCountAsFailure(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk),
		WithCircuitBreaker(2, time.Minute),
		WithRateLimit(1, time.Hour, RateLimitReject))
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatal(err)
	}
	var called bool
	for range 5 {
		err := g.Do(context.Background(), "k", func(context.Context) error { called = true; return nil })
		if !errors.Is(err, ErrRateLimited) {
			t.Fatalf("err = %v, want ErrRateLimited", err)
		}
	}
	if called {
		t.Fatal("fn ran though the limiter rejected")
	}
	if g.State("k") != CircuitClosed {
		t.Fatal("rate-limit rejections counted as breaker failures")
	}
}

func TestGuard_BlockModeWaitsThenProceeds(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk), WithRateLimit(1, 10*time.Second, RateLimitBlock))
	_ = g.Do(context.Background(), "k", ok)
	done := make(chan error, 1)
	var ran atomic.Bool
	go func() {
		done <- g.Do(context.Background(), "k", func(context.Context) error { ran.Store(true); return nil })
	}()
	<-clk.timerSet
	if ran.Load() {
		t.Fatal("fn ran before budget freed")
	}
	clk.advance(10 * time.Second)
	if err := <-done; err != nil || !ran.Load() {
		t.Fatalf("err=%v ran=%v", err, ran.Load())
	}
}

func TestGuard_BlockModeHonorsContext(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk), WithRateLimit(1, 10*time.Second, RateLimitBlock))
	_ = g.Do(context.Background(), "k", ok)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Do(ctx, "k", func(context.Context) error { t.Error("fn ran"); return nil }) }()
	<-clk.timerSet
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestGuard_DefaultClassifierCountsEveryNonNilError(t *testing.T) {
	g := New(withClock(newFakeClock()), WithCircuitBreaker(2, time.Minute))
	_ = g.Do(context.Background(), "k", failing)
	_ = g.Do(context.Background(), "k", failing)
	if g.State("k") != CircuitOpen {
		t.Fatal("two non-nil errors did not trip a threshold-2 breaker")
	}
}

func TestGuard_CustomClassifier(t *testing.T) {
	benign := errors.New("bad arguments")
	g := New(withClock(newFakeClock()),
		WithCircuitBreaker(1, time.Minute),
		WithFailureClassifier(func(err error) bool { return !errors.Is(err, benign) }))
	if err := g.Do(context.Background(), "k", func(context.Context) error { return benign }); !errors.Is(err, benign) {
		t.Fatalf("err = %v", err)
	}
	if g.State("k") != CircuitClosed {
		t.Fatal("classified-benign error tripped the breaker")
	}
	_ = g.Do(context.Background(), "k", failing)
	if g.State("k") != CircuitOpen {
		t.Fatal("classified-failure error did not trip")
	}
}

func TestGuard_CallerCancellationNotCounted_DeadlineIs(t *testing.T) {
	g := New(withClock(newFakeClock()), WithCircuitBreaker(1, time.Minute))
	ctx, cancel := context.WithCancel(context.Background())
	_ = g.Do(ctx, "canceled", func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	})
	if g.State("canceled") != CircuitClosed {
		t.Fatal("caller cancellation counted as an upstream failure")
	}

	dctx, dcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer dcancel()
	_ = g.Do(dctx, "slow", func(ctx context.Context) error { return ctx.Err() })
	if g.State("slow") != CircuitOpen {
		t.Fatal("an expired deadline (slow upstream) was not counted")
	}
}

func TestGuard_PerKeyIsolation(t *testing.T) {
	g := New(withClock(newFakeClock()),
		WithCircuitBreaker(1, time.Minute),
		WithRateLimit(1, time.Hour, RateLimitReject))
	_ = g.Do(context.Background(), "a", failing)
	if g.State("a") != CircuitOpen {
		t.Fatal("a not open")
	}
	if g.State("b") != CircuitClosed || g.Available("b") != 1 {
		t.Fatal("b affected by a")
	}
	if err := g.Do(context.Background(), "b", ok); err != nil {
		t.Fatalf("b: %v", err)
	}
}

func TestGuard_ResetClearsOnlyThatKey(t *testing.T) {
	g := New(withClock(newFakeClock()),
		WithCircuitBreaker(1, time.Minute),
		WithRateLimit(2, time.Hour, RateLimitReject))
	_ = g.Do(context.Background(), "a", failing)
	_ = g.Do(context.Background(), "b", failing)
	g.Reset("a")
	if g.State("a") != CircuitClosed || g.Available("a") != 2 {
		t.Fatal("Reset(a) left state behind")
	}
	if g.State("b") != CircuitOpen || g.Available("b") != 1 {
		t.Fatal("Reset(a) touched b")
	}
	if err := g.Do(context.Background(), "a", ok); err != nil {
		t.Fatalf("a after Reset: %v", err)
	}
}

func TestGuard_AvailableUnusedKeyReportsFullLimit(t *testing.T) {
	g := New(WithRateLimit(4, time.Minute, RateLimitReject))
	if got := g.Available("never"); got != 4 {
		t.Fatalf("Available = %d, want 4", got)
	}
}

func TestGuard_GenericDoAppliesSameAdmission(t *testing.T) {
	g := New(withClock(newFakeClock()), WithCircuitBreaker(1, time.Minute))
	v, err := Do(context.Background(), g, "k", func(context.Context) (string, error) { return "x", nil })
	if v != "x" || err != nil {
		t.Fatalf("got (%q, %v)", v, err)
	}
	_, _ = Do(context.Background(), g, "k", func(context.Context) (string, error) { return "", errBoom })
	v, err = Do(context.Background(), g, "k", func(context.Context) (string, error) { return "never", nil })
	if v != "" || !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("got (%q, %v), want zero value and ErrCircuitOpen", v, err)
	}
}

// tripped returns a Guard whose key "k" is half-open-ready: tripped, with the
// cooldown already elapsed.
func tripped(t *testing.T, extra ...Option) (*Guard, *fakeClock) {
	t.Helper()
	clk := newFakeClock()
	g := New(append([]Option{withClock(clk), WithCircuitBreaker(1, 10*time.Second)}, extra...)...)
	_ = g.Do(context.Background(), "k", failing)
	clk.advance(10 * time.Second)
	return g, clk
}

// Acceptance (c): after cooldown exactly one concurrent caller is the probe.
func TestGuard_HalfOpenExactlyOneProbeInFlight(t *testing.T) {
	g, _ := tripped(t)
	const n = 32
	var (
		calls   atomic.Int32
		release = make(chan struct{})
		results = make(chan error, n)
		wg      sync.WaitGroup
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- g.Do(context.Background(), "k", func(context.Context) error {
				calls.Add(1)
				<-release
				return nil
			})
		}()
	}
	var closeOnce sync.Once
	unblock := func() { closeOnce.Do(func() { close(release) }) }
	defer unblock() // never leave goroutines parked, even on failure
	// n-1 callers must be refused while the probe is parked in fn. A
	// regression that admits more parks them in fn instead, so the receive
	// is bounded: it fails fast rather than hanging the suite.
	for range n - 1 {
		select {
		case err := <-results:
			if !errors.Is(err, ErrCircuitOpen) {
				t.Fatalf("concurrent caller got %v, want ErrCircuitOpen", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("callers not refused during half-open: fn ran %d times, want exactly 1", calls.Load())
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("fn ran %d times during half-open, want exactly 1", got)
	}
	unblock()
	wg.Wait()
	if err := <-results; err != nil {
		t.Fatalf("probe result = %v", err)
	}
	if g.State("k") != CircuitClosed {
		t.Fatalf("State = %v, want closed after a successful probe", g.State("k"))
	}
}

func TestGuard_ProbeFailureReopens(t *testing.T) {
	g, clk := tripped(t)
	if err := g.Do(context.Background(), "k", failing); !errors.Is(err, errBoom) {
		t.Fatalf("probe err = %v", err)
	}
	if g.State("k") != CircuitOpen {
		t.Fatalf("State = %v", g.State("k"))
	}
	if err := g.Do(context.Background(), "k", ok); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("err = %v", err)
	}
	clk.advance(10 * time.Second)
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("second probe: %v", err)
	}
}

// The breaker admitted the probe, then the limiter refused it. Without a
// release the breaker would sit half-open with a phantom probe forever.
func TestGuard_ProbeReleasedWhenLimiterRefuses(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk),
		WithCircuitBreaker(1, 10*time.Second),
		WithRateLimit(1, time.Hour, RateLimitReject))
	_ = g.Do(context.Background(), "k", failing) // consumes the only budget, trips
	clk.advance(10 * time.Second)                // cooldown over, budget still spent

	if err := g.Do(context.Background(), "k", ok); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
	if g.State("k") != CircuitHalfOpen {
		t.Fatalf("State = %v, want half-open (no verdict)", g.State("k"))
	}
	clk.advance(time.Hour) // budget returns
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("breaker stuck half-open: next call got %v, want to run as the probe", err)
	}
	if g.State("k") != CircuitClosed {
		t.Fatalf("State = %v, want closed", g.State("k"))
	}
}

func TestGuard_ProbeReleasedWhenCallerCancelsDuringFn(t *testing.T) {
	g, _ := tripped(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := g.Do(ctx, "k", func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if g.State("k") != CircuitHalfOpen {
		t.Fatalf("State = %v, want half-open", g.State("k"))
	}
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("breaker stuck: %v", err)
	}
}

func TestGuard_ProbeReleasedWhenBlockWaitCanceled(t *testing.T) {
	clk := newFakeClock()
	g := New(withClock(clk),
		WithCircuitBreaker(1, 10*time.Second),
		WithRateLimit(1, time.Hour, RateLimitBlock))
	_ = g.Do(context.Background(), "k", failing)
	clk.advance(10 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Do(ctx, "k", func(context.Context) error { t.Error("fn ran"); return nil }) }()
	<-clk.timerSet // the probe is parked waiting for budget
	// A pre-canceled ctx keeps a regression (second caller admitted, then
	// blocked in Wait on the fake clock) from hanging the suite.
	dead, deadCancel := context.WithCancel(context.Background())
	deadCancel()
	if err := g.Do(dead, "k", ok); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("second caller during the probe's wait got %v, want ErrCircuitOpen", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	clk.advance(time.Hour)
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("breaker stuck after canceled wait: %v", err)
	}
}

func TestGuard_ProbeReleasedWhenFnPanics(t *testing.T) {
	g, _ := tripped(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was swallowed")
			}
		}()
		_ = g.Do(context.Background(), "k", func(context.Context) error { panic("kaboom") })
	}()
	if err := g.Do(context.Background(), "k", ok); err != nil {
		t.Fatalf("breaker stuck after panicking probe: %v", err)
	}
}

// fn must run with no Guard lock held: if it did, a nested Do on another key
// (and State/Reset) would deadlock.
func TestGuard_NoLockHeldWhileFnRuns(t *testing.T) {
	g := New(withClock(newFakeClock()), WithCircuitBreaker(5, time.Minute))
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = g.Do(context.Background(), "outer", func(ctx context.Context) error {
			_ = g.Do(ctx, "inner", ok)
			_ = g.State("outer")
			g.Reset("outer")
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: Guard lock held across fn")
	}
}

func TestGuard_ResetSafeAgainstInflightCalls(t *testing.T) {
	g := New(WithCircuitBreaker(2, time.Millisecond), WithRateLimit(100, time.Millisecond, RateLimitReject))
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range 300 {
				_ = g.Do(context.Background(), "k", func(context.Context) error {
					if (i+j)%3 == 0 {
						return errBoom
					}
					return nil
				})
				_ = g.State("k")
				_ = g.Available("k")
				if j%50 == 0 {
					g.Reset("k")
				}
			}
		}()
	}
	wg.Wait()
}
