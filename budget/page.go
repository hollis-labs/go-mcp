package budget

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Page selects a window of an in-memory slice for [ApplyPage].
type Page struct {
	// Offset is the starting index when Cursor is empty. Negative is 0.
	Offset int
	// Cursor, if non-empty, is a token from a previous page's NextCursor; it
	// wins over Offset. It is verified against Fingerprint.
	Cursor string
	// Fingerprint binds issued cursors to the query (see [Fingerprint]).
	Fingerprint string
	// IssueCursors makes the envelope carry a NextCursor when more items
	// remain. It requires a non-empty Fingerprint.
	IssueCursors bool
}

// ApplyPage is [Apply] for an in-memory slice with paging: it resolves the
// start (Page.Cursor, else Page.Offset), takes at most Config.Limit items,
// enforces Config.MaxBytes / Config.MaxTokens when set, and returns an
// Envelope whose HasMore, NextCursor and TruncatedBy describe what actually
// shipped. Total is len(items); Hint (formatted with the total) is set when
// items remain. A bad Cursor yields an error satisfying
// errors.Is(err, ErrInvalidCursor) (ErrCursorMismatch for another query's
// cursor).
func ApplyPage[T any](items []T, page Page, cfg Config, hintTemplate string) (Envelope, error) {
	if page.IssueCursors && page.Fingerprint == "" {
		return Envelope{}, errors.New("budget: Page.IssueCursors requires a Fingerprint")
	}
	offset := page.Offset
	if page.Cursor != "" {
		o, err := DecodeOffset(page.Cursor, page.Fingerprint)
		if err != nil {
			return Envelope{}, err
		}
		offset = o
	}
	if offset < 0 {
		offset = 0
	}
	if offset > len(items) {
		offset = len(items)
	}
	var next func(int) (string, error)
	if page.IssueCursors {
		next = func(lastKept int) (string, error) {
			return EncodeOffset(offset+lastKept, page.Fingerprint)
		}
	}
	return seal(items[offset:], len(items), len(items), false, cfg, next, hintTemplate, true)
}

// Seal builds an Envelope for a page the store has already windowed (the
// keyset/SQL case). items is that window and hasMore says the store has rows
// beyond it. Seal applies Config.Limit, then the byte/token caps (when set),
// trimming to the largest prefix that fits (at least one item). Total is
// left unset (unknown), and hintTemplate's %d receives len(items).
//
// next is called with the number of items kept (>= 1) to mint the cursor for
// the page that follows; it is only called when more items exist, so the
// cursor reflects what shipped, not what the store returned. While fitting, next
// may also be called for candidate sizes, so it must be pure. A nil next, or
// one that returns "", emits no NextCursor (HasMore is still set). No cursor
// is ever produced for a page with zero kept items.
func Seal[T any](items []T, hasMore bool, cfg Config, next func(lastKept int) (string, error), hintTemplate string) (Envelope, error) {
	return seal(items, 0, len(items), hasMore, cfg, next, hintTemplate, true)
}

// seal is the shared core. total is the Envelope.Total (0 = unknown),
// hintArg the value formatted into the hint. When annotate is false the
// paging fields (HasMore, TruncatedBy, NextCursor) are left unset, keeping
// [Apply]'s legacy wire shape.
func seal[T any](items []T, total, hintArg int, hasMore bool, cfg Config, next func(int) (string, error), hintTemplate string, annotate bool) (Envelope, error) {
	capBytes := BytesCap(cfg.MaxBytes, cfg.MaxTokens)
	capName := "maxBytes"
	if cfg.MaxBytes <= 0 || (cfg.MaxTokens > 0 && 4*cfg.MaxTokens < cfg.MaxBytes) {
		capName = "maxTokens"
	}
	cfg = cfg.withDefaults()

	nLim := len(items)
	if cfg.Limit < nLim {
		nLim = cfg.Limit
	}

	build := func(k int, by string) (Envelope, error) {
		truncated := hasMore || k < len(items)
		env := Envelope{
			Items:      items[:k],
			Count:      k,
			Total:      total,
			Truncated:  truncated,
			TTLMs:      cfg.TTLMs,
			CacheScope: cfg.CacheScope,
		}
		if truncated {
			if hintTemplate != "" {
				env.Hint = fmt.Sprintf(hintTemplate, hintArg)
			} else {
				env.Hint = fmt.Sprintf("%d items available. Add filters to narrow results, or request a specific item by ID.", hintArg)
			}
		}
		if annotate && truncated {
			env.HasMore = true
			env.TruncatedBy = by
			if k > 0 && next != nil {
				c, err := next(k)
				if err != nil {
					return Envelope{}, err
				}
				env.NextCursor = c
			}
		}
		return env, nil
	}
	// byFor names what stopped the page at k items.
	byFor := func(k int) string {
		if k < nLim {
			return capName
		}
		return "limit"
	}

	kept := nLim
	if capBytes > 0 {
		var err error
		kept, _, err = FitPrefix(nLim, capBytes, func(k int) (int, error) {
			env, berr := build(k, byFor(k))
			if berr != nil {
				return 0, berr
			}
			b, merr := json.Marshal(env)
			return len(b), merr
		})
		if err != nil {
			return Envelope{}, err
		}
	}
	return build(kept, byFor(kept))
}
