package supervise

import (
	"sort"
	"strings"
	"sync"
)

// DefaultTailBytes is the retained-window size Tether itself uses for a
// redacted stderr tail.
const DefaultTailBytes = 8192

// Tail is a bounded, concurrent-safe ring buffer for a supervised process's
// stderr (or any byte stream worth capturing but not retaining in full),
// with configured secret values redacted from every read.
//
// Write from the process's Stderr pipe continuously, so the process never
// blocks on a full pipe -- Tail retains only the newest Bytes bytes written,
// dropping the rest. Redaction happens in String, not Write, so a secret
// split across a write boundary is still caught once its full text has
// landed in the retained window; Redact separately handles a secret's
// fragment truncated off either edge of that window.
//
// The zero value is ready to use, with DefaultTailBytes as its retained
// window and no redaction.
type Tail struct {
	// Bytes is the retained window size. Zero means DefaultTailBytes.
	Bytes int
	// Secrets are exact values redacted from String's output.
	Secrets []string

	mu   sync.Mutex
	data []byte
}

func (t *Tail) limit() int {
	if t.Bytes > 0 {
		return t.Bytes
	}
	return DefaultTailBytes
}

// Write implements io.Writer, so a Tail can be assigned directly to an
// exec.Cmd's Stderr field. It never returns an error and never blocks past
// the ring-buffer copy.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	limit := t.limit()
	n := len(p)
	if len(p) >= limit {
		t.data = append(t.data[:0], p[len(p)-limit:]...)
	} else {
		t.data = append(t.data, p...)
		if len(t.data) > limit {
			t.data = append(t.data[:0], t.data[len(t.data)-limit:]...)
		}
	}
	return n, nil
}

// String returns the retained window with every configured secret redacted.
func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Redact(string(t.data), t.Secrets)
	if limit := t.limit(); len(s) > limit {
		s = s[len(s)-limit:]
	}
	return s
}

type redactionRange struct {
	start int
	end   int
}

// Redact replaces every occurrence of each secret in original with
// "[redacted]", including a secret's leading fragment truncated off the end
// of original and its trailing fragment truncated off the start -- the shape
// a secret takes when a bounded tail is read mid-stream, split across the
// retained window's edge. Matches are found against a single immutable
// snapshot, so redacting one secret never changes the offsets another
// secret's search depends on. Empty secret values are ignored.
func Redact(original string, secrets []string) string {
	ranges := make([]redactionRange, 0, len(secrets))
	for _, secret := range secrets {
		if secret == "" {
			continue
		}
		for from := 0; from < len(original); {
			offset := strings.Index(original[from:], secret)
			if offset < 0 {
				break
			}
			start := from + offset
			ranges = append(ranges, redactionRange{start: start, end: start + len(secret)})
			from = start + 1 // retain overlapping matches
		}
		// The retained window may begin partway through a configured value.
		for n := min(len(secret)-1, len(original)); n > 0; n-- {
			if strings.HasPrefix(original, secret[len(secret)-n:]) {
				ranges = append(ranges, redactionRange{end: n})
				break
			}
		}
		// A snapshot may be read between arbitrary stderr writes. Cover the
		// longest unfinished value prefix at the live end before publishing it.
		for n := min(len(secret)-1, len(original)); n > 0; n-- {
			if strings.HasSuffix(original, secret[:n]) {
				ranges = append(ranges, redactionRange{start: len(original) - n, end: len(original)})
				break
			}
		}
	}
	if len(ranges) == 0 {
		return original
	}
	sort.Slice(ranges, func(i, j int) bool {
		if ranges[i].start != ranges[j].start {
			return ranges[i].start < ranges[j].start
		}
		return ranges[i].end < ranges[j].end
	})
	merged := ranges[:1]
	for _, next := range ranges[1:] {
		last := &merged[len(merged)-1]
		if next.start <= last.end {
			last.end = max(last.end, next.end)
			continue
		}
		merged = append(merged, next)
	}
	var out strings.Builder
	out.Grow(len(original))
	cursor := 0
	for _, match := range merged {
		out.WriteString(original[cursor:match.start])
		out.WriteString("[redacted]")
		cursor = match.end
	}
	out.WriteString(original[cursor:])
	return out.String()
}
