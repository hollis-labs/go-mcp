package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/hollis-labs/go-mcp/budget"
)

var (
	_ ToolMiddleware = StrictArgs()
	_ ToolMiddleware = ValidateSchema()
)

// Violation describes what StrictArgs found wrong with one call.
type Violation struct {
	Tool string
	// Unknown are the argument names the tool does not declare, sorted.
	Unknown []string
	// Missing are declared-required names absent from the call, sorted.
	Missing []string
	// Accepted is the tool's full declared argument set, sorted.
	Accepted []string
	// Suggestions maps each Unknown name to its nearest declared names.
	Suggestions map[string][]string
	// Retired maps an Unknown name to the caller-supplied guidance for it.
	Retired map[string]string
}

type strictConfig struct {
	keys      map[string]struct{}
	prefix    string
	strip     bool
	retired   map[string]map[string]string
	code      string
	onViolate func(ctx context.Context, def ToolDefinition, v Violation) (any, error)
}

// StrictOption configures StrictArgs.
type StrictOption func(*strictConfig)

// WithTransportKeys replaces the set of exact argument names exempt from the
// check (default "_traceparent", "_tracestate"). Any other underscore key is
// still refused.
func WithTransportKeys(keys ...string) StrictOption {
	return func(c *strictConfig) {
		c.keys = make(map[string]struct{}, len(keys))
		for _, k := range keys {
			c.keys[k] = struct{}{}
		}
	}
}

// WithTransportPrefix additionally exempts every key starting with prefix.
// It is a blanket exemption, so it is opt-in.
func WithTransportPrefix(prefix string) StrictOption {
	return func(c *strictConfig) { c.prefix = prefix }
}

// WithStripTransportKeys hands the next handler a copy of the arguments
// without the exempt keys. The caller's map is never mutated.
func WithStripTransportKeys() StrictOption {
	return func(c *strictConfig) { c.strip = true }
}

// WithRetiredArgs supplies per-tool guidance for argument names that used to
// exist, keyed tool name then argument name.
func WithRetiredArgs(byTool map[string]map[string]string) StrictOption {
	return func(c *strictConfig) { c.retired = byTool }
}

// WithErrorCode sets the ToolError code of the default violation result
// (default "invalid_argument"). Error vocabularies stay app-owned.
func WithErrorCode(code string) StrictOption {
	return func(c *strictConfig) { c.code = code }
}

// WithViolationHandler replaces the default violation result. Whatever it
// returns becomes the tool call's result, so an app can keep its own wire
// shape.
func WithViolationHandler(h func(ctx context.Context, def ToolDefinition, v Violation) (any, error)) StrictOption {
	return func(c *strictConfig) { c.onViolate = h }
}

// StrictArgs refuses a call that names an argument the tool does not declare
// or omits a required one, instead of letting the handler silently drop it.
// The SDK's raw AddTool validates nothing, so without this a misspelled
// argument produces a successful call with the value missing.
//
// The declared names and required list are derived from def.InputSchema when
// the middleware wraps a tool (once, not per call). Supported schema types:
// map[string]any (as built by ObjectSchema / InputSchema), *jsonschema.Schema,
// and json.RawMessage / []byte. Anything else panics at wrap time: a tool
// whose accepted set is unknown is the silent-drop defect again. Names and
// required-ness only are checked; value types are not (see ValidateSchema).
//
// The default violation result is a *budget.ToolError, so the call reports
// IsError. Call ordering: receiving middleware (WithSanitize) runs first, so
// sanitize can move a leaked fragment into an argument slot before this
// guard looks.
func StrictArgs(opts ...StrictOption) ToolMiddleware {
	cfg := strictConfig{
		keys: map[string]struct{}{"_traceparent": {}, "_tracestate": {}},
		code: "invalid_argument",
	}
	for _, o := range opts {
		o(&cfg)
	}
	return func(def ToolDefinition, next ToolHandler) ToolHandler {
		declared, required, err := introspectSchema(def.InputSchema)
		if err != nil {
			panic(fmt.Sprintf("server: StrictArgs: tool %q: %v", def.Name, err))
		}
		accepted := sortedSet(declared)
		retired := cfg.retired[def.Name]
		exempt := func(k string) bool {
			if _, ok := cfg.keys[k]; ok {
				return true
			}
			return cfg.prefix != "" && strings.HasPrefix(k, cfg.prefix)
		}
		return func(ctx context.Context, args map[string]any) (any, error) {
			var unknown, missing []string
			for k := range args {
				if _, ok := declared[k]; ok || exempt(k) {
					continue
				}
				unknown = append(unknown, k)
			}
			for _, r := range required {
				if _, ok := args[r]; !ok {
					missing = append(missing, r)
				}
			}
			if len(unknown) > 0 || len(missing) > 0 {
				sort.Strings(unknown)
				sort.Strings(missing)
				v := Violation{Tool: def.Name, Unknown: unknown, Missing: missing, Accepted: accepted}
				if len(unknown) > 0 {
					v.Suggestions = make(map[string][]string, len(unknown))
					for _, u := range unknown {
						v.Suggestions[u] = suggestArgNames(u, accepted)
						if g, ok := retired[u]; ok {
							if v.Retired == nil {
								v.Retired = map[string]string{}
							}
							v.Retired[u] = g
						}
					}
				}
				if cfg.onViolate != nil {
					return cfg.onViolate(ctx, def, v)
				}
				return nil, violationError(cfg.code, v)
			}
			if cfg.strip {
				var hasExempt bool
				for k := range args {
					if exempt(k) {
						hasExempt = true
						break
					}
				}
				if hasExempt {
					cp := make(map[string]any, len(args))
					for k, val := range args {
						if !exempt(k) {
							cp[k] = val
						}
					}
					args = cp
				}
			}
			return next(ctx, args)
		}
	}
}

