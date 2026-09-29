package server

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// AlwaysLoadMetaKey is the per-tool _meta key published for a tool with
// Tool.AlwaysLoad set. The value is always true; the key is absent otherwise.
const AlwaysLoadMetaKey = "tether/alwaysLoad"

// toolsListCursorKind is the budget cursor kind of a tools/list cursor, so a
// cursor minted for anything else is rejected here.
const toolsListCursorKind = "tools/list"

// WithToolOrder installs a three-tier tools/list order, used by
// ToolDefinitions, PaginateCatalog and the wire tools/list: the pinned names
// first, in the given sequence; then registration order; then name. A pinned
// name that is not registered is ignored. It also installs the catalog
// middleware, so the wire tools/list follows the same order (unpaginated
// unless WithToolsListPagination sets a page size).
func WithToolOrder(pinned ...string) Option {
	return func(o *options) {
		o.pinned = append([]string(nil), pinned...)
		o.ordered = true
		o.catalog = true
	}
}

// WithToolsListPagination serves tools/list from the server's own catalog:
// ordered as ToolDefinitions is, pageSize tools per page (pageSize <= 0 means
// one unpaginated page), with an opaque nextCursor bound to CatalogFingerprint
// and the profile id. The SDK's own tools/list is always complete and
// alphabetical, so this works by receiving middleware that replaces its
// result; it is installed innermost, after WithSanitize and every
// WithReceivingMiddleware middleware.
//
// profileOf reads the caller's profile id from the request context; nil means
// one shared, unscoped catalog. A cursor issued under another profile, or
// before the catalog changed, is refused with JSON-RPC invalid params (-32602).
//
// Only tools registered through this Server are listed; tools added directly
// to SDKServer() are not. Each page's ttlMs and cacheScope are folded from its
// tools' Tool.TTLMs / Tool.CacheScope: the smallest ttlMs on the page (so 0 if
// any tool is uncacheable), and "private" if any tool is private.
func WithToolsListPagination(pageSize int, profileOf func(context.Context) string) Option {
	return func(o *options) {
		if pageSize < 0 {
			pageSize = 0
		}
		o.pageSize = pageSize
		o.profileOf = profileOf
		o.catalog = true
	}
}

// orderedLocked returns the definitions in catalog order. s.mu must be held.
func (s *Server) orderedLocked() []ToolDefinition {
	defs := make([]ToolDefinition, 0, len(s.defs))
	for _, d := range s.defs {
		defs = append(defs, d)
	}
	if !s.cfg.ordered {
		sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
		return defs
	}
	pin := make(map[string]int, len(s.cfg.pinned))
	for i, n := range s.cfg.pinned {
		if _, dup := pin[n]; !dup {
			pin[n] = i
		}
	}
	pos := make(map[string]int, len(s.order))
	for i, n := range s.order {
		pos[n] = i
	}
	sort.Slice(defs, func(i, j int) bool {
		a, b := defs[i].Name, defs[j].Name
		pa, aPinned := pin[a]
		pb, bPinned := pin[b]
		switch {
		case aPinned && bPinned:
			return pa < pb
		case aPinned != bPinned:
			return aPinned
		}
		if pos[a] != pos[b] {
			return pos[a] < pos[b]
		}
		return a < b
	})
	return defs
}

// CatalogFingerprint is a digest of every client-visible property of the
// registered tools (name, title, description, schemas, annotations, always-load
// and cache hints), taken in name order, so it does not depend on registration
// order. Adding, removing or changing a tool changes it.
func (s *Server) CatalogFingerprint() string {
	s.mu.RLock()
	defs := make([]ToolDefinition, 0, len(s.defs))
	for _, d := range s.defs {
		defs = append(defs, d)
	}
	s.mu.RUnlock()
	return catalogFingerprint(defs)
}

