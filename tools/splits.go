package tools

import (
	"context"
	"fmt"
	"time"

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
			"Only populated for strength_training activities — empty for other sport types. category/exercise_name are Garmin's own device-detected exercise (top ML candidate), not user-entered. " +
			"weight_kg's unit is unconfirmed — no device in this account has ever logged a non-null weight, so \"kg\" is a provisional label, not a verified one.",
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

// activityLookupHorizon bounds the initial activity_id -> date lookup itself
// (#95) — the "activity" measurement has far fewer rows than the per-activity
// detail measurements (one row per activity vs. many per activity), so it hits
// InfluxDB 3 Core's file-scan limit much later, but an unbounded query against
// it would eventually hit the same wall. 2 years comfortably covers anything
// these detail tools are realistically asked about.
const activityLookupHorizon = 2 * 365 * 24 * time.Hour

// activityTimeWindow resolves activityID's own timestamp (a single bounded query
// against the small "activity" measurement) and returns a window around it,
// wide enough to contain every point written for that activity (laps/HR-zones/
// exercise-sets are all timestamped within the activity's own duration, plus a
// day of slack either side for any timezone-adjacent edge case) without scanning
// the detail measurement's entire history.
//
// #95: get_activity_splits/hr_zones/exercise_sets previously queried
// activity_lap/activity_hr_zones/activity_exercise_set filtered only by
// activity_id, with no time bound at all — InfluxDB 3 Core partitions by time
// and can't prune on a non-time predicate, so this scanned every Parquet file
// in the table's entire history and started failing live once that file count
// passed InfluxDB's scan limit (432 files on a real activity, 2026-08-09).
func activityTimeWindow(ctx context.Context, client influxClient, activityID int64) (time.Time, time.Time, error) {
	start := time.Now().UTC().Add(-activityLookupHorizon)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE activity_id = '%d' AND time >= '%s' ORDER BY time DESC LIMIT 1",
		influx.MeasurementActivity, activityID, start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("look up activity %d date: %w", activityID, err)
	}
	if len(rows) == 0 {
		return time.Time{}, time.Time{}, fmt.Errorf("activity %d not found in the last 2 years", activityID)
	}
	activity := garmin.ActivityFrom(rows[0])
	// Window end tracks the activity's own duration (not a flat +24h) so an
	// ultra/expedition-length activity doesn't silently lose laps/HR-zone/
	// exercise-set points past a fixed cutoff — padded generously on both sides
	// for clock-skew/timezone-adjacent edge cases, not because detail points are
	// ever expected outside the activity's own span.
	pad := 6 * time.Hour
	duration := time.Duration(activity.DurationS) * time.Second
	if duration <= 0 {
		duration = 24 * time.Hour // DurationS missing/zero — fall back to a generous flat window
	}
	return activity.Time.Add(-pad), activity.Time.Add(duration).Add(pad), nil
}

// queryActivityDetailRows resolves activityID's time window (#95) and runs a
// time-bounded query against a per-activity detail measurement. Centralized so
// any future per-activity-detail tool goes through the same time-bounding path
// by construction, rather than copying the SQL shape by hand and risking the
// same drift that left tools/workouts.go's queryWorkoutDetail unbounded even
// after this exact pattern was established here.
func queryActivityDetailRows(
	ctx context.Context, client influxClient, measurement string, activityID int64, orderClause string,
) ([]map[string]any, error) {
	windowStart, windowEnd, err := activityTimeWindow(ctx, client, activityID)
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE activity_id = '%d' AND time >= '%s' AND time < '%s' %s",
		measurement, activityID,
		windowStart.Format(time.RFC3339), windowEnd.Format(time.RFC3339), orderClause,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", measurement, err)
	}
	return rows, nil
}

func queryActivitySplits(ctx context.Context, client influxClient, activityID int64) ([]garmin.Lap, error) {
	rows, err := queryActivityDetailRows(ctx, client, influx.MeasurementActivityLap, activityID, "ORDER BY time ASC")
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
	// set_index (messageIndex) breaks ties on time — sync.py falls back to the
	// activity's own start time when a set's startTime is missing/unparseable,
	// which can put more than one set at the same timestamp. Cast: set_index is
	// stored as a string tag, so a plain ORDER BY would sort "10" before "2".
	rows, err := queryActivityDetailRows(
		ctx, client, influx.MeasurementActivityExerciseSet, activityID,
		"ORDER BY time ASC, CAST(set_index AS BIGINT) ASC",
	)
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
	rows, err := queryActivityDetailRows(
		ctx, client, influx.MeasurementActivityHRZones, activityID, "ORDER BY time DESC LIMIT 1",
	)
	if err != nil {
		return nil, fmt.Errorf("get_activity_hr_zones: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	z := garmin.ActivityHRZonesFrom(rows[0])
	return &z, nil
}
