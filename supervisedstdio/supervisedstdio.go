package supervisedstdio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hollis-labs/go-mcp/supervise"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Sentinel errors.
var (
	// ErrRestartLimit is wrapped by Status.Err once the restart budget is
	// spent: the connection is in StateFailed and will not try again. Correct
	// the child and start a new Connection.
	ErrRestartLimit = errors.New("supervisedstdio: restart limit reached")
	// ErrStillRunning is returned by Close when the child did not exit within
	// ShutdownTimeout of its stdin closing. It is not killed: this package sends
	// no signals.
	ErrStillRunning = errors.New("supervisedstdio: child still running after stdin closed; no signal was sent")
)

// Config describes the child and how it is supervised.
type Config struct {
	// Command and Args are run directly, with no shell. Command is required.
	Command string
	Args    []string
	// Env is added to the parent's environment, in the child's environment. Its
	// values are also redacted from the stderr tail: treat them as secrets.
	Env map[string]string
	// Dir is the child's working directory; empty means the caller's.
	Dir string

	// Policy is the backoff schedule and stable-uptime window. Its Delays are the
	// restart budget: Delays[n] is the wait before restart n, and len(Delays) is
	// how many restarts are allowed. The zero Policy (nil Delays and zero
	// StableFor) means supervise.DefaultPolicy: five restarts at 1s, 2s, 4s, 8s
	// and 16s, reset after a minute up. An empty but non-nil Delays means no
	// restarts at all.
	Policy supervise.Policy
	// HandshakeTimeout bounds the MCP initialize exchange and OnConnect. Default 10s.
	HandshakeTimeout time.Duration
	// ShutdownTimeout is how long Close waits for the child to exit after its
	// stdin is closed. Default 5s. After that Close returns ErrStillRunning and
	// the child is left running.
	ShutdownTimeout time.Duration

	// StderrSecrets are exact values redacted from the stderr tail, in addition to
	// the values of Env. StderrBytes is the tail's size (default
	// supervise.DefaultTailBytes).
	StderrSecrets []string
	StderrBytes   int

	// ClientInfo identifies this client to the server; default
	// {Name: "go-mcp/supervisedstdio"}.
	ClientInfo *mcpsdk.Implementation
	// ClientOptions are the SDK client options for every connection: handlers
	// (a tools/list_changed handler, say) and capabilities, which the SDK only
	// accepts before Connect. The struct is copied per connection. Optional.
	ClientOptions *mcpsdk.ClientOptions
	// OnConnect runs after each successful handshake, on the same clock as it: a
	// caller lists tools here, for example. An error fails the attempt like a
	// failed handshake: the session is closed and the failure consumes the
	// restart budget. Optional.
	OnConnect func(ctx context.Context, cs *mcpsdk.ClientSession) error
	// OnChange is called with a snapshot after every state change, from the
	// supervisor goroutine and never under a lock. It must return quickly.
	// Optional.
	OnChange func(Status)
}

// redactions is StderrSecrets plus the values of Env.
func (c *Config) redactions() []string {
	out := append([]string(nil), c.StderrSecrets...)
	for _, v := range c.Env {
		out = append(out, v)
	}
	return out
}

// State is where a Connection stands.
type State string

// The states.
const (
	// StateStarting: a child is being spawned and handshaken.
	StateStarting State = "starting"
	// StateConnected: the handshake succeeded and the transport is up.
	StateConnected State = "connected"
	// StateReconnecting: there is no usable session and recovery is pending: the
	// transport was lost, the child is exiting, or the next attempt is waiting
	// out its backoff.
	StateReconnecting State = "reconnecting"
	// StateFailed: the restart budget is spent (Status.Exhausted).
	StateFailed State = "failed"
	// StateClosed: Close was called or the parent context ended.
	StateClosed State = "closed"
)

// Status is a snapshot of a Connection. These are observed states, not active
// probes: a Connected connection has not been pinged.
type Status struct {
	State State
	// Restarts is how many restarts have been spent from the budget; Limit is the
	// budget. Restarts returns to zero only after the connection stays up for
	// Policy.StableFor, not after a handshake.
	Restarts int
	Limit    int
	// NextRetry is when the next attempt starts, while one is scheduled.
	NextRetry time.Time
	// LastExit is how the most recent child ended, once one has.
	LastExit *supervise.Exit
	// Stderr is the redacted tail of the current (or last) child's stderr.
	Stderr string
	// PID is the current (or last) child's process id, 0 before the first start.
	PID int
	// Err is why the connection is not connected: the last failed attempt, the
	// lost transport, or (wrapping ErrRestartLimit) exhaustion. Nil when connected.
	Err error
	// Exhausted is true once the restart budget is spent.
	Exhausted bool
}

// Connection is one supervised stdio MCP server. Create it with Start.
type Connection struct {
	cfg  Config
	deps deps

	ctx    context.Context
	cancel context.CancelFunc
	ready  chan struct{}
	done   chan struct{}

	mu       sync.Mutex
	st       Status
	session  *mcpsdk.ClientSession
	tail     *supervise.Tail
	stillRun atomic.Bool
	closed   sync.Once
}