func violationError(code string, v Violation) *budget.ToolError {
	var b strings.Builder
	for _, u := range v.Unknown {
		if g, ok := v.Retired[u]; ok {
			fmt.Fprintf(&b, "`%s` is not an argument of %s. %s ", u, v.Tool, g)
			continue
		}
		fmt.Fprintf(&b, "`%s` is not an argument of %s.", u, v.Tool)
		if s := v.Suggestions[u]; len(s) > 0 {
			fmt.Fprintf(&b, " Did you mean %s?", quotedList(s, "or"))
		}
		b.WriteString(" ")
	}
	if len(v.Missing) > 0 {
		fmt.Fprintf(&b, "Required by %s but missing: %s. ", v.Tool, quotedList(v.Missing, "and"))
	}
	if len(v.Accepted) > 0 {
		fmt.Fprintf(&b, "This tool accepts: %s.", strings.Join(v.Accepted, ", "))
	} else {
		b.WriteString("This tool accepts no arguments.")
	}
	e := budget.NewToolError(code, b.String()).
		WithNextStep("Retry the call using only the accepted argument names" +
			", and include every required one.")
	switch {
	case len(v.Unknown) == 1 && len(v.Missing) == 0:
		e.Field = v.Unknown[0]
	case len(v.Unknown) == 0 && len(v.Missing) == 1:
		e.Field = v.Missing[0]
	}
	return e
}

func quotedList(names []string, conj string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + n + "`"
	}
	if len(q) == 1 {
		return q[0]
	}
	return strings.Join(q[:len(q)-1], ", ") + " " + conj + " " + q[len(q)-1]
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// introspectSchema reads the declared property names and required list out of
// the supported InputSchema representations.
func introspectSchema(schema any) (declared map[string]struct{}, required []string, err error) {
	switch s := schema.(type) {
	case map[string]any:
		props, ok := s["properties"].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("InputSchema has no map[string]any \"properties\"")
		}
		declared = make(map[string]struct{}, len(props))
		for k := range props {
			declared[k] = struct{}{}
		}
		switch r := s["required"].(type) {
		case nil:
		case []string:
			required = append(required, r...)
		case []any:
			for _, x := range r {
				name, ok := x.(string)
				if !ok {
					return nil, nil, fmt.Errorf("InputSchema \"required\" holds a %T", x)
				}
				required = append(required, name)
			}
		default:
			return nil, nil, fmt.Errorf("InputSchema \"required\" is a %T", r)
		}
		return declared, required, nil
	case *jsonschema.Schema:
		if s == nil {
			return nil, nil, fmt.Errorf("InputSchema is a nil *jsonschema.Schema")
		}
		declared = make(map[string]struct{}, len(s.Properties))
		for k := range s.Properties {
			declared[k] = struct{}{}
		}
		return declared, append([]string(nil), s.Required...), nil
	case json.RawMessage:
		return introspectRaw(s)
	case []byte:
		return introspectRaw(s)
	default:
		return nil, nil, fmt.Errorf("InputSchema is a %T; want map[string]any, *jsonschema.Schema or json.RawMessage", schema)
	}
}

