package supervisedstdio_test

import (
	"context"
	"fmt"
	"time"

	"github.com/hollis-labs/go-mcp/supervise"
	"github.com/hollis-labs/go-mcp/supervisedstdio"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A product that wants a local MCP server to stay up starts a Connection and
// takes a fresh Session for each use. Compile-checked only: it would spawn a
// real server.
func ExampleStart() {
	ctx := context.Background()
	conn, err := supervisedstdio.Start(ctx, supervisedstdio.Config{
		Command: "my-mcp-server",
		Args:    []string{"--stdio"},
		Env:     map[string]string{"API_TOKEN": "..."}, // redacted from the stderr tail
		Policy:  supervise.DefaultPolicy(),             // five restarts at 1s..16s, reset after a minute up
		OnConnect: func(ctx context.Context, cs *mcpsdk.ClientSession) error {
			_, err := cs.ListTools(ctx, nil) // refresh the tool list after every (re)connect
			return err
		},
	})
	if err != nil {
		panic(err) // an invalid Config; a failing child is reported through Status
	}
	defer func() { _ = conn.Close() }()

	if sess := conn.Session(); sess != nil {
		res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: "echo"})
		fmt.Println(res, err) // an error here may mean the outcome is unknown: it is never replayed
	}
	st := conn.Status()
	fmt.Println(st.State, st.Restarts, st.Limit, st.LastExit, time.Until(st.NextRetry))
}
