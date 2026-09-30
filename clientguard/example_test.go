package clientguard_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hollis-labs/go-mcp/clientguard"
)

// A Guard wraps the caller's own call. After threshold consecutive failures
// the circuit opens and Do fails fast with ErrCircuitOpen without calling fn.
func ExampleGuard_Do() {
	g := clientguard.New(
		clientguard.WithCircuitBreaker(2, 30*time.Second),
		clientguard.WithRateLimit(60, time.Minute, clientguard.RateLimitReject),
	)
	upstreamDown := errors.New("upstream down")

	for range 2 {
		_ = g.Do(context.Background(), "flaky", func(context.Context) error { return upstreamDown })
	}
	err := g.Do(context.Background(), "flaky", func(context.Context) error {
		fmt.Println("never printed")
		return nil
	})
	fmt.Println(errors.Is(err, clientguard.ErrCircuitOpen), g.State("flaky"))
	// Output: true open
}

func ExampleDo() {
	g := clientguard.New(clientguard.WithRateLimit(1, time.Hour, clientguard.RateLimitReject))

	for range 2 {
		v, err := clientguard.Do(context.Background(), g, "search", func(context.Context) (string, error) {
			return "result", nil
		})
		fmt.Printf("%q %v\n", v, err)
	}
	// Output:
	// "result" <nil>
	// "" clientguard: rate limit exceeded
}