func introspectRaw(raw []byte) (map[string]struct{}, []string, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil, fmt.Errorf("InputSchema is not a JSON object: %w", err)
	}
	return introspectSchema(m)
}

// ValidateSchema validates each call's arguments against the tool's whole
// InputSchema with jsonschema-go: the schema is resolved once when the
// middleware wraps a tool, then each call gets defaults applied and is
// validated. A failure is a *budget.ToolError with code "invalid_argument".
//
// It is opt-in and stricter than StrictArgs: it also checks value types, so
// it rejects a numeric string for an integer property, which some clients
// send and some servers deliberately tolerate. It accepts the same schema
// representations as StrictArgs and panics at wrap time on an unusable or
// unresolvable schema. Defaults are applied to a copy the handler receives.
func ValidateSchema() ToolMiddleware {
	return func(def ToolDefinition, next ToolHandler) ToolHandler {
		resolved, err := resolveSchema(def.InputSchema)
		if err != nil {
			panic(fmt.Sprintf("server: ValidateSchema: tool %q: %v", def.Name, err))
		}
		return func(ctx context.Context, args map[string]any) (any, error) {
			cp := make(map[string]any, len(args))
			for k, v := range args {
				cp[k] = v
			}
			if err := resolved.ApplyDefaults(&cp); err != nil {
				return nil, budget.NewToolError("invalid_argument", fmt.Sprintf("validating \"arguments\": %v", err))
			}
			if err := resolved.Validate(cp); err != nil {
				return nil, budget.NewToolError("invalid_argument", fmt.Sprintf("validating \"arguments\": %v", err)).
					WithNextStep("Fix the arguments to match the tool's input schema and retry.")
			}
			return next(ctx, cp)
		}
	}
}

func resolveSchema(schema any) (*jsonschema.Resolved, error) {
	var js *jsonschema.Schema
	switch s := schema.(type) {
	case *jsonschema.Schema:
		if s == nil {
			return nil, fmt.Errorf("InputSchema is a nil *jsonschema.Schema")
		}
		js = s
	case map[string]any:
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		js = new(jsonschema.Schema)
		if err := json.Unmarshal(raw, js); err != nil {
			return nil, err
		}
	case json.RawMessage:
		js = new(jsonschema.Schema)
		if err := json.Unmarshal(s, js); err != nil {
			return nil, err
		}
	case []byte:
		js = new(jsonschema.Schema)
		if err := json.Unmarshal(s, js); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("InputSchema is a %T; want map[string]any, *jsonschema.Schema or json.RawMessage", schema)
	}
	return js.Resolve(nil)
}

// maxArgSuggestions caps how many near-miss names a refusal offers.
const maxArgSuggestions = 3

// suggestArgNames ranks declared names by how likely each is to be what the
// caller meant: a case-only difference first, then containment (short name for
// a prefixed argument), then edit distance within a tolerance that scales
// with the name's length. Lifted from Tesseract's strict-args guard.
func suggestArgNames(unknown string, accepted []string) []string {
	type scored struct {
		name string
		rank int
		size int
	}
	lower := strings.ToLower(unknown)
	var matches []scored
	for _, name := range accepted {
		cand := strings.ToLower(name)
		switch {
		case cand == lower:
			matches = append(matches, scored{name, 0, len(name)})
		case strings.Contains(cand, lower) || strings.Contains(lower, cand):
			matches = append(matches, scored{name, 1, len(name)})
		default:
			if d := editDistance(lower, cand); d <= argTypoTolerance(lower) {
				matches = append(matches, scored{name, 2 + d, len(name)})
			}
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		if matches[i].size != matches[j].size {
			return matches[i].size < matches[j].size
		}
		return matches[i].name < matches[j].name
	})
	out := make([]string, 0, maxArgSuggestions)
	for _, m := range matches {
		if len(out) == maxArgSuggestions {
			break
		}
		out = append(out, m.name)
	}
	return out
}

func argTypoTolerance(name string) int {
	switch {
	case len(name) <= 4:
		return 1
	case len(name) <= 8:
		return 2
	default:
		return 3
	}
}

// editDistance is the Levenshtein distance over bytes (argument names are
// ASCII by construction).
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}
