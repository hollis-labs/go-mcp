// Package budget provides MCP response budget enforcement utilities.
//
// MCP tool servers commonly need to keep list-style responses within a
// model-context-friendly token budget. This package wraps results in an
// [Envelope] with pagination and truncation metadata, and provides small
// helpers for extracting paging arguments from untyped MCP parameter maps
// and rendering MCP tool-response JSON.
//
// Paging is opt-in and additive. [ApplyPage] pages an in-memory slice and
// [Seal] finishes a page a store has already windowed; both enforce the
// caller's Config.MaxBytes / Config.MaxTokens (one marshaled Envelope) and
// mint opaque, fingerprint-bound cursors ([EncodeCursor], [DecodeCursor],
// [EncodeOffset], [EncodeKeyset]) that reflect what actually shipped.
// [FitPrefix], [ArrayBytes] and [BytesCap] are the underlying fit primitives
// for callers that keep their own envelope.
//
// [DefaultMaxTokens] (~2000) and [DefaultMaxBytes] (~8000 bytes of serialized
// JSON) are suggested starting points for tool responses consumed inline by
// an LLM; they are never applied implicitly. A byte or token cap binds only
// when the caller sets it in [Config].
//
// The package is dependency-free (stdlib only).
package budget
