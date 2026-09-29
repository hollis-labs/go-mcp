package budget

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestCursor_RoundTrip(t *testing.T) {
	type st struct {
		A int    `json:"a"`
		B string `json:"b"`
	}
	tok, err := EncodeCursor("k", "fp", st{3, "x"})
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	if strings.ContainsAny(tok, "+/=") {
		t.Fatalf("not url-safe/unpadded: %q", tok)
	}
	var got st
	if err := DecodeCursor(tok, "k", "fp", &got); err != nil || got != (st{3, "x"}) {
		t.Fatalf("%v %+v", err, got)
	}
}

func TestCursor_Errors(t *testing.T) {
	good, _ := EncodeCursor("k", "fp", map[string]int{"o": 1})
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	cases := map[string]string{
		"empty":       "",
		"not base64":  "!!!",
		"not json":    enc("hello"),
		"json array":  enc("[1]"),
		"bad version": enc(`{"v":2,"k":"k","f":"fp","s":{}}`),
		"no version":  enc(`{"k":"k","f":"fp","s":{}}`),
		"wrong kind":  enc(`{"v":1,"k":"z","f":"fp","s":{}}`),
		"no state":    enc(`{"v":1,"k":"k","f":"fp"}`),
		"bad state":   enc(`{"v":1,"k":"k","f":"fp","s":"str"}`),
		"oversize":    strings.Repeat("A", maxCursorLen+1),
		"truncated":   good[:len(good)/2],
	}
	for name, tok := range cases {
		var s struct{ O int }
		err := DecodeCursor(tok, "k", "fp", &s)
		if !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: err = %v", name, err)
		}
		if errors.Is(err, ErrCursorMismatch) {
			t.Errorf("%s: should not be mismatch", name)
		}
	}
}

func TestCursor_MismatchIsInvalid(t *testing.T) {
	tok, _ := EncodeCursor("k", "A", nil)
	err := DecodeCursor(tok, "k", "B", nil)
	if !errors.Is(err, ErrCursorMismatch) || !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("%v", err)
	}
	if !errors.Is(ErrCursorMismatch, ErrInvalidCursor) {
		t.Fatal("ErrCursorMismatch must wrap ErrInvalidCursor")
	}
}

func TestEncodeCursor_FailureNeverEmptyWithoutError(t *testing.T) {
	tok, err := EncodeCursor("k", "f", make(chan int))
	if err == nil || tok != "" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if _, err := EncodeCursor("", "f", nil); err == nil {
		t.Fatal("empty kind accepted")
	}
}

func TestOffsetCursor(t *testing.T) {
	if o, err := DecodeOffset("", "f"); o != 0 || err != nil {
		t.Fatal(o, err)
	}
	tok, err := EncodeOffset(42, "f")
	if err != nil {
		t.Fatal(err)
	}
	if o, err := DecodeOffset(tok, "f"); o != 42 || err != nil {
		t.Fatal(o, err)
	}
	if _, err := DecodeOffset(tok, "g"); !errors.Is(err, ErrCursorMismatch) {
		t.Fatal(err)
	}
	if _, err := EncodeOffset(-1, "f"); err == nil {
		t.Fatal("negative accepted")
	}
	neg, _ := EncodeCursor(kindOffset, "f", offsetState{O: -5})
	if _, err := DecodeOffset(neg, "f"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal(err)
	}
	ks, _ := EncodeKeyset(Keyset{ID: "a"}, "f")
	if _, err := DecodeOffset(ks, "f"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("keyset cursor accepted as offset: %v", err)
	}
}

func TestKeysetCursor(t *testing.T) {
	if k, err := DecodeKeyset("", "f"); k != (Keyset{}) || err != nil {
		t.Fatal(k, err)
	}
	want := Keyset{SortValue: "2026-01-02T03:04:05.123456789Z", ID: "T-9"}
	tok, err := EncodeKeyset(want, "f")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := DecodeKeyset(tok, "f"); got != want || err != nil {
		t.Fatal(got, err)
	}
	if _, err := DecodeKeyset(tok, "other-sort"); !errors.Is(err, ErrCursorMismatch) {
		t.Fatal(err)
	}
	if _, err := EncodeKeyset(Keyset{SortValue: "x"}, "f"); err == nil {
		t.Fatal("missing id accepted")
	}
	noID, _ := EncodeCursor(kindKeyset, "f", map[string]string{"sv": "x"})
	if _, err := DecodeKeyset(noID, "f"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatal(err)
	}
}

func TestFingerprint(t *testing.T) {
	a := Fingerprint("status", "open", map[string]any{"b": 1, "a": 2})
	b := Fingerprint("status", "open", map[string]any{"a": 2, "b": 1})
	if a != b || len(a) != 16 {
		t.Fatalf("%q %q", a, b)
	}
	if a == Fingerprint("status", "closed", map[string]any{"a": 2, "b": 1}) {
		t.Fatal("collision")
	}
	if Fingerprint(make(chan int)) == "" {
		t.Fatal("unmarshalable parts should still fingerprint")
	}
}

func FuzzDecodeCursor(f *testing.F) {
	good, _ := EncodeOffset(3, "fp")
	ks, _ := EncodeKeyset(Keyset{SortValue: "s", ID: "i"}, "fp")
	for _, s := range []string{"", good, ks, "e30", "bnVsbA", "W10", "eyJ2IjoxfQ", strings.Repeat("A", 100)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, tok string) {
		var st offsetState
		err := DecodeCursor(tok, kindOffset, "fp", &st)
		if err != nil && !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("error not ErrInvalidCursor: %v", err)
		}
		_, _ = DecodeOffset(tok, "fp")
		_, _ = DecodeKeyset(tok, "fp")
		_ = DecodeCursor(tok, "x", "y", nil)
	})
}
