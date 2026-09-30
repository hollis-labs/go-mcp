package server

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// MaxInstructionsLen is the ceiling, in runes, on the server instructions
// string.
const MaxInstructionsLen = 2048

// DefaultMaxNameLength is LintCatalog's default tool-name length cap. It is
// an inferred limit (common MCP clients cap names near it), not a ruled one;
// WithMaxNameLength overrides it.
const DefaultMaxNameLength = 64

var defaultNameCharset = regexp.MustCompile(`^[a-z0-9_]+$`)

// ValidateInstructions reports an error when instructions is longer than
// MaxInstructionsLen runes. It is the non-panicking counterpart of the check
// WithInstructions applies.
func ValidateInstructions(instructions string) error {
	if n := utf8.RuneCountInString(instructions); n > MaxInstructionsLen {
		return fmt.Errorf("server: instructions are %d runes, over the %d limit", n, MaxInstructionsLen)
	}
	return nil
}

type lintConfig struct {
	charset      *regexp.Regexp
	maxName      int
	instructions *string
	requireCheck bool
	expected     map[string]bool // nil: not checking; non-nil (possibly empty): the exact set of tool names
}

// LintOption configures LintCatalog.
type LintOption func(*lintConfig)

// WithNameCharset overrides the name pattern (default ^[a-z0-9_]+$).
func WithNameCharset(re *regexp.Regexp) LintOption {
	return func(c *lintConfig) { c.charset = re }
}

// WithMaxNameLength overrides the name length cap (default
// DefaultMaxNameLength). n <= 0 disables the check.
func WithMaxNameLength(n int) LintOption {
	return func(c *lintConfig) { c.maxName = n }
}

// WithLintInstructions also checks a server instructions string with
// ValidateInstructions.
func WithLintInstructions(instructions string) LintOption {
	return func(c *lintConfig) { c.instructions = &instructions }
}

// WithRequireChecked flags every tool registered through plain RegisterTool
// rather than RegisterChecked (ToolDefinition.AnnotationsChecked is false).
// Off by default, since plain RegisterTool is a supported path.
func WithRequireChecked() LintOption {
	return func(c *lintConfig) { c.requireCheck = true }
}

// WithExpectedNames pins the exact set of tool names the catalog exposes, the
// golden-list contract test many servers write by hand: a tool registered but
// not in names is reported as "unexpected tool", and a name in names that is
// not registered as "missing tool". Both are ordinary Issues, in name order,
// so a drift in either direction fails a test that asserts LintCatalog is
// empty and says which side moved. It replaces any earlier WithExpectedNames.
// With no names it expects an empty catalog. Off by default, since a catalog is
// often supposed to grow.
func WithExpectedNames(names ...string) LintOption {
	return func(c *lintConfig) {
		c.expected = make(map[string]bool, len(names))
		for _, n := range names {
			c.expected[n] = true
		}
	}
}

// LintCatalog checks a catalog and returns its findings, empty when clean:
// name charset and length; a blank title; names that differ only by case
// (which registration cannot see, because it keys on the exact name); tools
// that differ from an expected set (WithExpectedNames); an over-long
// instructions string (WithLintInstructions); and every finding of
// ValidateAnnotations, appended verbatim. It is opt-in and never called by
// NewServer or RegisterTool. Findings come in a fixed order: per tool in name
// order, then collisions, unexpected then missing tools, instructions, and
// annotation contradictions.
func LintCatalog(defs []ToolDefinition, opts ...LintOption) []Issue {
	cfg := lintConfig{charset: defaultNameCharset, maxName: DefaultMaxNameLength}
	for _, o := range opts {
		o(&cfg)
	}
	sorted := append([]ToolDefinition(nil), defs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	var out []Issue
	for _, d := range sorted {
		if cfg.charset != nil && !cfg.charset.MatchString(d.Name) {
			out = append(out, Issue{d.Name, fmt.Sprintf("name does not match %s", cfg.charset)})
		}
		if n := utf8.RuneCountInString(d.Name); cfg.maxName > 0 && n > cfg.maxName {
			out = append(out, Issue{d.Name, fmt.Sprintf("name is %d characters, over the %d limit", n, cfg.maxName)})
		}
		if strings.TrimSpace(d.Title) == "" {
			out = append(out, Issue{d.Name, "title is blank"})
		}
		if cfg.requireCheck && !d.AnnotationsChecked {
			out = append(out, Issue{d.Name, "annotations were not declared through RegisterChecked"})
		}
	}

	groups := make(map[string][]string)
	for _, d := range sorted {
		k := strings.ToLower(d.Name)
		groups[k] = append(groups[k], d.Name)
	}
	keys := make([]string, 0, len(groups))
	for k, g := range groups {
		if len(g) > 1 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := groups[k]
		out = append(out, Issue{g[0], fmt.Sprintf("names differ only by case: %s", strings.Join(g, ", "))})
	}

	if cfg.expected != nil {
		registered := make(map[string]bool, len(sorted))
		for _, d := range sorted {
			registered[d.Name] = true
			if !cfg.expected[d.Name] {
				out = append(out, Issue{d.Name, "unexpected tool: registered but not in the expected names"})
			}
		}
		missing := make([]string, 0, len(cfg.expected))
		for n := range cfg.expected {
			if !registered[n] {
				missing = append(missing, n)
			}
		}
		sort.Strings(missing)
		for _, n := range missing {
			out = append(out, Issue{n, "missing tool: in the expected names but not registered"})
		}
	}

	if cfg.instructions != nil {
		if err := ValidateInstructions(*cfg.instructions); err != nil {
			out = append(out, Issue{Message: err.Error()})
		}
	}
	return append(out, ValidateAnnotations(defs)...)
}
