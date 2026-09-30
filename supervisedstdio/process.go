package supervisedstdio

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/hollis-labs/go-mcp/supervise"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// process is one spawned child. It owns Wait; the MCP transport owns only the
// protocol pipes. A replacement is never started until done is closed, that is
// until Wait has confirmed this process exited.
type process struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *os.File
	stderr *supervise.Tail
	pid    int

	// lost is closed when the protocol stream ended (stdout EOF or closed):
	// the transport is gone, the process may not be.
	lost chan struct{}
	// done is closed after Wait returned; exit is valid once it is.
	done chan struct{}
	exit supervise.Exit

	// sess is the session over this child, once there is one.
	sess *mcpsdk.ClientSession

	reapOnce  sync.Once
	closeOnce sync.Once
}

// eofReader reports a closed stdout as EOF and signals lost. It must be an
// io.ReadCloser (IOTransport requires one): Close is promoted from the embedded
// file.
type eofReader struct {
	io.ReadCloser
	once sync.Once
	lost chan struct{}
}

func (r *eofReader) Read(b []byte) (int, error) {
	n, err := r.ReadCloser.Read(b)
	if errors.Is(err, os.ErrClosed) {
		err = io.EOF
	}
	if err != nil {
		r.once.Do(func() { close(r.lost) })
	}
	return n, err
}

// spawn starts the child. It does not use exec.CommandContext: nothing here
// ever signals a process.
func spawn(cfg *Config) (*process, *mcpsdk.IOTransport, error) {
	// #nosec G204 -- running the configured MCP server command is the purpose of this package; no shell is involved.
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Dir = cfg.Dir
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	tail := &supervise.Tail{Bytes: cfg.StderrBytes, Secrets: cfg.redactions()}
	cmd.Stderr = tail
	// Bound how long Wait waits for inherited stderr after the child itself has exited.
	cmd.WaitDelay = time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	// Our own pipe (not cmd.StdoutPipe) so stdout can be closed independently
	// of Wait, and a read of it can be observed.
	stdout, writer, err := os.Pipe()
	if err != nil {
		_ = stdin.Close()
		return nil, nil, err
	}
	cmd.Stdout = writer
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = writer.Close()
		return nil, nil, err
	}
	_ = writer.Close() // the child holds the write end now

	p := &process{
		cmd: cmd, stdin: stdin, stdout: stdout, stderr: tail, pid: cmd.Process.Pid,
		lost: make(chan struct{}), done: make(chan struct{}),
	}
	transport := &mcpsdk.IOTransport{Reader: &eofReader{ReadCloser: stdout, lost: p.lost}, Writer: stdin}
	return p, transport, nil
}

// reap starts the goroutine that owns Wait. It is called exactly once per
// process, right after a successful spawn, so a child is never left as a
// zombie whatever happens to the session. On exit it classifies the outcome and
// closes the session (so in-flight calls are released even if a descendant still
// holds stdout), then closes done.
func (p *process) reap(closeSession func()) {
	p.reapOnce.Do(func() {
		go func() {
			_ = p.cmd.Wait()
			p.exit = supervise.ClassifyExit(p.cmd.ProcessState, time.Now().UTC())
			closeSession()
			_ = p.stdout.Close()
			close(p.done)
		}()
	})
}

// closeStdin is the only shutdown request this package ever makes of a child.
func (p *process) closeStdin() {
	p.closeOnce.Do(func() { _ = p.stdin.Close() })
}
