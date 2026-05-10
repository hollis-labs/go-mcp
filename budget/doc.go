// Package budget provides MCP response budget enforcement utilities.
//
// MCP tool servers commonly need to keep list-style responses within a
// model-context-friendly token budget. This package wraps results in an
// [Envelope] with pagination and truncation metadata, and provides small
// helpers for extracting paging arguments from untyped MCP parameter maps
// and rendering MCP tool-response JSON.
//
// The defaults target ~2000 tokens (~8000 bytes of serialized JSON), a
// reasonable starting point for tool responses consumed inline by an LLM.
// Callers override the limits via [Config].
//
// The package is dependency-free (stdlib only).
package budget
