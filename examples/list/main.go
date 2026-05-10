// Package main demonstrates wrapping a list-style MCP tool response with
// the budget package: extracting pagination from untyped params, applying
// truncation with a progressive-disclosure hint, and rendering the
// resulting JSON tool response.
//
// Run with: go run ./examples/list
package main

import (
	"fmt"

	"github.com/hollis-labs/go-mcp/budget"
)

// Task is a stand-in for whatever record an MCP server would return.
type Task struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// listTasks is what an MCP tool handler might call to wrap its results
// in a budget-aware envelope before returning a JSON string to the host.
func listTasks(params map[string]any, all []Task) string {
	limit, _ := budget.ExtractPagination(params)

	env := budget.Apply(
		all,
		budget.Config{Limit: limit},
		"%d tasks found. Use task_get for details.",
	)

	return budget.ToolJSON(env)
}

func main() {
	all := []Task{
		{ID: "1", Title: "first"},
		{ID: "2", Title: "second"},
		{ID: "3", Title: "third"},
		{ID: "4", Title: "fourth"},
		{ID: "5", Title: "fifth"},
	}

	// No params → DefaultLimit (10) applies; no truncation since len(all) < 10.
	fmt.Println("default:")
	fmt.Println(listTasks(map[string]any{}, all))

	// Caller-provided limit triggers truncation; envelope carries hint.
	fmt.Println("\nlimit=2:")
	fmt.Println(listTasks(map[string]any{"limit": 2}, all))
}
