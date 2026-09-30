// Package supervisedstdio runs one local MCP server as a supervised child
// process: it spawns the command, performs the MCP handshake over the child's
// stdin and stdout, and then keeps watching the process in the background,
// reconnecting proactively when it exits, on a bounded backoff schedule.
//
// It is the one-connection primitive behind Tether's per-upstream supervisor
// (internal/mcpadapter's client_pool.go and upstream_stdio.go), generalized so
// any product can use it, and it composes the primitives in package
// supervise: Policy for the schedule, ClassifyExit for how the process ended,
// Tail for the redacted stderr. Tether's own multi-server catalog, tool
// registry and launch observability stay in Tether; a product with N upstreams
// runs N Connections.
//
// # Proactive, unlike package client
//
// Package client's stdio transport is deliberately reactive: it dials on first
// use, retries once and never restarts anything on its own. That is the right
// model for a pool of tools used on demand and it is not changed. A Connection
// is the other model, for a child that should be there: it reconnects without
// being asked, and Status says what state it is in.
//
// # The rules it keeps
//
//   - Transport lost is not process exited. When the child's stdout closes
//     while the process is still running, the session is closed and Session
//     returns nil, but no replacement is started until Wait confirms the old
//     process has exited. The same holds after a failed handshake. A child that
//     ignores the closed stdin is waited for, not replaced.
//   - No process signals, ever. Closing a Connection closes the child's stdin
//     and waits; a child that ignores it is left running, and Close returns
//     ErrStillRunning after Config.ShutdownTimeout rather than killing it.
//   - Every exit is treated alike: clean, nonzero and signal all consume the
//     same restart budget (a clean exit is not read as a request to stop).
//     Startup and handshake failures consume it too.
//   - A successful handshake does not reset the budget. Only a connection that
//     stays up for Policy.StableFor does, so a process that flaps still runs
//     out.
//   - An in-flight call is never replayed. When the transport closes, the call
//     fails, and its outcome may be unknown: the side effect can have happened
//     before the response was lost. Calls made through a session obtained
//     before a reconnect fail; take a new Session afterwards.
//
// The package imports the official MCP SDK and package supervise, and nothing
// else from go-mcp.
package supervisedstdio
