package budget

import "fmt"

const (
	// DefaultLimit is the default number of items returned in a list response.
	DefaultLimit = 10
	// MaxLimit is the maximum number of items a caller can request.
	MaxLimit = 25
	// DefaultMaxTokens is the target token budget for a single response (~2000 tokens).
	DefaultMaxTokens = 2000
	// DefaultMaxBytes is the target byte budget for a single response (~8000 bytes).
	DefaultMaxBytes = 8000
)

// Config holds budget parameters for a response.
type Config struct {
	Limit     int // max items to include (default DefaultLimit, max MaxLimit)
	MaxBytes  int // max response bytes (default DefaultMaxBytes)
	MaxTokens int // max estimated tokens (default DefaultMaxTokens)

	// TTLMs, if positive, is copied onto the resulting Envelope as a
	// client-caching hint (see [Envelope.TTLMs]). It is opt-in: zero means
	// no caching metadata is added. CacheScope defaults to "public"
	// (matching the MCP spec's CacheableResult default) when TTLMs is set
	// and CacheScope is left empty.
	TTLMs      int
	CacheScope string
}

// withDefaults returns a copy of cfg with zero values replaced by defaults,
// and Limit clamped to [1, MaxLimit].
func (cfg Config) withDefaults() Config {
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultLimit
	}
	cfg.Limit = Clamp(cfg.Limit, 1, MaxLimit)

	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = DefaultMaxTokens
	}
	if cfg.TTLMs > 0 && cfg.CacheScope == "" {
		cfg.CacheScope = "public"
	}
	return cfg
}

// Apply takes a slice of items and a hint template, and returns an Envelope
// that respects the budget config. Items beyond the limit are counted but not
// included in the response. The hintTemplate is a format string that receives
// the total count as its first %d argument (e.g., "%d tasks found. Use
// task_get for details.").
//
// If hintTemplate is empty and the response is truncated, a generic hint is
// used.
func Apply[T any](items []T, cfg Config, hintTemplate string) Envelope {
	cfg = cfg.withDefaults()

	total := len(items)
	limit := cfg.Limit
	if limit > total {
		limit = total
	}

	truncated := total > limit
	included := items[:limit]

	var hint string
	if truncated {
		if hintTemplate != "" {
			hint = fmt.Sprintf(hintTemplate, total)
		} else {
			hint = fmt.Sprintf("%d items available. Add filters to narrow results, or request a specific item by ID.", total)
		}
	}

	return Envelope{
		Items:      included,
		Count:      limit,
		Total:      total,
		Truncated:  truncated,
		Hint:       hint,
		TTLMs:      cfg.TTLMs,
		CacheScope: cfg.CacheScope,
	}
}
