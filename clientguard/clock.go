package clientguard

import "time"

// clock is the package's time seam. Production code uses realClock; tests
// substitute a fake so nothing here sleeps or races the wall clock.
type clock struct {
	now func() time.Time
	// after returns a channel that fires once after d, and a stop func that
	// releases the timer.
	after func(d time.Duration) (<-chan time.Time, func())
}

var realClock = clock{
	now: time.Now,
	after: func(d time.Duration) (<-chan time.Time, func()) {
		t := time.NewTimer(d)
		return t.C, func() { t.Stop() }
	},
}
