package tools

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin/exercises"
)

func registerExerciseTools(s *mcp.Server) {
	type searchExercisesInput struct {
		Query    string `json:"query,omitempty"    jsonschema:"search term matched against exercise name, e.g. bench press or lunge"`
		Category string `json:"category,omitempty" jsonschema:"restrict to an exact Garmin exercise category, e.g. BENCH_PRESS or SQUAT"`
		Limit    int    `json:"limit,omitempty"    jsonschema:"max results, default 20, max 50"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_exercises",
		Description: "Search Garmin Connect's exercise catalog for valid category/exercise_name pairs. " +
			"Always call this before setting category/exercise_name on a create_workout strength step — " +
			"free-text guesses will be rejected since only real Garmin catalog entries are accepted.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input searchExercisesInput) (*mcp.CallToolResult, any, error) {
		limit := clampInt(input.Limit, 20, 50)
		results := exercises.Search(input.Query, input.Category, limit)
		return jsonResult(results)
	})
}
