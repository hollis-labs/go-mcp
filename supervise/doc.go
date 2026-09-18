// Package supervise holds pure, dependency-free primitives for a product's
// own child-process supervision loop: a bounded backoff schedule, an exit
// classifier, and a redacted stderr tail. It is extracted from Tether's
// internal/mcpadapter (client_pool.go's recoveryPolicy/supervise() and
// upstream_stdio.go's UpstreamExit/watchExit()/stderrTail), the portfolio's
// most complete existing implementation of proactive subprocess supervision
// for an MCP stdio upstream.
//
// This package deliberately does not supervise anything itself. It never
// spawns a process, calls os.Exit, sends a signal, waits on a process, or
// restarts one -- every primitive here is a value in, value out function or a
// small stateless-except-its-own-buffer type. The caller owns the process
// (exec.Cmd, Wait, the actual respawn), reads state from this package to
// decide what to do, and acts on that decision itself. This mirrors the
// ownership boundary the staleness package already established (see
// staleness/observe.go and ADR adr_mcp_staleness_detection_ownership,
// CW-20260912-0106): the shared library covers only the common
// comparison/classification logic; the product owns recovery/lifecycle
// decisions.
//
// Nothing here is MCP-specific -- Policy operates on time.Duration, Exit
// classifies an os.ProcessState, and Tail redacts a []byte stream -- so any
// product supervising a child process (not just an MCP stdio upstream) can
// use it. Like budget and staleness, this package uses only the Go standard
// library.
package supervise
