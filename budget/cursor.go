package budget

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

// cursorVersion is the wire version written into every cursor. A decoder
// rejects any other value.
const cursorVersion = 1

// maxCursorLen bounds the token a decoder will look at, so hostile input
// cannot make it allocate without limit. Real cursors are far smaller.
const maxCursorLen = 8192

// Cursor kinds used by the built-in offset and keyset codecs.
const (
	kindOffset = "offset"
	kindKeyset = "keyset"
)

// ErrInvalidCursor reports a cursor that is malformed, of an unknown
// version, of the wrong kind, or whose state does not decode. Map it to an
// invalid-argument error at the tool boundary.
var ErrInvalidCursor = errors.New("invalid cursor")

// ErrCursorMismatch reports a well-formed cursor issued for a different
// query (its fingerprint differs from the one supplied). It satisfies
// errors.Is(err, ErrInvalidCursor) as well.
var ErrCursorMismatch = fmt.Errorf("%w: cursor was issued for a different query", ErrInvalidCursor)

// cursorWire is the decoded JSON form of a cursor.
type cursorWire struct {
	V int             `json:"v"`
	K string          `json:"k"`
	F string          `json:"f"`
	S json.RawMessage `json:"s,omitempty"`
}

// Fingerprint returns a short, stable digest (8 bytes, hex) of parts, used to
// bind a cursor to the query that produced it: pass the filters and sort
// order, not the page size. Parts are hashed as canonical JSON (map keys are
// sorted by encoding/json); callers must sort any set-like slices
// themselves.
func Fingerprint(parts ...any) string {
	b, err := json.Marshal(parts)
	if err != nil {
		b = []byte(fmt.Sprintf("%#v", parts))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// EncodeCursor builds an opaque, versioned cursor of the given kind, bound to
// fingerprint, carrying state (any JSON-marshalable value). The wire form is
// base64url(JSON{v,k,f,s}) without padding. On failure it returns an empty
// string and a non-nil error; callers must not treat "" as "no next page"
// without checking the error.
func EncodeCursor(kind, fingerprint string, state any) (string, error) {
	if kind == "" {
		return "", errors.New("budget: cursor kind must not be empty")
	}
	w := cursorWire{V: cursorVersion, K: kind, F: fingerprint}
	if state != nil {
		s, err := json.Marshal(state)
		if err != nil {
			return "", fmt.Errorf("budget: encode cursor state: %w", err)
		}
		w.S = s
	}
	b, err := json.Marshal(w)
	if err != nil {
		return "", fmt.Errorf("budget: encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// DecodeCursor parses token, checks its version, kind and fingerprint, and
// unmarshals its state into state (a pointer; nil skips the state). It
// returns an error satisfying errors.Is(err, ErrInvalidCursor) for anything
// malformed, and ErrCursorMismatch when the cursor is well formed but was
// issued for a different fingerprint. It never panics on arbitrary input.
func DecodeCursor(token, kind, fingerprint string, state any) error {
	if token == "" || len(token) > maxCursorLen {
		return fmt.Errorf("%w: empty or oversize token", ErrInvalidCursor)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return fmt.Errorf("%w: not base64url", ErrInvalidCursor)
	}
	var w cursorWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return fmt.Errorf("%w: not a cursor", ErrInvalidCursor)
	}
	if w.V != cursorVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrInvalidCursor, w.V)
	}
	if w.K != kind {
		return fmt.Errorf("%w: wrong kind %q", ErrInvalidCursor, w.K)
	}
	if w.F != fingerprint {
		return ErrCursorMismatch
	}
	if state == nil {
		return nil
	}
	if len(w.S) == 0 {
		return fmt.Errorf("%w: missing state", ErrInvalidCursor)
	}
	if err := json.Unmarshal(w.S, state); err != nil {
		return fmt.Errorf("%w: bad state", ErrInvalidCursor)
	}
	return nil
}

// Keyset is the state of a keyset (seek) cursor. SortValue is the last
// returned row's sort value, string-encoded by the application (the package
// treats it as opaque); ID is the tiebreaker and must be non-empty.
type Keyset struct {
	SortValue string `json:"sv"`
	ID        string `json:"id"`
}

type offsetState struct {
	O int `json:"o"`
}

// EncodeOffset issues an offset cursor bound to fp.
func EncodeOffset(offset int, fp string) (string, error) {
	if offset < 0 {
		return "", errors.New("budget: negative cursor offset")
	}
	return EncodeCursor(kindOffset, fp, offsetState{O: offset})
}

// DecodeOffset reads an offset cursor bound to fp. An empty token means the
// first page and returns 0, nil.
func DecodeOffset(token, fp string) (int, error) {
	if token == "" {
		return 0, nil
	}
	var s offsetState
	if err := DecodeCursor(token, kindOffset, fp, &s); err != nil {
		return 0, err
	}
	if s.O < 0 {
		return 0, fmt.Errorf("%w: negative offset", ErrInvalidCursor)
	}
	return s.O, nil
}

// EncodeKeyset issues a keyset cursor bound to fp.
func EncodeKeyset(k Keyset, fp string) (string, error) {
	if k.ID == "" {
		return "", errors.New("budget: keyset cursor needs a non-empty ID")
	}
	return EncodeCursor(kindKeyset, fp, k)
}

// DecodeKeyset reads a keyset cursor bound to fp. An empty token means the
// first page and returns the zero Keyset, nil.
func DecodeKeyset(token, fp string) (Keyset, error) {
	if token == "" {
		return Keyset{}, nil
	}
	var k Keyset
	if err := DecodeCursor(token, kindKeyset, fp, &k); err != nil {
		return Keyset{}, err
	}
	if k.ID == "" {
		return Keyset{}, fmt.Errorf("%w: keyset missing id", ErrInvalidCursor)
	}
	return k, nil
}
