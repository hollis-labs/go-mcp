package supervise

import (
	"testing"
	"time"
)

func TestPolicyNext(t *testing.T) {
	p := Policy{Delays: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}}
	for _, tc := range []struct {
		name    string
		attempt int
		want    time.Duration
		ok      bool
	}{
		{"first attempt", 0, time.Second, true},
		{"second attempt", 1, 2 * time.Second, true},
		{"last attempt", 2, 4 * time.Second, true},
		{"exhausted at limit", 3, 0, false},
		{"exhausted past limit", 10, 0, false},
		{"negative attempt", -1, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := p.Next(tc.attempt)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("Next(%d) = (%v, %v), want (%v, %v)", tc.attempt, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestPolicyLimit(t *testing.T) {
	p := Policy{Delays: []time.Duration{time.Second, 2 * time.Second}}
	if got := p.Limit(); got != 2 {
		t.Fatalf("Limit() = %d, want 2", got)
	}
	if got := (Policy{}).Limit(); got != 0 {
		t.Fatalf("Limit() on zero value = %d, want 0", got)
	}
}

func TestDefaultPolicy(t *testing.T) {
	p := DefaultPolicy()
	if p.Limit() != 5 {
		t.Fatalf("DefaultPolicy limit = %d, want 5", p.Limit())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	for i, d := range want {
		got, ok := p.Next(i)
		if !ok || got != d {
			t.Fatalf("Next(%d) = (%v, %v), want (%v, true)", i, got, ok, d)
		}
	}
	if p.StableFor != time.Minute {
		t.Fatalf("StableFor = %v, want 1m", p.StableFor)
	}
}
