package mcptest_test

import (
	"context"
	"fmt"

	"github.com/hollis-labs/go-mcp/mcptest"
	"github.com/hollis-labs/go-mcp/server"
)

// exampleT is a minimal TestingT for a runnable example; in a real test, pass
// the *testing.T.
type exampleT struct{}

func (exampleT) Helper()                   {}
func (exampleT) Fatalf(f string, a ...any) { fmt.Printf(f+"\n", a...) }

func ExampleConnect() {
	srv := server.NewServer("demo", "1.0.0")
	srv.RegisterTool(server.Tool{
		Name: "ping", Description: "replies pong",
		InputSchema:  server.EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(context.Context, map[string]any) (any, error) {
			return map[string]any{"reply": "pong"}, nil
		},
	})

	cs, cleanup := mcptest.Connect(exampleT{}, srv)
	defer cleanup()

	fmt.Println(cs.InitializeResult().ServerInfo.Name)
	// Output: demo
}
