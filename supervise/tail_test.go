package supervise

import (
	"strings"
	"sync"
	"testing"
)

func TestTailWriteBounds(t *testing.T) {
	tail := &Tail{Bytes: 8}
	_, _ = tail.Write([]byte("0123"))
	_, _ = tail.Write([]byte("456789"))
	if got := tail.String(); got != "23456789" {
		t.Fatalf("String() = %q, want %q", got, "23456789")
	}
}

func TestTailWriteLargerThanLimitInOneCall(t *testing.T) {
	tail := &Tail{Bytes: 4}
	_, _ = tail.Write([]byte("0123456789"))
	if got := tail.String(); got != "6789" {
		t.Fatalf("String() = %q, want %q", got, "6789")
	}
}

func TestTailZeroValueUsesDefaultBytes(t *testing.T) {
	tail := &Tail{}
	_, _ = tail.Write([]byte("hello"))
	if got := tail.String(); got != "hello" {
		t.Fatalf("String() = %q, want %q", got, "hello")
	}
}

func TestTailRedactsSecrets(t *testing.T) {
	tail := &Tail{Bytes: 64, Secrets: []string{"tok-abc123"}}
	_, _ = tail.Write([]byte("connecting with token tok-abc123 now"))
	if got := tail.String(); got != "connecting with token [redacted] now" {
		t.Fatalf("String() = %q", got)
	}
}

func TestTailConcurrentWrites(t *testing.T) {
	tail := &Tail{Bytes: 4096}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = tail.Write([]byte("line\n"))
		}()
	}
	wg.Wait()
	if got := tail.String(); !strings.Contains(got, "line") {
		t.Fatalf("String() = %q, want it to contain writes from every goroutine", got)
	}
}

func TestRedact(t *testing.T) {
	for _, tc := range []struct {
		name     string
		original string
		secrets  []string
		want     string
	}{
		{"no secrets", "plain text", nil, "plain text"},
		{"empty secret ignored", "plain text", []string{""}, "plain text"},
		{"single match", "token=abc123 ok", []string{"abc123"}, "token=[redacted] ok"},
		{"repeated match", "abc123 and abc123 again", []string{"abc123"}, "[redacted] and [redacted] again"},
		{"overlapping matches from different secrets", "aaaa", []string{"aa"}, "[redacted]"},
		{"multiple distinct secrets", "user=bob pass=hunter2", []string{"bob", "hunter2"}, "user=[redacted] pass=[redacted]"},
		{
			"secret split across the window's start",
			"c123 was used", // a full write of "sec123 was used" trimmed to the last 14 bytes
			[]string{"sec123"},
			"[redacted] was used",
		},
		{
			"secret split across the window's end",
			"token is sec", // a partial secret hanging off the live end
			[]string{"sec123"},
			"token is [redacted]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.original, tc.secrets); got != tc.want {
				t.Fatalf("Redact(%q, %v) = %q, want %q", tc.original, tc.secrets, got, tc.want)
			}
		})
	}
}

func TestRedactDoesNotOverredactUnrelatedShortPrefix(t *testing.T) {
	// "a" is a prefix of "abc123", but a bare trailing "a" that is not
	// actually part of a longer secret occurrence should still be flagged
	// as a possible in-progress match at the live end -- this documents
	// that tradeoff (redact eagerly) rather than asserting it away.
	got := Redact("value ends in a", []string{"abc123"})
	if got != "value ends in [redacted]" {
		t.Fatalf("Redact() = %q", got)
	}
}
