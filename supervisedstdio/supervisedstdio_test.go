//go:build unix

package supervisedstdio

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hollis-labs/go-mcp/supervise"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// --- harness ---------------------------------------------------------------

const wait = 30 * time.Second // an upper bound on how long a condition may take, never a delay

type fakeTimer struct {
	d       time.Duration
	c       chan time.Time
	stopped atomic.Bool
}

func (f *fakeTimer) C() <-chan time.Time { return f.c }
func (f *fakeTimer) Stop()               { f.stopped.Store(true) }
func (f *fakeTimer) fire()               { f.c <- time.Time{} }

// fakeClock hands out timers that fire only when a test says so, and records
// every duration asked for, so the tests can assert the exact schedule.
type fakeClock struct {
	mu       sync.Mutex
	backoffs []*fakeTimer
	stables  []*fakeTimer
	shutdown []*fakeTimer
	now      time.Time
}

func (f *fakeClock) mk(list *[]*fakeTimer) func(time.Duration) timer {
	return func(d time.Duration) timer {
		f.mu.Lock()
		defer f.mu.Unlock()
		t := &fakeTimer{d: d, c: make(chan time.Time, 1)}
		*list = append(*list, t)
		return t
	}
}

func (f *fakeClock) deps() deps {
	f.now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return deps{backoff: f.mk(&f.backoffs), stable: f.mk(&f.stables), shutdown: f.mk(&f.shutdown), now: func() time.Time { return f.now }}
}

func (f *fakeClock) get(list *[]*fakeTimer, i int) *fakeTimer {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i < len(*list) {
		return (*list)[i]
	}
	return nil
}

func (f *fakeClock) count(list *[]*fakeTimer) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(*list)
}

func (f *fakeClock) backoff(t *testing.T, i int) *fakeTimer {
	t.Helper()
	var ft *fakeTimer
	eventually(t, func() bool { ft = f.get(&f.backoffs, i); return ft != nil }, "backoff timer %d", i)
	return ft
}

func (f *fakeClock) stable(t *testing.T, i int) *fakeTimer {
	t.Helper()
	var ft *fakeTimer
	eventually(t, func() bool { ft = f.get(&f.stables, i); return ft != nil }, "stable timer %d", i)
	return ft
}

func (f *fakeClock) shutdownTimer(t *testing.T, i int) *fakeTimer {
	t.Helper()
	var ft *fakeTimer
	eventually(t, func() bool { ft = f.get(&f.shutdown, i); return ft != nil }, "shutdown timer %d", i)
	return ft
}

func (f *fakeClock) backoffDurations() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []time.Duration
	for _, b := range f.backoffs {
		out = append(out, b.d)
	}
	return out
}

