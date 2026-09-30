// Package clientguard provides call-rate and failure-rate protection for a
// caller invoking calls against an external, fallible service -- in this
// portfolio, most commonly go-mcp/client's Pool.CallTool against an upstream
// MCP server. It has no dependency on client, the MCP SDK, or any other
// go-mcp package: like its siblings budget, staleness and supervise, it is a
// dependency-free primitive that owns no lifecycle. The caller wraps its own
// call with Guard.Do (or the generic Do); clientguard keeps per-key state
// (one CircuitBreaker and one RateLimiter per key, keyed by whatever string
// the caller chooses -- typically the upstream's server name) and decides
// admission before the caller's function runs.
//
// "Guard" here is the call-admission Guard in this package. It is unrelated to
// server/guard.go (StrictArgs, ValidateSchema), which validates tool arguments
// on the server side.
//
// This complements budget rather than overlapping it: budget governs response
// SIZE on a value already returned (truncation, pagination, cache hints);
// clientguard governs whether a CALL is attempted at all (rate limiting) and
// whether repeated failures should pause calls to a key entirely (circuit
// breaking). Timeouts and connection retry already exist in client
// (WithCallTimeout, WithDefaultCallTimeouts, RetryPolicy, IsRecoverableError,
// IsProvablyUnsent); clientguard composes around them and reimplements
// neither. client's retry reconnects immediately with no backoff delay, and
// clientguard does not add one.
//
// The circuit breaker admits exactly one probe while half-open, releases the
// probe slot if the call never reached a verdict, and ignores results from
// calls admitted before the latest state change. See CircuitBreaker and
// Guard.Do.
package clientguard
