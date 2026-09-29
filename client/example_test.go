package client_test

import (
	"context"
	"fmt"
	"net/http/httptest"

	"github.com/hollis-labs/go-mcp/client"
	"github.com/hollis-labs/go-mcp/server"
	httptransport "github.com/hollis-labs/go-mcp/transport/http"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A Pool talking to a go-mcp server over httptest: the pattern to use in a
// caller's own tests instead of a private fake. Per-call _meta reaches the
// handler through server.MetaFromContext.
func ExamplePool_CallTool() {
	srv := server.NewServer("demo", "0.0.0")
	srv.RegisterTool(server.Tool{
		Name:         "whoami",
		Description:  "returns the caller's idempotency key",
		InputSchema:  server.EmptyObjectSchema(),
		ReadOnlyHint: true,
		Handler: func(ctx context.Context, _ map[string]any) (any, error) {
			return server.MetaFromContext(ctx)["myapp/idempotencyKey"], nil
		},
	})
	ts := httptest.NewServer(httptransport.NewHandler(srv, httptransport.HandlerOptions{}))
	defer ts.Close()

	pool := client.NewPool(client.WithIdentity("example", "0.0.0"))
	defer pool.Close()
	if err := pool.Register("demo", client.ServerConfig{Transport: client.TransportHTTP, URL: ts.URL}); err != nil {
		panic(err)
	}

	res, _, err := pool.CallTool(context.Background(), "demo", "whoami", nil,
		client.WithCallMeta(map[string]any{"myapp/idempotencyKey": "key-123"}),
		client.WithCallRetry(client.RetryIfUnsent),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(res.Content[0].(*mcpsdk.TextContent).Text)
	// Output: key-123
}
