package budget

import "fmt"

func ExampleApply() {
	items := []string{"alpha", "beta", "gamma"}
	env := Apply(items, Config{Limit: 2}, "%d items available. Add filters to narrow results.")

	fmt.Printf("%d %d %t %s\n", env.Count, env.Total, env.Truncated, env.Hint)
	// Output: 2 3 true 3 items available. Add filters to narrow results.
}

func ExampleToolJSON() {
	env := Envelope{
		Items:     []string{"alpha"},
		Count:     1,
		Total:     1,
		Truncated: false,
	}

	fmt.Println(ToolJSON(env))
	// Output: {"items":["alpha"],"count":1,"total":1}
}

func ExampleExtractPagination() {
	limit, offset := ExtractPagination(map[string]any{
		"limit":  7,
		"offset": 3,
	})

	fmt.Printf("%d %d\n", limit, offset)
	// Output: 7 3
}
