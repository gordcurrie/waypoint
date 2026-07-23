package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerWorkoutTools(s *mcp.Server, client influxClient) {
	type scheduledWorkoutsInput struct {
		Days int `json:"days,omitempty" jsonschema:"look-ahead window in days, default 14, max 60"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_scheduled_workouts",
		Description: "Return workouts scheduled on the Garmin calendar for the next N days (default 14). Use before creating new workouts to avoid scheduling conflicts.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input scheduledWorkoutsInput) (*mcp.CallToolResult, any, error) {
		days := input.Days
		if days <= 0 {
			days = 14
		}
		if days > 60 {
			days = 60
		}
		workouts, err := queryScheduledWorkouts(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(workouts)
	})
}

func queryScheduledWorkouts(ctx context.Context, client influxClient, days int) ([]garmin.ScheduledWorkout, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.Add(time.Duration(days) * 24 * time.Hour)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' AND time < '%s' ORDER BY time ASC",
		influx.MeasurementScheduledWorkout,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_scheduled_workouts: %w", err)
	}
	workouts := make([]garmin.ScheduledWorkout, 0, len(rows))
	for _, row := range rows {
		workouts = append(workouts, garmin.ScheduledWorkoutFrom(row))
	}
	return workouts, nil
}
