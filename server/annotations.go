package server

import "fmt"

type behaviorKind uint8

const (
	kindInvalid behaviorKind = iota
	kindReads
	kindWrites
	kindDestroys
)

// Behavior is a deliberate statement of what a tool does to the world. The
// zero value is invalid on purpose: a Behavior can only come from Reads,
// Writes or Destroys, so "I never thought about it" cannot look like "this
// tool is safe". Hints are never inferred from a tool's name.
//
// The claims are asymmetric. Reads and Destroys are claims about safety that
// a reviewer must be able to check, so they require a stated reason; Writes
// is the unremarkable middle (non-destructive, not read-only).
type Behavior struct {
	kind       behaviorKind
	why        string
	openWorld  bool
	idempotent bool
}

// Reads declares a tool that only reads. why states what it reads and why
// that is safe; it must be non-empty.
func Reads(why string) Behavior { return Behavior{kind: kindReads, why: why} }

// Writes declares a tool that modifies state without destroying it.
func Writes() Behavior { return Behavior{kind: kindWrites} }

// Destroys declares a tool that may destroy or irreversibly overwrite state.
// why states what is lost; it must be non-empty.
func Destroys(why string) Behavior { return Behavior{kind: kindDestroys, why: why} }

// OpenWorld marks the tool as interacting with an open-ended set of external
// entities.
func (b Behavior) OpenWorld() Behavior { b.openWorld = true; return b }

// Idempotent marks repeated identical calls as having no additional effect.
// Leaving it off means "not asserted", not "asserted false".
func (b Behavior) Idempotent() Behavior { b.idempotent = true; return b }

// Valid reports whether b was built by Reads, Writes or Destroys.
func (b Behavior) Valid() bool { return b.kind != kindInvalid }

// Annotations returns the four MCP hints b stands for. An invalid Behavior
// yields the zero ToolAnnotations.
func (b Behavior) Annotations() ToolAnnotations {
	return ToolAnnotations{
		ReadOnlyHint:    b.kind == kindReads,
		DestructiveHint: b.kind == kindDestroys,
		IdempotentHint:  b.idempotent,
		OpenWorldHint:   b.openWorld,
	}
}

// RegisterChecked registers t with the hints of b. It panics when b is
// invalid, when a Reads/Destroys behavior has no reason, or when t already
// sets a hint field itself (two sources of truth for one claim).
func (s *Server) RegisterChecked(t Tool, b Behavior) {
	if !b.Valid() {
		panic(fmt.Sprintf("server: tool %q: invalid Behavior; build one with Reads, Writes or Destroys", t.Name))
	}
	if (b.kind == kindReads || b.kind == kindDestroys) && b.why == "" {
		panic(fmt.Sprintf("server: tool %q: Reads/Destroys need a non-empty reason", t.Name))
	}
	if t.ReadOnlyHint || t.DestructiveHint || t.IdempotentHint || t.OpenWorldHint {
		panic(fmt.Sprintf("server: tool %q: sets hint fields and a Behavior; use only one", t.Name))
	}
	a := b.Annotations()
	t.ReadOnlyHint, t.DestructiveHint = a.ReadOnlyHint, a.DestructiveHint
	t.IdempotentHint, t.OpenWorldHint = a.IdempotentHint, a.OpenWorldHint
	s.registerTool(t)
}

// AnnotationTable maps tool names to deliberately chosen annotations.
// Presence in the table is the deliberateness: an all-false entry is a valid
// "writes, non-destructive" statement, not a missing one.
type AnnotationTable map[string]ToolAnnotations

// UnknownPolicy says what AnnotationTable.Apply does for a tool that has no
// entry.
type UnknownPolicy uint8

const (
	// UnknownPanic panics (the default): a tool without an entry is a bug.
	UnknownPanic UnknownPolicy = iota
	// UnknownCautious applies CautiousAnnotations.
	UnknownCautious
	// UnknownError returns an error and leaves the tool untouched.
	UnknownError
)

// Apply sets tool's four hint fields from its table entry.
func (t AnnotationTable) Apply(tool *Tool, p UnknownPolicy) error {
	a, ok := t[tool.Name]
	if !ok {
		switch p {
		case UnknownCautious:
			a = CautiousAnnotations()
		case UnknownError:
			return fmt.Errorf("server: tool %q has no entry in the annotation table", tool.Name)
		default:
			panic(fmt.Sprintf("server: tool %q has no entry in the annotation table", tool.Name))
		}
	}
	tool.ReadOnlyHint, tool.DestructiveHint = a.ReadOnlyHint, a.DestructiveHint
	tool.IdempotentHint, tool.OpenWorldHint = a.IdempotentHint, a.OpenWorldHint
	return nil
}

// CautiousAnnotations is the safe direction for a tool nobody has assessed:
// assume it can destroy and can reach outside. Use it for relayed tools whose
// hints are unknown.
func CautiousAnnotations() ToolAnnotations {
	return ToolAnnotations{DestructiveHint: true, OpenWorldHint: true}
}

// Issue is one finding from ValidateAnnotations.
type Issue struct {
	Tool    string
	Message string
}

// ValidateAnnotations reports contradictory annotations. The only rule is
// readOnly together with destructive. All-zero annotations are never flagged:
// they are a legitimate Writes().
func ValidateAnnotations(defs []ToolDefinition) []Issue {
	var out []Issue
	for _, d := range defs {
		if d.Annotations.ReadOnlyHint && d.Annotations.DestructiveHint {
			out = append(out, Issue{Tool: d.Name, Message: "readOnlyHint and destructiveHint are both set"})
		}
	}
	return out
}
