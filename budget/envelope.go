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