func eventually(t *testing.T, cond func() bool, format string, args ...any) {
	t.Helper()
	deadline := time.Now().Add(wait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for "+format, args...)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

type env struct {
	t     *testing.T
	dir   string
	cfg   Config
	clock *fakeClock
	conn  *Connection
}

const (
	secret          = "s3cr3t-fixture-token"
	inheritedSecret = "inh3rited-fixture-token"
)

func newEnv(t *testing.T, plan string, delays ...time.Duration) *env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("SUP_INHERITED", inheritedSecret)
	e := &env{t: t, dir: dir, clock: &fakeClock{}}
	e.cfg = Config{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestFixtureProcess$"},
		Env:     map[string]string{"SUP_DIR": dir, "SUP_PLAN": plan, "SUP_SECRET": secret},
		Policy:  supervise.Policy{Delays: delays, StableFor: time.Minute},
		// inherited from the parent, not passed in Env, so only this redacts it
		StderrSecrets: []string{inheritedSecret},
		// The handshake timeout is the one real-time bound the tests use, and only
		// where a child is meant to hang.
		HandshakeTimeout: 20 * time.Second,
	}
	return e
}

func (e *env) start() *Connection {
	e.t.Helper()
	c, err := start(context.Background(), e.cfg, e.clock.deps())
	if err != nil {
		e.t.Fatalf("Start: %v", err)
	}
	e.t.Cleanup(func() {
		// tidy up any child a test left running, without signals
		e.write("release", "1")
		e.c().closed.Do(c.cancel)
		<-c.done
	})
	e.conn = c
	return c
}

func (e *env) c() *Connection { return e.conn }

func (e *env) write(name, content string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.dir, name), []byte(content), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) events() []string {
	b, _ := os.ReadFile(filepath.Join(e.dir, "events")) //nolint:gosec // a test temp dir
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (e *env) count(prefix string) int {
	n := 0
	for _, l := range e.events() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func (e *env) waitEvent(prefix string, n int) {
	e.t.Helper()
	eventually(e.t, func() bool { return e.count(prefix) >= n }, "%d %q events (have %v)", n, prefix, e.events())
}

func (e *env) noSignals() {
	e.t.Helper()
	if n := e.count("signal"); n != 0 {
		e.t.Errorf("the child received a signal (%d): %v", n, e.events())
	}
}

func waitState(t *testing.T, c *Connection, want State) Status {
	t.Helper()
	var st Status
	eventually(t, func() bool { st = c.Status(); return st.State == want }, "state %s (now %+v)", want, c.Status())
	return st
}

func callEcho(t *testing.T, c *Connection) string {
	t.Helper()
	var sess *mcpsdk.ClientSession
	eventually(t, func() bool { sess = c.Session(); return sess != nil }, "a session")
	res, err := sess.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "echo"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	return res.Content[0].(*mcpsdk.TextContent).Text
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// --- tests -----------------------------------------------------------------

func TestConnectsCallsAndClosesGracefully(t *testing.T) {
	e := newEnv(t, "ok", time.Second)
	c := e.start()
	st := waitState(t, c, StateConnected)
	if st.Restarts != 0 || st.Limit != 1 || st.PID == 0 || st.Err != nil {
		t.Fatalf("status = %+v", st)
	}
	if got := callEcho(t, c); got != "echo gen 0" {
		t.Fatalf("echo = %q", got)
	}
	// stderr is captured and redacted, both for a value that was passed in Env and
	// for one that was only configured as a secret (the child inherited it)
	for _, want := range []string{"diagnostic [redacted]", "inherited [redacted]"} {
		if !strings.Contains(c.Status().Stderr, want) {
			t.Errorf("stderr lacks %q: %q", want, c.Status().Stderr)
		}
	}
	if s := c.Status().Stderr; strings.Contains(s, secret) || strings.Contains(s, inheritedSecret) {
		t.Errorf("a secret leaked into stderr: %q", s)
	}

	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	st = c.Status()
	if st.State != StateClosed || st.LastExit == nil || st.LastExit.Kind != supervise.Clean {
		t.Errorf("after Close: %+v", st)
	}
	if c.Session() != nil {
		t.Error("a closed connection has no session")
	}
	e.waitEvent("serve-ended", 1) // it saw stdin EOF and ended by itself
	e.noSignals()
	if err := c.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestCrashRestartsOnTheExactSchedule(t *testing.T) {
	e := newEnv(t, "ok", 10*time.Millisecond, 20*time.Millisecond, 30*time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)
	if callEcho(t, c) != "echo gen 0" {
		t.Fatal("gen 0")
	}

	e.write("exit", "3")
	st := waitState(t, c, StateReconnecting)
	b0 := e.clock.backoff(t, 0)
	if b0.d != 10*time.Millisecond {
		t.Fatalf("first restart waits %v, want the first delay", b0.d)
	}
	st = c.Status()
	if st.LastExit == nil || st.LastExit.Kind != supervise.Error || st.LastExit.Code != 3 {
		t.Fatalf("LastExit = %+v", st.LastExit)
	}
	if want := e.clock.now.Add(10 * time.Millisecond); !st.NextRetry.Equal(want) {
		t.Errorf("NextRetry = %v, want %v", st.NextRetry, want)
	}
	if c.Session() != nil {
		t.Error("no session while reconnecting")
	}
	if n := e.count("start"); n != 1 {
		t.Fatalf("a replacement started before its backoff elapsed: %v", e.events())
	}

	b0.fire()
	waitState(t, c, StateConnected)
	if got := callEcho(t, c); got != "echo gen 1" {
		t.Fatalf("after restart: %q", got)
	}
	if st := c.Status(); st.Restarts != 1 || st.Err != nil || !st.NextRetry.IsZero() {
		t.Fatalf("status after restart = %+v", st)
	}

	// a CLEAN exit is treated the same and takes the next delay: the budget is
	// not reset by the successful handshake
	e.write("exit", "0")
	e.clock.backoff(t, 1)
	if got := e.clock.backoffDurations(); len(got) != 2 || got[1] != 20*time.Millisecond {
		t.Fatalf("schedule = %v, want the second delay next", got)
	}
	if st := c.Status(); st.LastExit == nil || st.LastExit.Kind != supervise.Clean {
		t.Errorf("clean exit classified as %+v", st.LastExit)
	}
	e.noSignals()
}

func TestSignalExitIsClassifiedAndRestarted(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)
	// the child is killed from outside (the test's doing, not the supervisor's)
	if err := syscall.Kill(c.Status().PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	e.clock.backoff(t, 0)
	if x := c.Status().LastExit; x == nil || x.Kind != supervise.Signal || x.Signal == "" {
		t.Fatalf("LastExit = %+v", x)
	}
}

func TestExhaustionAfterStartupFailures(t *testing.T) {
	e := newEnv(t, "startfail", time.Millisecond, 2*time.Millisecond, 3*time.Millisecond)
	c := e.start() // Start returns although the first attempt failed
	if st := c.Status(); st.State == StateConnected || st.Err == nil {
		t.Fatalf("status = %+v", st)
	}
	for i := 0; i < 3; i++ {
		e.clock.backoff(t, i).fire()
	}
	st := waitState(t, c, StateFailed)
	<-c.Done()
	if !st.Exhausted || !errors.Is(st.Err, ErrRestartLimit) || st.Restarts != 3 {
		t.Fatalf("status = %+v", st)
	}
	if got := e.clock.backoffDurations(); len(got) != 3 || got[0] != time.Millisecond || got[1] != 2*time.Millisecond || got[2] != 3*time.Millisecond {
		t.Errorf("schedule = %v", got)
	}
	if n := e.count("start"); n != 4 {
		t.Errorf("%d spawns, want the first plus one per restart (4)", n)
	}
	if st.LastExit == nil || st.LastExit.Code != 23 {
		t.Errorf("LastExit = %+v", st.LastExit)
	}
	if c.Session() != nil {
		t.Error("a failed connection has no session")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close after exhaustion: %v", err)
	}
	if c.Status().State != StateFailed {
		t.Errorf("Close must not hide the failure: %+v", c.Status())
	}
}

// A process that connects and then crashes before the stable window keeps
// consuming the budget: a successful handshake alone resets nothing.
func TestFlappingProcessStillExhausts(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond, 2*time.Millisecond)
	c := e.start()
	for i := 0; i < 2; i++ {
		waitState(t, c, StateConnected)
		e.write("exit", "1")
		e.clock.backoff(t, i).fire()
	}
	waitState(t, c, StateConnected)
	e.write("exit", "1")
	st := waitState(t, c, StateFailed)
	if !errors.Is(st.Err, ErrRestartLimit) || st.Restarts != 2 {
		t.Fatalf("status = %+v", st)
	}
	if n := e.clock.count(&e.clock.stables); n != 3 {
		t.Errorf("%d stable timers, want one per successful connection", n)
	}
	if d := e.clock.stable(t, 0).d; d != time.Minute {
		t.Errorf("stable window = %v", d)
	}
}

func TestStableUptimeResetsTheBudget(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond, 2*time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)

	e.write("exit", "1")
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	if c.Status().Restarts != 1 {
		t.Fatalf("Restarts = %d", c.Status().Restarts)
	}

	// the second connection stays up for the stable window
	e.clock.stable(t, 1).fire()
	eventually(t, func() bool { return c.Status().Restarts == 0 }, "the budget to reset")

	e.write("exit", "1")
	e.clock.backoff(t, 1)
	if got := e.clock.backoffDurations(); got[1] != time.Millisecond {
		t.Fatalf("after a reset the schedule starts over: %v", got)
	}
}

// Transport lost is not process exited. While the child is alive with a closed
// stdout no replacement is started and no backoff is even scheduled; only the
// confirmed exit starts the clock.
func TestTransportLostWaitsForTheProcessToExit(t *testing.T) {
	e := newEnv(t, "ok,ok", time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)
	first := c.Status().PID

	e.write("lose-stdout", "1")
	e.waitEvent("stdout-closed", 1)
	waitState(t, c, StateReconnecting)
	eventually(t, func() bool { return c.Session() == nil }, "the session to be dropped")
	if st := c.Status(); st.Err == nil || !strings.Contains(st.Err.Error(), "stdio closed") || st.LastExit != nil {
		t.Fatalf("status = %+v", st)
	}
	// the process is alive; give the supervisor every chance to (wrongly) act
	time.Sleep(200 * time.Millisecond)
	if !alive(first) {
		t.Fatal("the child died on its own; the test needs it alive")
	}
	if n := e.clock.count(&e.clock.backoffs); n != 0 {
		t.Fatalf("a restart was scheduled while the old process was still running (%d timers)", n)
	}
	if n := e.count("start"); n != 1 {
		t.Fatalf("a replacement was spawned while the old process was still running: %v", e.events())
	}

	e.write("release", "1")
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	if st := c.Status(); st.LastExit == nil || st.LastExit.Kind != supervise.Clean || st.PID == first {
		t.Fatalf("status = %+v", st)
	}
	if callEcho(t, c) != "echo gen 1" {
		t.Fatal("the replacement does not serve")
	}
	e.noSignals()
}

// A failed handshake abandons the child but does not replace it until it has
// exited, even when it ignores the closed stdin. Nothing kills it.
func TestFailedHandshakeWaitsForTheProcessToExit(t *testing.T) {
	e := newEnv(t, "hang-ignore-eof,ok", time.Millisecond)
	e.cfg.HandshakeTimeout = 150 * time.Millisecond
	c := e.start() // returns once the handshake timed out
	st := c.Status()
	if st.State != StateReconnecting || st.Err == nil || !strings.Contains(st.Err.Error(), "waiting for the child to exit") {
		t.Fatalf("status = %+v", st)
	}
	e.waitEvent("stdin-eof", 1) // the abandoned child saw its stdin close and is ignoring it
	time.Sleep(150 * time.Millisecond)
	if !alive(st.PID) || e.count("start") != 1 || e.clock.count(&e.clock.backoffs) != 0 {
		t.Fatalf("a child that ignores EOF must be waited for, not replaced (events %v, %d timers)", e.events(), e.clock.count(&e.clock.backoffs))
	}

	e.write("release", "1")
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	e.noSignals()
}

func TestHandshakeTimeoutIsBounded(t *testing.T) {
	e := newEnv(t, "hang", time.Millisecond)
	e.cfg.HandshakeTimeout = 100 * time.Millisecond
	start := time.Now()
	c := e.start()
	if time.Since(start) > 10*time.Second {
		t.Fatalf("Start took %v", time.Since(start))
	}
	// the hung child got EOF, exited, and is being restarted on schedule
	e.clock.backoff(t, 0)
	if st := c.Status(); st.LastExit == nil || st.LastExit.Kind != supervise.Clean {
		t.Fatalf("status = %+v", st)
	}
}

func TestOnConnectErrorConsumesTheBudget(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond, 2*time.Millisecond)
	var calls atomic.Int32
	e.cfg.OnConnect = func(ctx context.Context, cs *mcpsdk.ClientSession) error {
		if calls.Add(1) == 1 {
			return errors.New("tool listing failed")
		}
		_, err := cs.ListTools(ctx, nil)
		return err
	}
	c := e.start()
	if st := c.Status(); st.State == StateConnected || !strings.Contains(st.Err.Error(), "tool listing failed") {
		t.Fatalf("status = %+v", st)
	}
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	if st := c.Status(); st.Restarts != 1 || calls.Load() != 2 {
		t.Fatalf("status = %+v, OnConnect calls %d", st, calls.Load())
	}
}

// An in-flight call fails when its process dies and is never replayed: the
// child that comes back never sees it.
func TestInFlightCallIsNeverReplayed(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)
	sess := c.Session()

	errc := make(chan error, 1)
	go func() {
		_, err := sess.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "block"})
		errc <- err
	}()
	e.waitEvent("call block", 1)
	e.write("exit", "9")
	if err := <-errc; err == nil {
		t.Fatal("the in-flight call must fail when its process dies")
	}
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	if callEcho(t, c) != "echo gen 1" {
		t.Fatal("the replacement does not serve")
	}
	if n := e.count("call block"); n != 1 {
		t.Fatalf("the call was replayed: %d 'call block' events", n)
	}
	// the stale session stays dead
	if _, err := sess.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: "echo"}); err == nil {
		t.Error("a session from before the reconnect must not silently work")
	}
}

