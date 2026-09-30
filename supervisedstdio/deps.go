package supervisedstdio

import "time"

// timer is the slice of time.Timer the supervisor uses, so tests can drive the
// backoff and stable-uptime clocks and see exactly which durations were asked for.
type timer interface {
	C() <-chan time.Time
	Stop()
}

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop()               { r.t.Stop() }

// deps are the clocks a Connection uses. The production values are real timers;
// only tests substitute them.
type deps struct {
	backoff  func(time.Duration) timer // the wait before a restart
	stable   func(time.Duration) timer // the uptime after which the budget resets
	shutdown func(time.Duration) timer // the wait for a child to exit on Close
	now      func() time.Time
}

func realDeps() deps {
	mk := func(d time.Duration) timer { return realTimer{t: time.NewTimer(d)} }
	return deps{backoff: mk, stable: mk, shutdown: mk, now: func() time.Time { return time.Now().UTC() }}
}
