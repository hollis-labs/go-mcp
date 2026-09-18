package budget

// Envelope wraps list responses with pagination, truncation, and optional
// client-caching metadata. Callers serialize this to JSON via [ToolJSON] and
// return the resulting string in their MCP framework's response type.
type Envelope struct {
	Items     any    `json:"items"`
	Count     int    `json:"count"`               // items in this response
	Total     int    `json:"total,omitempty"`     // total available (before truncation)
	Truncated bool   `json:"truncated,omitempty"` // true if items were omitted
	Hint      string `json:"hint,omitempty"`      // progressive disclosure hint

	// TTLMs and CacheScope mirror the MCP 2026-07-28 spec's CacheableResult
	// shape (the ttlMs/cacheScope fields on tools/list, resources/list,
	// resources/read, prompts/list, and resources/templates/list results):
	// a hint for how long, and how broadly, a client may cache this
	// response. Both are zero/empty by default; set them via
	// [Config.TTLMs] and [Config.CacheScope].
	TTLMs      int    `json:"ttlMs,omitempty"`
	CacheScope string `json:"cacheScope,omitempty"`
}
