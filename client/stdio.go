package client

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultTerminateDuration mirrors the official SDK's own default grace
// period between closing a stdio server's stdin and escalating to SIGTERM
// (see cappedPipe.shutdown).
const defaultTerminateDuration = 5 * time.Second

// dialStdio spawns cfg.Command and connects to it over stdin/stdout.
//
// The subprocess is spawned with exec.Command, not exec.CommandContext(ctx,
// ...): ctx here is whatever per-call context the caller happened to pass
// to the call that triggered this lazy dial (see withSession), and its
// lifetime ends when that ONE call returns -- typically via a deferred
// cancel in the caller, per CW-20260918-0046. Tying the subprocess to it
// with exec.CommandContext killed the process the instant the triggering
// call returned, before a second call ever got a chance to reuse the
// connection. Neither shutdown path below needs ctx for the process's
// lifetime: the SDK's own CommandTransport performs its MCP-spec shutdown
// sequence from Close alone, and cappedPipe.teardown (below) does the same.
//
// When no response cap is requested (maxResponseBytes <= 0), this hands the
// subprocess straight to the official SDK's own CommandTransport, which
// performs the MCP-spec stdio shutdown sequence (close stdin, wait, SIGTERM,
// SIGKILL) via its own unexported pipeRWC, and caps a single inbound frame
// at its fixed DefaultMaxLineLength (16 MiB) with no override.
//
// When a tighter cap is requested -- which every caller with tiered trust
// levels, like Nanite, will want -- CommandTransport's fixed cap can't be
// changed, so this instead owns the subprocess's pipes directly and connects
// via IOTransport, which does expose MaxLineLength. That means this
// function, not the SDK, is responsible for the same MCP-spec shutdown
// sequence CommandTransport would otherwise have handled: see cappedPipe.
func dialStdio(ctx context.Context, sdkClient *mcpsdk.Client, name string, cfg ServerConfig, maxResponseBytes int, opts config) (*mcpsdk.ClientSession, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: command is required", name)
	}

	env, err := opts.buildCommandEnv(cfg)
	if err != nil {
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: build env: %w", name, err)
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = env

	if maxResponseBytes <= 0 {
		cs, err := sdkClient.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
		if err != nil {
			return nil, fmt.Errorf("go-mcp/client: stdio server %q: connect: %w", name, err)
		}
		return cs, nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: stdout pipe: %w", name, err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: stdin pipe: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: start: %w", name, err)
	}

	r, w := newCappedPipe(cmd, stdout, stdin)
	t := &mcpsdk.IOTransport{Reader: r, Writer: w, MaxLineLength: maxResponseBytes}
	cs, err := sdkClient.Connect(ctx, t, nil)
	if err != nil {
		_ = r.Close() // tears the process down via the shared teardown below
		return nil, fmt.Errorf("go-mcp/client: stdio server %q: connect: %w", name, err)
	}
	return cs, nil
}

// cappedPipe reads a subprocess's stdout and writes its stdin, and performs
// the MCP-spec stdio shutdown sequence on Close: close stdin, wait up to
// defaultTerminateDuration, SIGTERM, wait again, SIGKILL. This is the same
// sequence the official SDK's CommandTransport performs via its own
// unexported pipeRWC -- reimplemented here because IOTransport (needed for a
// MaxLineLength tighter than the SDK's fixed 16 MiB default) takes a plain
// io.ReadCloser/io.WriteCloser pair with no such behavior of its own.
//
// mcpsdk.IOTransport's underlying connection closes BOTH the Reader and the
// Writer on shutdown (and every error branch of a call closes the
// connection too -- see Client's reconnect handling), so teardown is guarded
// by sync.Once to run exactly once regardless of which side is closed
// first, or both.
type cappedPipe struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stdin  io.WriteCloser

	once sync.Once
	err  error
}

func newCappedPipe(cmd *exec.Cmd, stdout io.ReadCloser, stdin io.WriteCloser) (*cappedPipeReader, *cappedPipeWriter) {
	p := &cappedPipe{cmd: cmd, stdout: stdout, stdin: stdin}
	return &cappedPipeReader{p}, &cappedPipeWriter{p}
}

func (p *cappedPipe) teardown() error {
	p.once.Do(func() { p.err = p.shutdown() })
	return p.err
}

func (p *cappedPipe) shutdown() error {
	// "For the stdio transport, the client SHOULD initiate shutdown by:
	// First, closing the input stream to the child process (the server)..."
	if err := p.stdin.Close(); err != nil {
		return fmt.Errorf("closing stdin: %w", err)
	}

	resChan := make(chan error, 1)
	go func() { resChan <- p.cmd.Wait() }()
	wait := func() (error, bool) {
		select {
		case err := <-resChan:
			return err, true
		case <-time.After(defaultTerminateDuration):
			return nil, false
		}
	}

	// "...Waiting for the server to exit, or sending SIGTERM if the server
	// does not exit within a reasonable time"
	if err, ok := wait(); ok {
		return err
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err == nil {
		if err, ok := wait(); ok {
			return err
		}
	}
	// "...Sending SIGKILL if the server does not exit within a reasonable
	// time after SIGTERM"
	if err := p.cmd.Process.Kill(); err != nil {
		return err
	}
	if err, ok := wait(); ok {
		return err
	}
	return fmt.Errorf("unresponsive subprocess")
}

type cappedPipeReader struct{ p *cappedPipe }

func (r *cappedPipeReader) Read(b []byte) (int, error) { return r.p.stdout.Read(b) }
func (r *cappedPipeReader) Close() error               { return r.p.teardown() }

type cappedPipeWriter struct{ p *cappedPipe }

func (w *cappedPipeWriter) Write(b []byte) (int, error) { return w.p.stdin.Write(b) }
func (w *cappedPipeWriter) Close() error                { return w.p.teardown() }