func TestCloseDuringBackoffCancelsTheTimer(t *testing.T) {
	e := newEnv(t, "ok", time.Hour)
	c := e.start()
	waitState(t, c, StateConnected)
	e.write("exit", "1")
	b := e.clock.backoff(t, 0)
	waitState(t, c, StateReconnecting)

	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	if err := <-done; err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !b.stopped.Load() {
		t.Error("Close must cancel the pending restart")
	}
	if st := c.Status(); st.State != StateClosed {
		t.Errorf("status = %+v", st)
	}
	if n := e.count("start"); n != 1 {
		t.Errorf("a restart happened after Close: %v", e.events())
	}
}

// Close closes stdin and waits; a child that ignores it is not killed, Close
// says so, and the child is still running afterwards.
func TestCloseNeverSendsASignal(t *testing.T) {
	e := newEnv(t, "ignore-eof", time.Second)
	c := e.start()
	waitState(t, c, StateConnected)
	pid := c.Status().PID

	errc := make(chan error, 1)
	go func() { errc <- c.Close() }()
	shutdown := e.clock.shutdownTimer(t, 0)
	e.waitEvent("serve-ended", 1) // it saw EOF and is ignoring it
	if !alive(pid) {
		t.Fatal("the child should still be running")
	}
	select {
	case err := <-errc:
		t.Fatalf("Close returned %v before the child exited or the timeout", err)
	case <-time.After(100 * time.Millisecond):
	}
	shutdown.fire()
	if err := <-errc; !errors.Is(err, ErrStillRunning) {
		t.Fatalf("Close = %v, want ErrStillRunning", err)
	}
	if !alive(pid) {
		t.Fatal("Close killed the child")
	}
	e.noSignals()
	if err := c.Close(); !errors.Is(err, ErrStillRunning) {
		t.Errorf("Close is idempotent, including its result: %v", err)
	}
	e.write("release", "1") // the test's cleanup: let the child go
	eventually(t, func() bool { return !alive(pid) }, "the released child to exit")
}

