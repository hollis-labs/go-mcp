//go:build unix

package supervisedstdio

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestFixtureProcess is the child. The tests re-exec the test binary with
// SUP_DIR set and it becomes a disposable MCP server whose behavior comes from
// SUP_PLAN, one mode per generation (the count of "start" events so far; the
// last mode repeats):
//
//	ok             serve MCP; exit 0 when stdin closes
//	ignore-eof     serve MCP; when stdin closes, wait for the release marker
//	startfail      exit 23 before serving
//	hang           never answer the handshake; exit 0 when stdin closes
//	hang-ignore-eof never answer, and wait for the release marker after EOF
//
// Marker files in SUP_DIR steer a running child: "exit" (contents: a code) makes
// it exit at once; "lose-stdout" makes it close stdout and stay alive until
// "release" appears. Every notable moment is appended to SUP_DIR/events, and a
// signal received is recorded too: the tests assert none ever arrives.
func TestFixtureProcess(t *testing.T) {
	dir := os.Getenv("SUP_DIR")
	if dir == "" {
		return
	}
	fixtureMain(dir)
}

func fixtureEvent(dir, event string) {
	f, err := os.OpenFile(filepath.Join(dir, "events"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // a test temp dir
	if err != nil {
		os.Exit(90)
	}
	_, _ = fmt.Fprintf(f, "%s %d\n", event, os.Getpid())
	_ = f.Close()
}

func fixtureGeneration(dir string) int {
	b, _ := os.ReadFile(filepath.Join(dir, "events")) //nolint:gosec // a test temp dir
	n := 0
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "start ") {
			n++
		}
	}
	return n
}

func fixtureWaitRelease(dir string) {
	for {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil { //nolint:gosec // a test temp dir
			fixtureEvent(dir, "released")
			os.Exit(0)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func fixtureMain(dir string) {
	gen := fixtureGeneration(dir)
	fixtureEvent(dir, "start")
	plan := strings.Split(os.Getenv("SUP_PLAN"), ",")
	mode := plan[min(gen, len(plan)-1)]
	if s := os.Getenv("SUP_SECRET"); s != "" {
		fmt.Fprintln(os.Stderr, "diagnostic "+s)
	}
	if s := os.Getenv("SUP_INHERITED"); s != "" {
		fmt.Fprintln(os.Stderr, "inherited "+s)
	}
	fmt.Fprintf(os.Stderr, "gen %d mode %s\n", gen, mode)

	sigs := make(chan os.Signal, 8)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGUSR1)
	go func() {
		for s := range sigs {
			fixtureEvent(dir, "signal "+s.String())
		}
	}()
	go func() { // markers
		for {
			if b, err := os.ReadFile(filepath.Join(dir, "exit")); err == nil { //nolint:gosec // a test temp dir
				_ = os.Remove(filepath.Join(dir, "exit")) //nolint:gosec // a test temp dir
				fixtureEvent(dir, "exit-marker")
				code, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				os.Exit(code)
			}
			if _, err := os.Stat(filepath.Join(dir, "lose-stdout")); err == nil { //nolint:gosec // a test temp dir
				_ = os.Remove(filepath.Join(dir, "lose-stdout")) //nolint:gosec // a test temp dir
				fixtureEvent(dir, "stdout-closed")
				_ = os.Stdout.Close()
				fixtureWaitRelease(dir)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	switch mode {
	case "startfail":
		os.Exit(23)
	case "hang", "hang-ignore-eof":
		buf := make([]byte, 4096)
		for {
			if _, err := os.Stdin.Read(buf); err != nil {
				break
			}
		}
		fixtureEvent(dir, "stdin-eof")
		if mode == "hang-ignore-eof" {
			fixtureWaitRelease(dir)
		}
		os.Exit(0)
	}

	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "fixture", Version: "0"}, nil)
	schema := map[string]any{"type": "object"}
	srv.AddTool(&mcpsdk.Tool{Name: "echo", InputSchema: schema}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		fixtureEvent(dir, "call echo")
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: fmt.Sprintf("echo gen %d", gen)}}}, nil
	})
	srv.AddTool(&mcpsdk.Tool{Name: "block", InputSchema: schema}, func(ctx context.Context, _ *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		fixtureEvent(dir, "call block")
		<-ctx.Done()
		return nil, ctx.Err()
	})
	_ = srv.Run(context.Background(), &mcpsdk.StdioTransport{})
	fixtureEvent(dir, "serve-ended")
	if mode == "ignore-eof" {
		fixtureWaitRelease(dir)
	}
	os.Exit(0)
}
