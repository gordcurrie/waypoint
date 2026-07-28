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

func TestClampInt_ForSearchLimit(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"zero uses default", 0, 20},
		{"negative uses default", -5, 20},
		{"within range unchanged", 10, 10},
		{"exceeds max clamps to max", 100, 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampInt(tt.n, 20, 50)
			if got != tt.want {
				t.Errorf("clampInt(%d, 20, 50) = %d, want %d", tt.n, got, tt.want)
			}
		})
	}
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
	defer session.Close()

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