func catalogFingerprint(defs []ToolDefinition) string {
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	type entry struct {
		ToolDefinition
		AlwaysLoad bool
		TTLMs      int
		CacheScope string
	}
	parts := make([]any, len(defs))
	for i, d := range defs {
		parts[i] = entry{d, d.AlwaysLoad, d.TTLMs, d.CacheScope}
	}
	return budget.Fingerprint(parts...)
}

type catalogCursor struct {
	Offset int `json:"o"`
}

// PaginateCatalog returns one page of the catalog in catalog order. cursor is
// the previous page's nextCursor ("" for the first page); pageSize <= 0
// returns everything. The cursor is bound to profileID and CatalogFingerprint:
// one issued under a different profile, or before the catalog changed,
// yields an error satisfying errors.Is(err, budget.ErrCursorMismatch).
func (s *Server) PaginateCatalog(profileID, cursor string, pageSize int) (page []ToolDefinition, nextCursor string, err error) {
	s.mu.RLock()
	defs := s.orderedLocked()
	s.mu.RUnlock()

	fp := budget.Fingerprint(profileID, catalogFingerprint(append([]ToolDefinition(nil), defs...)))
	start := 0
	if cursor != "" {
		var st catalogCursor
		if derr := budget.DecodeCursor(cursor, toolsListCursorKind, fp, &st); derr != nil {
			return nil, "", derr
		}
		if st.Offset < 0 || st.Offset > len(defs) {
			return nil, "", fmt.Errorf("%w: offset out of range", budget.ErrInvalidCursor)
		}
		start = st.Offset
	}
	end := len(defs)
	if pageSize > 0 && start+pageSize < end {
		end = start + pageSize
	}
	page = defs[start:end]
	if end < len(defs) {
		nextCursor, err = budget.EncodeCursor(toolsListCursorKind, fp, catalogCursor{Offset: end})
		if err != nil {
			return nil, "", err
		}
	}
	return page, nextCursor, nil
}

// applyCacheable folds a page's per-tool hints into the one ttlMs/cacheScope
// the response carries: the minimum ttlMs (0 if any tool is uncacheable) and
// "private" if any tool is private, else "public".
func applyCacheable(page []ToolDefinition, c *mcpsdk.Cacheable) {
	c.TTLMs, c.CacheScope = 0, "public"
	for i, d := range page {
		if i == 0 || d.TTLMs < c.TTLMs {
			c.TTLMs = d.TTLMs
		}
		if d.CacheScope == "private" {
			c.CacheScope = "private"
		}
	}
}

// toolsListMiddleware replaces the SDK's tools/list result with a catalog page.
func (s *Server) toolsListMiddleware() mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			if method != "tools/list" {
				return next(ctx, method, req)
			}
			// The SDK's own handler decodes the cursor with its private
			// scheme and would refuse ours, so hide it while that runs.
			var cursor string
			if p, ok := req.GetParams().(*mcpsdk.ListToolsParams); ok && p != nil {
				cursor, p.Cursor = p.Cursor, ""
				defer func() { p.Cursor = cursor }()
			}
			res, err := next(ctx, method, req)
			if err != nil {
				return res, err
			}
			lr, ok := res.(*mcpsdk.ListToolsResult)
			if !ok {
				return res, err
			}
			var profile string
			if s.cfg.profileOf != nil {
				profile = s.cfg.profileOf(ctx)
			}
			page, next2, perr := s.PaginateCatalog(profile, cursor, s.cfg.pageSize)
			if perr != nil {
				if errors.Is(perr, budget.ErrInvalidCursor) {
					return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "tools/list: " + perr.Error()}
				}
				return nil, perr
			}
			// The tools come from our own record, not lr.Tools: the SDK
			// truncates its list at its own page size.
			tools := make([]*mcpsdk.Tool, 0, len(page))
			s.mu.RLock()
			for _, d := range page {
				if t, ok := s.sdkTools[d.Name]; ok {
					tools = append(tools, t)
				}
			}
			s.mu.RUnlock()
			lr.Tools = tools
			lr.NextCursor = next2
			applyCacheable(page, &lr.Cacheable)
			return lr, nil
		}
	}
}
