package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterExerciseTools_NoPanic(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: "test"}, nil)
	registerExerciseTools(s)
}

func TestSearchExercisesTool_ReturnsResults(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: "test"}, nil)
	registerExerciseTools(s)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatal(err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "test"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "search_exercises",
		Arguments: map[string]any{"query": "bench press"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("want success, got error result: %v", result.Content)
	}
	if len(result.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(result.Content))
	}
}
