package clientguard

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestBreaker(threshold int, cooldown time.Duration) (*CircuitBreaker, *fakeClock) {
	f := newFakeClock()
	return newCircuitBreaker(threshold, cooldown, f.clock()), f
}

func TestBreaker_TripsAtExactlyThreshold(t *testing.T) {
	cb, _ := newTestBreaker(3, time.Minute)
	for i := 1; i <= 2; i++ {
		if cb.RecordFailure() {
			t.Fatalf("failure %d tripped early", i)
		}
		if cb.State() != CircuitClosed {
			t.Fatalf("state after %d failures = %v", i, cb.State())
		}
	}
	if !cb.RecordFailure() {
		t.Fatal("third failure did not report tripped")
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open", cb.State())
	}
}

func TestBreaker_SuccessResetsConsecutiveCount(t *testing.T) {
	cb, _ := newTestBreaker(2, time.Minute)
	cb.RecordFailure()
	cb.RecordSuccess()
	if cb.RecordFailure() {
		t.Fatal("non-consecutive failures tripped the breaker")
	}
}

func TestBreaker_OpenRefusesUntilCooldown(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	if cb.Allow() {
		t.Fatal("Allow true immediately after trip")
	}
	clk.advance(9 * time.Second)
	if cb.Allow() {
		t.Fatal("Allow true before cooldown elapsed")
	}
	clk.advance(time.Second)
	if !cb.Allow() {
		t.Fatal("Allow false once cooldown elapsed")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("state = %v, want half-open", cb.State())
	}
}

func TestBreaker_HalfOpenProbeFailureReopens(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	clk.advance(10 * time.Second)
	cb.Allow()
	if cb.RecordFailure() {
		t.Fatal("probe failure reported a fresh trip")
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open", cb.State())
	}
	// Cooldown restarts from the probe's failure.
	clk.advance(9 * time.Second)
	if cb.Allow() {
		t.Fatal("Allow true before the restarted cooldown elapsed")
	}
	clk.advance(time.Second)
	if !cb.Allow() {
		t.Fatal("Allow false after the restarted cooldown")
	}
}

func TestBreaker_HalfOpenProbeSuccessCloses(t *testing.T) {
	cb, clk := newTestBreaker(2, 10*time.Second)
	cb.RecordFailure()
	cb.RecordFailure()
	clk.advance(10 * time.Second)
	cb.Allow()
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Fatalf("state = %v, want closed", cb.State())
	}
	// fails was reset: one failure must not trip a threshold-2 breaker.
	if cb.RecordFailure() {
		t.Fatal("fail count survived the probe success")
	}
}

func TestBreaker_ResetForcesClosedFromAnyState(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	cb.Reset()
	if cb.State() != CircuitClosed || !cb.Allow() {
		t.Fatal("Reset did not close an open breaker")
	}
	cb.RecordFailure()
	clk.advance(10 * time.Second)
	cb.Allow() // half-open, probe in flight
	cb.Reset()
	if cb.State() != CircuitClosed || !cb.Allow() {
		t.Fatal("Reset did not close a half-open breaker")
	}
}

func TestBreaker_DefaultsForNonPositiveArgs(t *testing.T) {
	cb := NewCircuitBreaker(0, 0)
	if cb.threshold != DefaultThreshold || cb.cooldown != DefaultCooldown {
		t.Fatalf("got threshold=%d cooldown=%v", cb.threshold, cb.cooldown)
	}
}

func TestBreaker_HalfOpenAdmitsExactlyOneProbeConcurrently(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	clk.advance(10 * time.Second)

	const n = 64
	var (
		admitted atomic.Int32
		wg       sync.WaitGroup
		start    = make(chan struct{})
	)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if cb.Allow() {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := admitted.Load(); got != 1 {
		t.Fatalf("%d callers admitted through a half-open breaker, want exactly 1", got)
	}
}

func TestBreaker_ReleaseHandsProbeToNextCaller(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	clk.advance(10 * time.Second)
	if !cb.Allow() {
		t.Fatal("first probe refused")
	}
	if cb.Allow() {
		t.Fatal("second caller admitted while the probe is in flight")
	}
	cb.Release()
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("state after Release = %v, want half-open", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("no new probe after Release")
	}
}

func TestBreaker_ReleaseOutsideHalfOpenIsNoop(t *testing.T) {
	cb, _ := newTestBreaker(1, time.Minute)
	cb.Release()
	if cb.State() != CircuitClosed || !cb.Allow() {
		t.Fatal("Release disturbed a closed breaker")
	}
	cb.RecordFailure()
	cb.Release()
	if cb.State() != CircuitOpen {
		t.Fatal("Release disturbed an open breaker")
	}
}

func TestBreaker_StaleResultsIgnored(t *testing.T) {
	cb, clk := newTestBreaker(2, 10*time.Second)
	slow, ok := cb.admit() // admitted while closed, still running
	if !ok {
		t.Fatal("admit refused")
	}
	cb.RecordFailure()
	cb.RecordFailure() // trips
	clk.advance(10 * time.Second)
	probe, ok := cb.admit()
	if !ok || !probe.probe {
		t.Fatal("probe not admitted")
	}

	// The slow pre-trip call reports success; it must not close the circuit
	// under the probe's feet.
	cb.done(slow, outcomeSuccess)
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("stale success moved state to %v", cb.State())
	}
	cb.done(slow, outcomeFailure) // nor re-open it
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("stale failure moved state to %v", cb.State())
	}

	cb.done(probe, outcomeSuccess)
	if cb.State() != CircuitClosed {
		t.Fatalf("probe success left state %v", cb.State())
	}
}

func TestBreaker_ResetIgnoresInflightProbeResult(t *testing.T) {
	cb, clk := newTestBreaker(1, 10*time.Second)
	cb.RecordFailure()
	clk.advance(10 * time.Second)
	probe, _ := cb.admit()
	cb.Reset()
	cb.done(probe, outcomeFailure) // late result of a pre-Reset probe
	if cb.State() != CircuitClosed {
		t.Fatalf("pre-Reset probe failure re-opened a reset breaker: %v", cb.State())
	}
}

func TestBreaker_ConcurrentUseRace(t *testing.T) {
	cb := NewCircuitBreaker(3, time.Millisecond)
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				cb.Allow()
				switch (i + g) % 4 {
				case 0:
					cb.RecordFailure()
				case 1:
					cb.RecordSuccess()
				case 2:
					cb.Release()
				default:
					_ = cb.State()
				}
				if i%97 == 0 {
					cb.Reset()
				}
			}
		}()
	}
	wg.Wait()
}
