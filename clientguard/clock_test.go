package clientguard

import (
	"sync"
	"time"
)

// fakeClock is a manually advanced clock. Timers fire only from advance, and
// timerSet is signaled when a timer is registered so a test can wait for a
// goroutine to be blocked before advancing, without sleeping.
type fakeClock struct {
	mu       sync.Mutex
	t        time.Time
	timers   []*fakeTimer
	timerSet chan struct{}
}

type fakeTimer struct {
	at      time.Time
	ch      chan time.Time
	stopped bool
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_000_000, 0), timerSet: make(chan struct{}, 64)}
}

func (f *fakeClock) clock() clock {
	return clock{
		now: func() time.Time {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.t
		},
		after: func(d time.Duration) (<-chan time.Time, func()) {
			f.mu.Lock()
			tm := &fakeTimer{at: f.t.Add(d), ch: make(chan time.Time, 1)}
			f.timers = append(f.timers, tm)
			f.mu.Unlock()
			f.timerSet <- struct{}{}
			return tm.ch, func() {
				f.mu.Lock()
				tm.stopped = true
				f.mu.Unlock()
			}
		},
	}
}

func (f *fakeClock) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
	for _, tm := range f.timers {
		if !tm.stopped && !tm.at.After(f.t) {
			tm.stopped = true
			tm.ch <- f.t
		}
	}
}

// withClock is a test-only Option installing a fake clock.
func withClock(f *fakeClock) Option {
	return func(c *guardConfig) { c.clk = f.clock() }
}