// Start spawns the child, waits for the first bounded attempt (spawn,
// handshake, OnConnect) to finish, and leaves a supervisor running in the
// background until Close or ctx ends. A failed first attempt is not an error
// here: the supervisor carries on with the backoff schedule and Status says what
// happened, the same as Tether's proxy, where a failed upstream must not stop
// its siblings starting. An error is returned only for an invalid Config, or if
// ctx ends first.
func Start(ctx context.Context, cfg Config) (*Connection, error) {
	return start(ctx, cfg, realDeps())
}

func start(ctx context.Context, cfg Config, d deps) (*Connection, error) {
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, errors.New("supervisedstdio: Config.Command is required")
	}
	if cfg.Policy.Delays == nil && cfg.Policy.StableFor == 0 {
		cfg.Policy = supervise.DefaultPolicy()
	}
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 5 * time.Second
	}
	if cfg.ClientInfo == nil {
		cfg.ClientInfo = &mcpsdk.Implementation{Name: "go-mcp/supervisedstdio"}
	}
	c := &Connection{cfg: cfg, deps: d, ready: make(chan struct{}), done: make(chan struct{})}
	c.ctx, c.cancel = context.WithCancel(ctx) //nolint:gosec // c.cancel is called by Close and when the supervisor stops
	c.st = Status{State: StateStarting, Limit: cfg.Policy.Limit()}
	go c.run()
	if err := c.Ready(ctx); err != nil {
		return c, err
	}
	return c, nil
}