func TestParentContextEndsTheSupervisor(t *testing.T) {
	e := newEnv(t, "ok", time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	c, err := start(ctx, e.cfg, e.clock.deps())
	if err != nil {
		t.Fatal(err)
	}
	e.conn = c
	waitState(t, c, StateConnected)
	cancel()
	<-c.Done()
	if st := c.Status(); st.State != StateClosed || st.LastExit == nil {
		t.Fatalf("status = %+v", st)
	}
	e.waitEvent("serve-ended", 1)
	e.noSignals()
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	if _, err := Start(context.Background(), Config{}); err == nil {
		t.Error("an empty Command must be rejected")
	}
	if _, err := Start(context.Background(), Config{Command: "  "}); err == nil {
		t.Error("a blank Command must be rejected")
	}

	e := newEnv(t, "ok")
	e.cfg.Policy = supervise.Policy{} // the zero value means the default policy
	c := e.start()
	if st := c.Status(); st.Limit != 5 {
		t.Errorf("Limit = %d, want the default policy's 5", st.Limit)
	}
	if c.cfg.HandshakeTimeout <= 0 || c.cfg.ShutdownTimeout != 5*time.Second || c.cfg.ClientInfo == nil {
		t.Errorf("defaults not applied: %+v", c.cfg)
	}
}

// An empty but non-nil Delays is "no restarts", distinct from the zero Policy.
func TestEmptyDelaysMeansNoRestarts(t *testing.T) {
	e := newEnv(t, "startfail")
	e.cfg.Policy = supervise.Policy{Delays: []time.Duration{}}
	c := e.start()
	st := waitState(t, c, StateFailed)
	if st.Limit != 0 || !st.Exhausted || !errors.Is(st.Err, ErrRestartLimit) {
		t.Fatalf("status = %+v", st)
	}
	if e.clock.count(&e.clock.backoffs) != 0 || e.count("start") != 1 {
		t.Errorf("no restart may be attempted (events %v)", e.events())
	}
}

func TestMissingCommandConsumesTheBudget(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond)
	e.cfg.Command = filepath.Join(e.dir, "does-not-exist")
	c := e.start()
	if st := c.Status(); st.Err == nil || !strings.Contains(st.Err.Error(), "start") || st.PID != 0 {
		t.Fatalf("status = %+v", st)
	}
	e.clock.backoff(t, 0).fire()
	st := waitState(t, c, StateFailed)
	if !errors.Is(st.Err, ErrRestartLimit) {
		t.Fatalf("status = %+v", st)
	}
}

