// Package budget provides MCP response budget enforcement utilities.
//
// All Fragments Engine MCP servers (Engine, Hadron, Cortex) use this package
// to ensure list responses stay within a ~2000-token budget (~8000 bytes of
// serialized JSON). See ADR-006 for the full contract.
//
// This package is dependency-free (stdlib only) so it can be imported by any
// MCP implementation without pulling in framework dependencies.
package budget