// Ready blocks until the first attempt has finished (successfully or not), or
// ctx ends.
func (c *Connection) Ready(ctx context.Context) error {
	select {
	case <-c.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Done is closed when the supervisor has stopped: after Close or the parent
// context ending, or once the restart budget is spent.
func (c *Connection) Done() <-chan struct{} { return c.done }

// Session returns the current MCP client session, or nil while there is none
// (starting, reconnecting, failed, closed). A session obtained earlier stops
// working when its connection is lost; a call in flight fails and is never
// replayed, so its outcome may be unknown. Take a new Session after a reconnect.
func (c *Connection) Session() *mcpsdk.ClientSession {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.session
}

// Status returns a snapshot.
func (c *Connection) Status() Status {
	c.mu.Lock()
	st := c.st
	tail := c.tail
	c.mu.Unlock()
	if st.LastExit != nil {
		e := *st.LastExit
		st.LastExit = &e
	}
	if tail != nil {
		st.Stderr = tail.String()
	}
	return st
}

// Close stops supervising: it cancels a pending restart, closes the session and
// the child's stdin, and waits up to ShutdownTimeout for the child to exit. It
// sends no signal. A child that has not exited by then is left running and Close
// returns ErrStillRunning. Close is idempotent and safe to call concurrently.
func (c *Connection) Close() error {
	c.closed.Do(c.cancel)
	<-c.done
	if c.stillRun.Load() {
		return ErrStillRunning
	}
	return nil
}

// update mutates the status under the lock, then publishes a snapshot.
func (c *Connection) update(f func(st *Status, c *Connection)) {
	c.mu.Lock()
	f(&c.st, c)
	c.mu.Unlock()
	if c.cfg.OnChange != nil {
		c.cfg.OnChange(c.Status())
	}
}

func (c *Connection) run() {
	defer close(c.done)
	defer c.cancel() // release the context once the supervisor has stopped
	first := true
	signalReady := func() {
		if first {
			first = false
			close(c.ready)
		}
	}
	defer signalReady()
	defer c.finish()

	for c.ctx.Err() == nil {
		c.update(func(st *Status, c *Connection) {
			st.State, st.NextRetry, c.session = StateStarting, time.Time{}, nil
		})
		p, sess, err := c.attempt()

		var stable timer
		var stableC <-chan time.Time
		if err == nil {
			c.update(func(st *Status, c *Connection) { st.State, st.Err, c.session = StateConnected, nil, sess })
			if c.cfg.Policy.StableFor > 0 {
				stable = c.deps.stable(c.cfg.Policy.StableFor)
				stableC = stable.C()
			}
		} else {
			c.update(func(st *Status, c *Connection) {
				st.State, st.Err = StateReconnecting, err
				if p != nil {
					st.Err = fmt.Errorf("%w; waiting for the child to exit", err)
				}
			})
		}
		signalReady()

		if p != nil && !c.watch(p, sess, err == nil, stableC) {
			if stable != nil {
				stable.Stop()
			}
			return
		}
		if stable != nil {
			stable.Stop()
		}
		if c.ctx.Err() != nil {
			return
		}

		// The child has exited (or never started): spend the budget or give up.
		c.mu.Lock()
		delay, ok := c.cfg.Policy.Next(c.st.Restarts)
		c.mu.Unlock()
		if !ok {
			c.update(func(st *Status, c *Connection) {
				st.State, st.Exhausted, c.session, st.NextRetry = StateFailed, true, nil, time.Time{}
				st.Err = fmt.Errorf("%w (%d): %w", ErrRestartLimit, c.cfg.Policy.Limit(), lastErr(st.Err))
			})
			return
		}
		c.update(func(st *Status, c *Connection) {
			st.State, st.NextRetry = StateReconnecting, c.deps.now().Add(delay)
		})
		backoff := c.deps.backoff(delay)
		select {
		case <-c.ctx.Done():
			backoff.Stop()
			return
		case <-backoff.C():
		}
		c.update(func(st *Status, c *Connection) { st.Restarts++ })
	}
}

func lastErr(err error) error {
	if err == nil {
		return errors.New("no error recorded")
	}
	return err
}

// finish records the terminal state when the supervisor returns without having
// exhausted the budget.
func (c *Connection) finish() {
	c.mu.Lock()
	failed := c.st.State == StateFailed
	c.mu.Unlock()
	if failed {
		return
	}
	c.update(func(st *Status, c *Connection) { st.State, st.NextRetry, c.session = StateClosed, time.Time{}, nil })
}

// attempt spawns the child, handshakes and runs OnConnect. It returns the
// process whenever one was started, even on failure: the caller must then wait
// for it to exit before doing anything else. It returns a nil process only when
// the spawn itself failed.
func (c *Connection) attempt() (*process, *mcpsdk.ClientSession, error) {
	p, transport, err := spawn(&c.cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("start %q: %w", c.cfg.Command, err)
	}
	var sessRef atomic.Pointer[mcpsdk.ClientSession]
	p.reap(func() {
		if s := sessRef.Load(); s != nil {
			_ = s.Close()
		}
	})
	c.mu.Lock()
	c.tail = p.stderr
	c.st.PID = p.pid
	c.mu.Unlock()

	hctx, cancel := context.WithTimeout(c.ctx, c.cfg.HandshakeTimeout)
	defer cancel()

	var opts mcpsdk.ClientOptions
	if c.cfg.ClientOptions != nil {
		opts = *c.cfg.ClientOptions
	}
	sess, err := mcpsdk.NewClient(c.cfg.ClientInfo, &opts).Connect(hctx, transport, nil)
	if err != nil {
		// No session was ever established. Close the pipes (stdin is the only
		// shutdown request) and let reap observe the exit.
		p.closeStdin()
		_ = p.stdout.Close()
		return p, nil, fmt.Errorf("connect mcp stdio server %q: %w", c.cfg.Command, err)
	}
	sessRef.Store(sess)
	p.sess = sess
	if c.cfg.OnConnect != nil {
		if err := c.cfg.OnConnect(hctx, sess); err != nil {
			_ = sess.Close()
			p.closeStdin()
			return p, nil, fmt.Errorf("on connect: %w", err)
		}
	}
	return p, sess, nil
}

// watch supervises a live or abandoned child until it is safe to go on. It
// returns true once Wait confirmed the child exited, and false if the parent
// context ended first (the graceful shutdown has then already run).
//
// A lost transport alone is never enough to start another child: it closes the
// session and keeps waiting for the process.
func (c *Connection) watch(p *process, sess *mcpsdk.ClientSession, connected bool, stableC <-chan time.Time) bool {
	var lost <-chan struct{}
	if connected {
		lost = p.lost
	}
	for {
		select {
		case <-c.ctx.Done():
			c.shutdown(p, sess)
			return false
		case <-stableC:
			c.update(func(st *Status, _ *Connection) { st.Restarts = 0 })
			stableC = nil
		case <-lost:
			lost, stableC = nil, nil
			c.update(func(st *Status, c *Connection) {
				st.State, c.session = StateReconnecting, nil
				st.Err = errors.New("stdio closed; waiting for the child to exit")
			})
			if sess != nil {
				_ = sess.Close()
			}
		case <-p.done:
			exit := p.exit
			c.update(func(st *Status, c *Connection) {
				st.LastExit, c.session = &exit, nil
				st.Err = fmt.Errorf("child exited: %s (code %d, signal %q)", exit.Kind, exit.Code, exit.Signal)
				st.State = StateReconnecting
			})
			return true
		}
	}
}

// shutdown is the whole graceful stop: close the session and the child's stdin,
// then wait for the child to exit, at most ShutdownTimeout. No signal is sent.
func (c *Connection) shutdown(p *process, sess *mcpsdk.ClientSession) {
	p.closeStdin()
	if sess != nil {
		_ = sess.Close()
	}
	t := c.deps.shutdown(c.cfg.ShutdownTimeout)
	defer t.Stop()
	select {
	case <-p.done:
		exit := p.exit
		c.update(func(st *Status, _ *Connection) { st.LastExit = &exit })
	case <-t.C():
		c.stillRun.Store(true)
	}
}
