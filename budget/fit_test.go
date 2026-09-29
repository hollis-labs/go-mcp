package budget

import (
	"encoding/json"
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func TestFitPrefix_PropertyMatchesLinearScan(t *testing.T) {
	r := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic test data
	for iter := 0; iter < 2000; iter++ {
		n := r.Intn(40)
		sizes := make([]int, n)
		for i := range sizes {
			sizes[i] = r.Intn(60)
		}
		f := ArrayBytes(sizes)
		limit := 1 + r.Intn(800)
		calls := 0
		kept, oversize, err := FitPrefix(n, limit, func(k int) (int, error) { calls++; return f(k), nil })
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for k := 0; k <= n; k++ {
			if f(k) <= limit {
				want = k
			}
		}
		wantOver := false
		if n > 0 && want == 0 {
			want, wantOver = 1, true
		}
		if kept != want || oversize != wantOver {
			t.Fatalf("n=%d limit=%d sizes=%v: got (%d,%v) want (%d,%v)", n, limit, sizes, kept, oversize, want, wantOver)
		}
		if bound := 2*bitLen(n) + 3; calls > bound {
			t.Fatalf("n=%d: %d calls, want O(log n) <= %d", n, calls, bound)
		}
	}
}

func bitLen(n int) int {
	b := 0
	for n > 0 {
		b++
		n >>= 1
	}
	return b
}

func TestFitPrefix_EdgeCases(t *testing.T) {
	size := func(k int) (int, error) { return k * 10, nil }
	if k, o, _ := FitPrefix(0, 5, size); k != 0 || o {
		t.Fatal(k, o)
	}
	if k, o, _ := FitPrefix(5, 5, size); k != 1 || !o {
		t.Fatalf("progress rule: %d %v", k, o)
	}
	if k, o, _ := FitPrefix(5, 0, size); k != 5 || o {
		t.Fatalf("unbounded: %d %v", k, o)
	}
	if k, o, _ := FitPrefix(5, 1000, size); k != 5 || o {
		t.Fatal(k, o)
	}
	boom := errors.New("boom")
	if _, _, err := FitPrefix(5, 20, func(int) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatal(err)
	}
}

func TestArrayBytes_MatchesMarshal(t *testing.T) {
	r := rand.New(rand.NewSource(3)) //nolint:gosec // deterministic test data
	for iter := 0; iter < 200; iter++ {
		n := r.Intn(20)
		items := make([]any, n)
		sizes := make([]int, n)
		for i := range items {
			switch r.Intn(3) {
			case 0:
				items[i] = strings.Repeat("é\"", r.Intn(10))
			case 1:
				items[i] = map[string]int{"a": r.Intn(1e6), "b": i}
			default:
				items[i] = r.Intn(1000)
			}
			b, _ := json.Marshal(items[i])
			sizes[i] = len(b)
		}
		f := ArrayBytes(sizes)
		for k := 0; k <= n; k++ {
			b, _ := json.Marshal(items[:k])
			if f(k) != len(b) {
				t.Fatalf("k=%d: %d != %d", k, f(k), len(b))
			}
		}
		if f(-3) != 2 || f(n+5) != f(n) {
			t.Fatal("clamp")
		}
	}
	if ArrayBytes(nil)(0) != 2 {
		t.Fatal("empty")
	}
}

func TestBytesCap(t *testing.T) {
	cases := []struct{ b, tk, want int }{
		{0, 0, 0}, {100, 0, 100}, {0, 10, 40}, {100, 10, 40}, {30, 10, 30},
		{-5, 10, 40}, {100, -1, 100}, {-1, -1, 0},
	}
	for _, c := range cases {
		if got := BytesCap(c.b, c.tk); got != c.want {
			t.Errorf("BytesCap(%d,%d)=%d want %d", c.b, c.tk, got, c.want)
		}
	}
	// tokens <= T exactly when bytes <= 4T
	for n := 0; n < 50; n++ {
		if (EstimateTokens(make([]byte, n)) <= 5) != (n <= 20) {
			t.Fatalf("n=%d", n)
		}
	}
}
