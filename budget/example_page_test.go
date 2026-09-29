package budget

import (
	"errors"
	"fmt"
)

func ExampleApplyPage() {
	items := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	fp := Fingerprint("list", "all") // bind cursors to the query, not the page size

	cursor := ""
	for {
		env, err := ApplyPage(items, Page{Cursor: cursor, Fingerprint: fp, IssueCursors: true},
			Config{Limit: 2}, "%d items available.")
		if err != nil {
			panic(err)
		}
		fmt.Println(env.Items, env.HasMore, env.TruncatedBy)
		if env.NextCursor == "" {
			break
		}
		cursor = env.NextCursor
	}
	// Output:
	// [alpha beta] true limit
	// [gamma delta] true limit
	// [epsilon] false
}

func ExampleFitPrefix() {
	itemSizes := []int{10, 10, 10, 10} // marshaled length of each item
	arr := ArrayBytes(itemSizes)
	kept, oversize, _ := FitPrefix(len(itemSizes), 25, func(k int) (int, error) { return arr(k), nil })
	fmt.Println(kept, oversize)
	// Output: 2 false
}

func ExampleEncodeOffset() {
	fp := Fingerprint("status", "open")
	tok, _ := EncodeOffset(20, fp)

	off, err := DecodeOffset(tok, fp)
	fmt.Println(off, err)

	_, err = DecodeOffset(tok, Fingerprint("status", "closed"))
	fmt.Println(errors.Is(err, ErrCursorMismatch), errors.Is(err, ErrInvalidCursor))
	// Output:
	// 20 <nil>
	// true true
}

func ExampleSeal() {
	// The store already returned one window of rows plus "there are more".
	rows := []string{"a", "b", "c"}
	env, _ := Seal(rows, true, Config{Limit: 2}, func(lastKept int) (string, error) {
		return fmt.Sprintf("after:%s", rows[lastKept-1]), nil
	}, "")
	fmt.Println(env.Items, env.NextCursor, env.TruncatedBy)
	// Output: [a b] after:b limit
}
