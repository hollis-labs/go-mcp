// Package budget provides MCP response budget enforcement utilities.
//
// All Fragments Engine MCP servers (Engine, Hadron, Cortex) use this package to ensure
// list responses stay within a ~2000-token budget (~8000 bytes of serialized
// JSON). See ADR-006 for the full contract.
//
// This package is dependency-free (stdlib only) so it can be imported by any
// MCP implementation without pulling in framework dependencies.
package budget

// Envelope wraps list responses with pagination and truncation metadata.
// Callers serialize this to JSON via [ToolJSON] and return the resulting
// string in their MCP framework's response type.
type Envelope struct {
	Items     any    `json:"items"`
	Count     int    `json:"count"`               // items in this response
	Total     int    `json:"total,omitempty"`     // total available (before truncation)
	Truncated bool   `json:"truncated,omitempty"` // true if items were omitted
	Hint      string `json:"hint,omitempty"`      // progressive disclosure hint
}
