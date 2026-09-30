package skills

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/hollis-labs/go-mcp/budget"
	"github.com/hollis-labs/go-mcp/server"
)

// DefaultArgName is the argument Register reads the skill name from.
const DefaultArgName = "name"

type config struct {
	argName string
	title   string
}

// Option configures Register.
type Option func(*config)

// WithArgName renames the tool's one argument (default "name"), for a server
// whose convention differs -- Station's guide tool calls it "topic".
func WithArgName(argName string) Option {
	return func(c *config) { c.argName = argName }
}

// WithTitle sets the tool's display title (default: the tool's name). Every
// tool needs a non-blank title to pass server.LintCatalog.
func WithTitle(title string) Option {
	return func(c *config) { c.title = title }
}

// Register adds one progressive-discovery tool, toolName, to srv.
//
// Called with no argument (or a blank one) the tool returns the catalog:
//
//	{"items": [{"name": ..., "description": ...}, ...],
//	 "meta":  {"count": N, "progressive_discovery": true, "next": toolName}}
//
// Called with the argument it returns that skill's body as text. An unknown
// name is a *budget.ToolError with code "skill_not_found", the argument as its
// field, a message that lists the available names, and toolName as its help
// tool, so an agent that guessed wrong is sent back to the catalog. A
// non-string argument is "invalid_argument"; a Source failure is
// "internal_error".
//
// The tool is registered read-only and idempotent through RegisterChecked. It
// takes no capability or profile check: it serves documentation the server has
// chosen to publish.
//
// Register verifies src once, now: List must succeed and every name in it must
// be non-empty, unique and returned by Get. A Source whose content changes
// later is not re-verified. Register also refuses a name that is already
// registered, whatever the server's duplicate policy.
func Register(srv *server.Server, toolName, description string, src Source, opts ...Option) error {
	cfg := config{argName: DefaultArgName}
	for _, o := range opts {
		o(&cfg)
	}
	switch {
	case srv == nil:
		return errors.New("skills: Register needs a server")
	case strings.TrimSpace(toolName) == "":
		return errors.New("skills: Register needs a tool name")
	case strings.TrimSpace(description) == "":
		return fmt.Errorf("skills: tool %q needs a description", toolName)
	case src == nil:
		return fmt.Errorf("skills: tool %q needs a Source", toolName)
	case strings.TrimSpace(cfg.argName) == "":
		return fmt.Errorf("skills: tool %q needs a non-blank argument name", toolName)
	}
	if cfg.title == "" {
		cfg.title = toolName
	}
	if err := verify(src); err != nil {
		return fmt.Errorf("skills: tool %q: %w", toolName, err)
	}
	for _, d := range srv.ToolDefinitions() {
		if d.Name == toolName {
			return fmt.Errorf("skills: tool %q is already registered", toolName)
		}
	}

	h := &handler{toolName: toolName, argName: cfg.argName, src: src}
	srv.RegisterChecked(server.Tool{
		Name:        toolName,
		Title:       cfg.title,
		Description: description,
		InputSchema: server.InputSchema(server.StringProp(cfg.argName,
			"Skill to read in full. Omit to list the available skills.", false)),
		Handler: h.call,
	}, server.Reads("returns documentation the server publishes; changes nothing").Idempotent())
	return nil
}

// verify checks that a Source's catalog and content agree.
func verify(src Source) error {
	metas, err := src.List()
	if err != nil {
		return fmt.Errorf("list skills: %w", err)
	}
	seen := make(map[string]bool, len(metas))
	for _, m := range metas {
		switch {
		case strings.TrimSpace(m.Name) == "":
			return errors.New("a listed skill has a blank name")
		case seen[m.Name]:
			return fmt.Errorf("skill %q is listed twice", m.Name)
		}
		seen[m.Name] = true
		if _, err := src.Get(m.Name); err != nil {
			return fmt.Errorf("skill %q is listed but cannot be read: %w", m.Name, err)
		}
	}
	return nil
}

type handler struct {
	toolName string
	argName  string
	src      Source
}

func (h *handler) call(_ context.Context, args map[string]any) (any, error) {
	var name string
	if v, ok := args[h.argName]; ok && v != nil {
		s, isString := v.(string)
		if !isString {
			return nil, budget.NewToolError("invalid_argument", fmt.Sprintf("%s must be a string", h.argName)).
				WithField(h.argName)
		}
		name = strings.TrimSpace(s)
	}
	if name == "" {
		return h.catalog()
	}
	body, err := h.src.Get(name)
	if err == nil {
		return body, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, budget.NewToolError("internal_error", err.Error())
	}
	return nil, h.notFound(name)
}

func (h *handler) catalog() (any, error) {
	metas, err := h.src.List()
	if err != nil {
		return nil, budget.NewToolError("internal_error", err.Error())
	}
	return map[string]any{
		"items": metas,
		"meta": map[string]any{
			"count":                 len(metas),
			"progressive_discovery": true,
			"next":                  h.toolName,
		},
	}, nil
}

func (h *handler) notFound(name string) *budget.ToolError {
	msg := fmt.Sprintf("%s: %q", ErrNotFound, name)
	if metas, err := h.src.List(); err == nil && len(metas) > 0 {
		names := make([]string, len(metas))
		for i, m := range metas {
			names[i] = m.Name
		}
		msg += ". Available: [" + strings.Join(names, ", ") + "]"
	}
	return budget.NewToolError("skill_not_found", msg).
		WithField(h.argName).
		WithHelpTool(h.toolName).
		WithNextStep(fmt.Sprintf("call %s with no arguments to list available skill names", h.toolName))
}
