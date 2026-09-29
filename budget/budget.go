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
	Limit int // max items to include (default DefaultLimit, clamped to MaxLimit)

	// MaxLimit, if positive, replaces the package MaxLimit (25) as the
	// ceiling Limit is clamped to. Zero keeps today's clamp.
	MaxLimit int

	// MaxBytes and MaxTokens cap one marshaled [Envelope] (hint and cursor
	// included). They are enforced only when set to a positive value by the
	// caller; zero means no cap. [DefaultMaxBytes] and [DefaultMaxTokens]
	// are suggested values, never applied implicitly. When both are set the
	// stricter one binds ([BytesCap]). Note a server returning the Envelope
	// directly sends it twice on the wire (StructuredContent plus the
	// mirrored text block), so size the cap accordingly.
	MaxBytes  int
	MaxTokens int

	// TTLMs, if positive, is copied onto the resulting Envelope as a
	// client-caching hint (see [Envelope.TTLMs]). It is opt-in: zero means
	// no caching metadata is added. CacheScope defaults to "public"
	// (matching the MCP spec's CacheableResult default) when TTLMs is set
	// and CacheScope is left empty.
	TTLMs      int
	CacheScope string
}

// withDefaults returns a copy of cfg with zero values replaced by defaults,
// and Limit clamped to [1, MaxLimit]. MaxBytes and MaxTokens are left as the
// caller set them: zero means no cap.
func (cfg Config) withDefaults() Config {
	if cfg.Limit <= 0 {
		cfg.Limit = DefaultLimit
	}
	ceiling := MaxLimit
	if cfg.MaxLimit > 0 {
		ceiling = cfg.MaxLimit
	}
	cfg.Limit = Clamp(cfg.Limit, 1, ceiling)

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
//
// When the caller sets Config.MaxBytes and/or Config.MaxTokens, Apply also
// trims to the largest prefix whose marshaled Envelope fits (at least one
// item), and sets HasMore and TruncatedBy on the result. With neither set
// the output is exactly what it always was. Apply issues no cursor; see
// [ApplyPage] and [Seal].
func Apply[T any](items []T, cfg Config, hintTemplate string) Envelope {
	if BytesCap(cfg.MaxBytes, cfg.MaxTokens) > 0 {
		// A marshal failure falls back to count-only truncation below.
		if env, err := seal(items, len(items), len(items), false, cfg, nil, hintTemplate, true); err == nil {
			return env
		}
	}
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
