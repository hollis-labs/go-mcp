package client

import (
	"context"
	"errors"
	"fmt"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pool holds a named set of external MCP server connections, dialing each
// lazily on first use and reusing the connection thereafter. It is the
// primary entry point for most callers; Register/Deregister are mutable
// (unlike Hadron's static WithExternalServers(map) constructor) because a
// caller like Nanite adds and removes servers at runtime, not only at
// startup.
type Pool struct {
	opts config

	mu      sync.Mutex
	entries map[string]*Client
}

// NewPool creates an empty Pool. Register servers with Register.
func NewPool(opts ...Option) *Pool {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}
	return &Pool{opts: cfg, entries: make(map[string]*Client)}
}

// Register adds a server under name, or replaces a previous registration
// with the same name (closing its old connection first, if one was open).
func (p *Pool) Register(name string, cfg ServerConfig) error {
	if name == "" {
		return fmt.Errorf("go-mcp/client: Register: name is required")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.entries[name]; ok {
		_ = existing.Close()
	}
	p.entries[name] = newClient(name, cfg, p.opts, dialSDK)
	return nil
}

// Deregister removes a server, closing its connection if one is open. It is
// not an error to deregister a name that was never registered.
func (p *Pool) Deregister(name string) error {
	p.mu.Lock()
	c, ok := p.entries[name]
	delete(p.entries, name)
	p.mu.Unlock()
	if !ok {
		return nil
	}
	return c.Close()
}

// Get returns the named server's Client, for a caller that wants to call
// CallTool/ListTools/Ping/SetMaxResponseBytes directly rather than through
// the Pool's own convenience methods.
func (p *Pool) Get(name string) (*Client, error) {
	p.mu.Lock()
	c, ok := p.entries[name]
	p.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("go-mcp/client: %q is not registered", name)
	}
	return c, nil
}

// CallTool calls a tool on the named server.
func (p *Pool) CallTool(ctx context.Context, name, tool string, args map[string]any) (*mcpsdk.CallToolResult, CallMetadata, error) {
	c, err := p.Get(name)
	if err != nil {
		return nil, CallMetadata{}, err
	}
	return c.CallTool(ctx, tool, args)
}

// ListTools lists the named server's tools.
func (p *Pool) ListTools(ctx context.Context, name string) (*mcpsdk.ListToolsResult, error) {
	c, err := p.Get(name)
	if err != nil {
		return nil, err
	}
	return c.ListTools(ctx)
}

// Invalidate closes the named server's connection, if one is open, without
// deregistering it -- the next call re-dials. Used both internally (a
// recoverable call/probe error) and by a caller driving its own external
// remediation (Nanite's RestartStdioTransports-style hook).
func (p *Pool) Invalidate(name string) {
	p.mu.Lock()
	c, ok := p.entries[name]
	p.mu.Unlock()
	if ok {
		_ = c.Close()
	}
}

// Close closes every open connection, deregistering all servers. It collects
// every error rather than stopping at the first one.
func (p *Pool) Close() error {
	p.mu.Lock()
	entries := p.entries
	p.entries = make(map[string]*Client)
	p.mu.Unlock()

	var errs []error
	for _, c := range entries {
		if err := c.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
