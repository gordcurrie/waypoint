package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

// WorkoutStep is a single step in a structured workout.
type WorkoutStep struct {
	Type         string `json:"type"`
	DurationS    *int   `json:"duration_s,omitempty"`
	DistanceM    *int   `json:"distance_m,omitempty"`
	TargetHRZone *int   `json:"target_hr_zone,omitempty"`
	Description  string `json:"description,omitempty"`
}

// WorkoutQueueItem is written to the shared queue file for the Python sidecar to consume.
type WorkoutQueueItem struct {
	ID    string        `json:"id"`
	Name  string        `json:"name"`
	Sport string        `json:"sport"`
	Steps []WorkoutStep `json:"steps"`
}

var validStepTypes = map[string]bool{
	"warmup": true, "interval": true, "recovery": true,
	"cooldown": true, "steady": true,
}

var validSports = map[string]bool{
	"running": true, "cycling": true, "walking": true,
	"swimming": true, "strength_training": true,
}

var queueMu sync.Mutex

func registerWorkoutTools(s *mcp.Server, client influxClient, dataDir string) {
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

	type createWorkoutInput struct {
		Name  string        `json:"name"  jsonschema:"required,description=Workout name e.g. Tuesday tempo run"`
		Sport string        `json:"sport" jsonschema:"required,description=Sport type: running cycling walking swimming strength_training"`
		Steps []WorkoutStep `json:"steps" jsonschema:"required,description=Ordered list of workout steps"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_workout",
		Description: "Queue a structured workout for upload to Garmin Connect. The Python sidecar uploads it on the next sync run (every 30 minutes by default; set via SYNC_SCHEDULE). Requires --data-dir pointing to the shared sync volume. Returns the queue ID. Each step needs type (warmup/interval/recovery/cooldown/steady) and either duration_s or distance_m. Optional target: target_hr_zone (1–5).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input createWorkoutInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult(fmt.Errorf("name is required"))
		}
		if !validSports[input.Sport] {
			return errorResult(fmt.Errorf("invalid sport %q (valid: running, cycling, walking, swimming, strength_training)", input.Sport))
		}
		if len(input.Steps) == 0 {
			return errorResult(fmt.Errorf("steps must not be empty"))
		}
		for i, step := range input.Steps {
			if !validStepTypes[step.Type] {
				return errorResult(fmt.Errorf("create_workout: step %d: invalid type %q (valid: warmup, interval, recovery, cooldown, steady)", i+1, step.Type))
			}
			if step.DurationS == nil && step.DistanceM == nil {
				return errorResult(fmt.Errorf("create_workout: step %d: must specify duration_s or distance_m", i+1))
			}
			if step.DurationS != nil && step.DistanceM != nil {
				return errorResult(fmt.Errorf("create_workout: step %d: specify duration_s or distance_m, not both", i+1))
			}
			if step.TargetHRZone != nil && (*step.TargetHRZone < 1 || *step.TargetHRZone > 5) {
				return errorResult(fmt.Errorf("create_workout: step %d: target_hr_zone must be 1–5", i+1))
			}
		}
		item := WorkoutQueueItem{
			ID:    fmt.Sprintf("%d", time.Now().UnixNano()),
			Name:  input.Name,
			Sport: input.Sport,
			Steps: input.Steps,
		}
		if err := appendToQueue(dataDir, item); err != nil {
			return errorResult(fmt.Errorf("create_workout: queue write: %w", err))
		}
		return jsonResult(map[string]string{"id": item.ID, "name": item.Name, "status": "queued"})
	})
}

func queuePath(dataDir string) string {
	return filepath.Join(dataDir, "workout_queue.json")
}

func loadQueue(dataDir string) ([]WorkoutQueueItem, error) {
	data, err := os.ReadFile(queuePath(dataDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var items []WorkoutQueueItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse queue: %w", err)
	}
	return items, nil
}

func saveQueue(dataDir string, items []WorkoutQueueItem) error {
	data, err := json.Marshal(items)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	tmp := queuePath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, queuePath(dataDir))
}

func appendToQueue(dataDir string, item WorkoutQueueItem) error {
	queueMu.Lock()
	defer queueMu.Unlock()
	items, err := loadQueue(dataDir)
	if err != nil {
		return err
	}
	return saveQueue(dataDir, append(items, item))
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
