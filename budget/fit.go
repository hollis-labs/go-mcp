package budget

// FitPrefix returns the largest k in [0, n] with size(k) <= limit, assuming
// size is non-decreasing in k, using O(log n) calls to size.
//
// Progress rule: when n > 0 and even size(1) exceeds limit, it returns
// kept=1, oversize=true. A page that keeps nothing while reporting a next
// cursor would loop forever, so one row always ships.
//
// A limit of zero or less means unbounded: kept=n. An error from size aborts
// the search.
func FitPrefix(n, limit int, size func(k int) (int, error)) (kept int, oversize bool, err error) {
	if n <= 0 {
		return 0, false, nil
	}
	if limit <= 0 {
		return n, false, nil
	}
	s, err := size(n)
	if err != nil {
		return 0, false, err
	}
	if s <= limit {
		return n, false, nil
	}
	s, err = size(1)
	if err != nil {
		return 0, false, err
	}
	if s > limit {
		return 1, true, nil
	}
	lo, hi := 1, n // size(lo) fits, size(hi) does not
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		s, err = size(mid)
		if err != nil {
			return 0, false, err
		}
		if s <= limit {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo, false, nil
}

// ArrayBytes returns a size function for the first k items of a JSON array,
// given each item's marshaled length: exactly len(json.Marshal(items[:k])),
// that is the item bytes plus brackets plus commas. k is clamped to
// [0, len(itemSizes)]; ArrayBytes(nil)(0) is 2 ("[]"). Wrap it to fit
// [FitPrefix]'s signature: func(k int) (int, error) { return f(k), nil }.
func ArrayBytes(itemSizes []int) func(k int) int {
	prefix := make([]int, len(itemSizes)+1)
	for i, s := range itemSizes {
		prefix[i+1] = prefix[i] + s
	}
	return func(k int) int {
		if k < 0 {
			k = 0
		}
		if k > len(itemSizes) {
			k = len(itemSizes)
		}
		if k == 0 {
			return 2
		}
		return 2 + prefix[k] + (k - 1)
	}
}

// BytesCap folds a byte cap and a token cap into one byte cap: the smaller
// of maxBytes and 4*maxTokens over the positive values ([EstimateTokens]
// uses ceil(n/4), so tokens <= T exactly when bytes <= 4T). Zero or negative
// values are ignored; 0 is returned when neither is positive, meaning
// unbounded.
func BytesCap(maxBytes, maxTokens int) int {
	c := 0
	if maxBytes > 0 {
		c = maxBytes
	}
	if maxTokens > 0 && (c == 0 || 4*maxTokens < c) {
		c = 4 * maxTokens
	}
	return c
}