func TestOnChangeSeesEveryTransitionInOrder(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond)
	var mu sync.Mutex
	var seen []State
	e.cfg.OnChange = func(s Status) {
		mu.Lock()
		defer mu.Unlock()
		if len(seen) == 0 || seen[len(seen)-1] != s.State {
			seen = append(seen, s.State)
		}
	}
	c := e.start()
	waitState(t, c, StateConnected)
	e.write("exit", "1")
	e.clock.backoff(t, 0).fire()
	waitState(t, c, StateConnected)
	_ = c.Close()
	mu.Lock()
	defer mu.Unlock()
	want := []State{StateStarting, StateConnected, StateReconnecting, StateStarting, StateConnected, StateClosed}
	if len(seen) != len(want) {
		t.Fatalf("states = %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("states = %v, want %v", seen, want)
		}
	}
}

// Status and Session are read from many goroutines while the connection crashes
// and restarts; -race is the assertion.
func TestConcurrentReadsWhileRestarting(t *testing.T) {
	e := newEnv(t, "ok", time.Millisecond, time.Millisecond, time.Millisecond)
	c := e.start()
	waitState(t, c, StateConnected)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = c.Status()
					_ = c.Session()
				}
			}
		}()
	}
	for i := 0; i < 3; i++ {
		waitState(t, c, StateConnected)
		e.write("exit", "1")
		e.clock.backoff(t, i).fire()
	}
	waitState(t, c, StateConnected)
	close(stop)
	wg.Wait()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// The production path, with the real clocks: a crash is followed by a real
// backoff and a real reconnect, with no test hook involved.
func TestRealClocksReconnectEndToEnd(t *testing.T) {
	e := newEnv(t, "ok", 50*time.Millisecond)
	c, err := Start(context.Background(), e.cfg)
	if err != nil {
		t.Fatal(err)
	}
	e.conn = c
	waitState(t, c, StateConnected)
	first := c.Status().PID

	began := time.Now()
	e.write("exit", "2")
	eventually(t, func() bool { st := c.Status(); return st.State == StateConnected && st.PID != first }, "a real reconnect")
	if took := time.Since(began); took < 50*time.Millisecond {
		t.Errorf("reconnected after %v, before the 50ms backoff elapsed", took)
	}
	if callEcho(t, c) != "echo gen 1" {
		t.Fatal("the replacement does not serve")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	e.noSignals()
}
