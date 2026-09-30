package skills_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing/fstest"

	"github.com/hollis-labs/go-mcp/server"
	"github.com/hollis-labs/go-mcp/skills"
)

// A server publishes its orientation docs as one tool. In a real server the
// file system is an embed.FS ("//go:embed skills/*.md", then fs.Sub).
func ExampleRegister() {
	fsys := fstest.MapFS{
		"start-here.md": {Data: []byte("# Where to begin\nCall the search tool first.")},
		"runs.md":       {Data: []byte("# Inspecting runs\nUse the inspect tool.")},
	}
	src, err := skills.FSSource(fsys)
	if err != nil {
		panic(err)
	}

	srv := server.NewServer("demo", "1.0.0")
	err = skills.Register(srv, "demo_skills",
		"Demo orientation. Call with no arguments for the catalog; pass `name` to read one skill.", src)
	if err != nil {
		panic(err)
	}

	catalog, _ := srv.CallTool(context.Background(), "demo_skills", nil)
	raw, _ := json.Marshal(catalog)
	fmt.Println(string(raw))

	body, _ := srv.CallTool(context.Background(), "demo_skills", map[string]any{"name": "start-here"})
	fmt.Println(body)

	_, err = srv.CallTool(context.Background(), "demo_skills", map[string]any{"name": "nope"})
	fmt.Println(err)
	// Output:
	// {"items":[{"name":"start-here","description":"Where to begin"},{"name":"runs","description":"Inspecting runs"}],"meta":{"count":2,"next":"demo_skills","progressive_discovery":true}}
	// # Where to begin
	// Call the search tool first.
	// skill_not_found: skill not found: "nope". Available: [start-here, runs]
}

// MapSource holds an ordered index and its bodies in memory, and WithArgName
// keeps a server's existing argument name.
func ExampleMapSource() {
	src := skills.MapSource(
		[]skills.Meta{{Name: "start-here", Description: "Orientation."}},
		map[string]string{"start-here": "Read this first."},
	)
	srv := server.NewServer("demo", "1.0.0")
	if err := skills.Register(srv, "demo_guide", "Guide.", src, skills.WithArgName("topic")); err != nil {
		panic(err)
	}
	body, _ := srv.CallTool(context.Background(), "demo_guide", map[string]any{"topic": "start-here"})
	fmt.Println(body)
	// Output: Read this first.
}
