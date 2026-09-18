package supervise

import "time"

// Policy is a bounded exponential backoff schedule for restarting a
// supervised process, plus the continuous-uptime window after which a
// caller's restart counter should reset to zero. It is pure configuration
// and arithmetic: it decides nothing about whether, when, or how to actually
// restart anything -- the caller's own loop drives a timer off Next's delay
// and StableFor, and owns the restart counter Next is indexed by.
type Policy struct {
	// Delays is the backoff schedule: Delays[n] is the delay before restart
	// attempt n (zero-based). Its length is the restart limit.
	Delays []time.Duration
	// StableFor is how long a restart must stay up before the caller's
	// restart counter resets to zero. Zero means never reset.
	StableFor time.Duration
}

// DefaultPolicy is Tether's own upstream recovery policy: five restarts at
// 1s/2s/4s/8s/16s, resetting after one minute of continuous uptime.
func DefaultPolicy() Policy {
	return Policy{
		Delays:    []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second},
		StableFor: time.Minute,
	}
}

// Limit is the number of restart attempts this policy allows before it is
// exhausted.
func (p Policy) Limit() int {
	return len(p.Delays)
}

// Next returns the backoff delay before restart attempt (zero-based) and
// true, or zero and false when attempt is at or past the policy's limit --
// the caller's signal to stop restarting and report failure instead of
// scheduling another attempt.
func (p Policy) Next(attempt int) (time.Duration, bool) {
	if attempt < 0 || attempt >= len(p.Delays) {
		return 0, false
	}
	return p.Delays[attempt], true
}
