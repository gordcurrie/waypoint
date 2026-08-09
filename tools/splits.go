package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerSplitTools(s *mcp.Server, client influxClient) {
	type activityDetailInput struct {
		ActivityID int64 `json:"activity_id" jsonschema:"Garmin activity ID from get_recent_activities"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_activity_splits",
		Title:       "Activity Splits",
		Description: "Return per-lap split data for a specific activity: distance, duration, average speed, heart rate, cadence, and power per lap.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input activityDetailInput) (*mcp.CallToolResult, any, error) {
		if input.ActivityID <= 0 {
			return errorResult(fmt.Errorf("get_activity_splits: activity_id is required"))
		}
		laps, err := queryActivitySplits(ctx, client, input.ActivityID)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(laps)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_activity_hr_zones",
		Title:       "Activity HR Zones",
		Description: "Return heart rate zone distribution for a specific activity. Fields z1_s through z5_s report seconds in each zone; zones with zero seconds are omitted. Use alongside get_activity_splits to understand aerobic vs threshold intensity.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input activityDetailInput) (*mcp.CallToolResult, any, error) {
		if input.ActivityID <= 0 {
			return errorResult(fmt.Errorf("get_activity_hr_zones: activity_id is required"))
		}
		zones, err := queryActivityHRZones(ctx, client, input.ActivityID)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(zones)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_activity_exercise_sets",
		Title: "Activity Exercise Sets",
		Description: "Return per-set detail (category, exercise_name, duration_s, reps, weight_kg, set_type ACTIVE/REST) for a strength_training activity, in set order. " +
			"Only populated for strength_training activities — empty for other sport types. category/exercise_name are Garmin's own device-detected exercise (top ML candidate), not user-entered.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input activityDetailInput) (*mcp.CallToolResult, any, error) {
		if input.ActivityID <= 0 {
			return errorResult(fmt.Errorf("get_activity_exercise_sets: activity_id is required"))
		}
		sets, err := queryActivityExerciseSets(ctx, client, input.ActivityID)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(sets)
	})
}

func queryActivitySplits(ctx context.Context, client influxClient, activityID int64) ([]garmin.Lap, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE activity_id = '%d' ORDER BY time ASC",
		influx.MeasurementActivityLap, activityID,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_activity_splits: %w", err)
	}
	laps := make([]garmin.Lap, 0, len(rows))
	for _, row := range rows {
		laps = append(laps, garmin.LapFrom(row))
	}
	return laps, nil
}

func queryActivityExerciseSets(ctx context.Context, client influxClient, activityID int64) ([]garmin.ExerciseSet, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE activity_id = '%d' ORDER BY time ASC",
		influx.MeasurementActivityExerciseSet, activityID,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_activity_exercise_sets: %w", err)
	}
	sets := make([]garmin.ExerciseSet, 0, len(rows))
	for _, row := range rows {
		sets = append(sets, garmin.ExerciseSetFrom(row))
	}
	return sets, nil
}

func queryActivityHRZones(ctx context.Context, client influxClient, activityID int64) (*garmin.ActivityHRZones, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE activity_id = '%d' ORDER BY time DESC LIMIT 1",
		influx.MeasurementActivityHRZones, activityID,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_activity_hr_zones: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	z := garmin.ActivityHRZonesFrom(rows[0])
	return &z, nil
}
