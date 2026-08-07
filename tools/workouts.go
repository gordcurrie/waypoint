package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/garmin/exercises"
	"github.com/gordcurrie/waypoint/internal/influx"
)

// WorkoutStep is a single step in a structured workout.
//
// A strength exercise step sets Category/ExerciseName (a validated pair from Garmin's
// exercise catalog — see search_exercises) and Reps as its end condition. Sets/RestS
// turn the step into a repeated set: sync.py wraps it in a Garmin RepeatGroupDTO with
// a synthesized rest step between each rep, rather than the caller listing out each
// set and rest individually.
type WorkoutStep struct {
	Type         string  `json:"type"`
	DurationS    *int    `json:"duration_s,omitempty"`
	DistanceM    *int    `json:"distance_m,omitempty"`
	Reps         *int    `json:"reps,omitempty"`
	Sets         *int    `json:"sets,omitempty"`
	RestS        *int    `json:"rest_s,omitempty"`
	Category     *string `json:"category,omitempty"`
	ExerciseName *string `json:"exercise_name,omitempty"`
	TargetHRZone *int    `json:"target_hr_zone,omitempty"`
	Description  string  `json:"description,omitempty"`
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

// "walking" is deliberately excluded: no working Garmin workout-service sportTypeId
// has been found for it (verified 2026-07-28 — see sync.py's _SPORT_TYPES comment).
var validSports = map[string]bool{
	"running": true, "cycling": true,
	"swimming": true, "strength_training": true,
}

var queueMu sync.Mutex

func registerWorkoutTools(s *mcp.Server, client influxClient, dataDir string) {
	type scheduledWorkoutsInput struct {
		Days int `json:"days,omitempty" jsonschema:"look-ahead window in days, default 14, max 60"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_scheduled_workouts",
		Title: "Scheduled Workouts",
		Description: "Return workouts scheduled on the Garmin calendar for the next N days (default 14). Use before create_workout to avoid scheduling conflicts. " +
			"Coach/training-plan-assigned days are enriched with real target detail from the active adaptive plan: duration_s, distance_m, description (the actual pace/HR target, e.g. \"21:00@5:10/km\" or \"137bpm\"), phase (BASE/BUILD/PEAK/TAPER/TARGET_EVENT_DAY), and rest_day. " +
			"Rest days appear here even though they have no real Garmin calendar entry — scheduled_id is 0 for those, since it's a plan entry, not a real calendar item. scheduled_id is also 0 for coach/training-plan-assigned workouts generally (deduped by sport+name rather than Garmin's own id); only self-created workouts (via create_workout) carry a real nonzero scheduled_id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input scheduledWorkoutsInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 14, 60)
		workouts, err := queryScheduledWorkouts(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(workouts)
	})

	type createWorkoutInput struct {
		Name  string        `json:"name"  jsonschema:"workout name e.g. Tuesday tempo run"`
		Sport string        `json:"sport" jsonschema:"sport type: running cycling swimming strength_training"`
		Steps []WorkoutStep `json:"steps" jsonschema:"ordered list of workout steps"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_workout",
		Title: "Create Workout",
		Description: "Queue a structured workout for upload to Garmin Connect. The Python sidecar uploads it on the next sync run (every 30 minutes by default; set via SYNC_SCHEDULE). Requires --data-dir pointing to the shared sync volume. Returns the queue ID. " +
			"Check get_scheduled_workouts first to avoid conflicts. " +
			"Each step needs type (warmup/interval/recovery/cooldown/steady) and exactly one of duration_s, distance_m, or reps. " +
			"For a strength exercise: call search_exercises first to get a valid category/exercise_name pair (free-text guesses are rejected), set reps, and optionally sets + rest_s to repeat it as a set — e.g. sets=3, rest_s=60 becomes \"3 sets of N reps, 60s rest between\" on the watch. " +
			"Optional target: target_hr_zone (1–5).",
		// Queues a new item onto the shared file — additive, not destructive (nothing is
		// overwritten or deleted), and each call creates a distinct queue entry so it's not
		// idempotent. Explicit because the SDK defaults DestructiveHint to true when unset.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input createWorkoutInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult(fmt.Errorf("name is required"))
		}
		if !validSports[input.Sport] {
			return errorResult(fmt.Errorf("invalid sport %q (valid: running, cycling, swimming, strength_training)", input.Sport))
		}
		if len(input.Steps) == 0 {
			return errorResult(fmt.Errorf("steps must not be empty"))
		}
		for i, step := range input.Steps {
			if !validStepTypes[step.Type] {
				return errorResult(fmt.Errorf("create_workout: step %d: invalid type %q (valid: warmup, interval, recovery, cooldown, steady)", i+1, step.Type))
			}
			if (step.Reps != nil || step.Sets != nil || step.Category != nil) && step.Type != "interval" {
				return errorResult(fmt.Errorf("create_workout: step %d: reps/sets/category/exercise_name require type \"interval\" — Garmin's exercise-performing steps always use that stepType, got %q", i+1, step.Type))
			}
			endConditions := 0
			if step.DurationS != nil {
				endConditions++
			}
			if step.DistanceM != nil {
				endConditions++
			}
			if step.Reps != nil {
				endConditions++
			}
			if endConditions != 1 {
				return errorResult(fmt.Errorf("create_workout: step %d: specify exactly one of duration_s, distance_m, or reps", i+1))
			}
			if step.TargetHRZone != nil && (*step.TargetHRZone < 1 || *step.TargetHRZone > 5) {
				return errorResult(fmt.Errorf("create_workout: step %d: target_hr_zone must be 1–5", i+1))
			}
			if (step.Category == nil) != (step.ExerciseName == nil) {
				return errorResult(fmt.Errorf("create_workout: step %d: category and exercise_name must be set together", i+1))
			}
			if step.Category != nil && !exercises.Valid(*step.Category, *step.ExerciseName) {
				return errorResult(fmt.Errorf("create_workout: step %d: %q/%q is not a real Garmin exercise — use search_exercises to find a valid pair", i+1, *step.Category, *step.ExerciseName))
			}
			if step.Sets != nil && *step.Sets < 2 {
				return errorResult(fmt.Errorf("create_workout: step %d: sets must be >= 2 (omit sets entirely for a single set)", i+1))
			}
			if step.Sets != nil && (step.RestS == nil || *step.RestS <= 0) {
				return errorResult(fmt.Errorf("create_workout: step %d: sets requires rest_s > 0", i+1))
			}
			if step.Sets == nil && step.RestS != nil {
				return errorResult(fmt.Errorf("create_workout: step %d: rest_s requires sets", i+1))
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

	type workoutDetailInput struct {
		WorkoutID int64 `json:"workout_id" jsonschema:"Garmin-assigned workout id (see get_scheduled_workouts' workout_id field)"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_workout_detail",
		Title: "Workout Detail",
		Description: "Return the step-by-step detail Garmin actually stored for a workout, keyed by its Garmin-assigned workout_id (see get_scheduled_workouts). " +
			"Only available for workouts uploaded via create_workout — the sync sidecar records what Garmin's response contained at upload time. Coach-plan workouts and anything built directly in the Garmin Connect app were never uploaded through this queue, so no detail is recorded for them. " +
			"steps is Garmin's own raw step tree (ExecutableStepDTO/RepeatGroupDTO shape) — useful to confirm what actually landed after an upload, since Garmin can silently normalize or reject parts of what was sent.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input workoutDetailInput) (*mcp.CallToolResult, any, error) {
		if input.WorkoutID <= 0 {
			return errorResult(fmt.Errorf("get_workout_detail: workout_id is required"))
		}
		detail, err := queryWorkoutDetail(ctx, client, input.WorkoutID)
		if err != nil {
			return errorResult(err)
		}
		// detail is nil (jsonResult marshals to JSON null) when no detail was recorded
		// for this workout_id — e.g. it wasn't uploaded via create_workout. Not an error
		// condition: consistent with get_activity_hr_zones's no-match handling.
		return jsonResult(detail)
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
		return nil, fmt.Errorf("read queue: %w", err)
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
		return fmt.Errorf("marshal queue: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}
	tmp := queuePath(dataDir) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write queue tmp: %w", err)
	}
	if err := os.Rename(tmp, queuePath(dataDir)); err != nil {
		return fmt.Errorf("rename queue: %w", err)
	}
	return nil
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

	rows, err := queryMeasurementRange(ctx, client, influx.MeasurementScheduledWorkout, start, end)
	if err != nil {
		return nil, fmt.Errorf("get_scheduled_workouts: %w", err)
	}
	workouts := make([]garmin.ScheduledWorkout, 0, len(rows))
	for _, row := range rows {
		workouts = append(workouts, garmin.ScheduledWorkoutFrom(row))
	}

	planRows, err := queryMeasurementRange(ctx, client, influx.MeasurementTrainingPlanTask, start, end)
	if err != nil {
		return nil, fmt.Errorf("get_scheduled_workouts: training plan detail: %w", err)
	}
	tasks := make([]garmin.TrainingPlanTask, 0, len(planRows))
	for _, row := range planRows {
		tasks = append(tasks, garmin.TrainingPlanTaskFrom(row))
	}

	return mergeTrainingPlanDetail(workouts, tasks), nil
}

func queryWorkoutDetail(ctx context.Context, client influxClient, workoutID int64) (*garmin.WorkoutDetail, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE workout_id = '%d' ORDER BY time DESC LIMIT 1",
		influx.MeasurementWorkoutDetail, workoutID,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_workout_detail: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	detail := garmin.WorkoutDetailFrom(rows[0])
	return &detail, nil
}

func queryMeasurementRange(ctx context.Context, client influxClient, measurement string, start, end time.Time) ([]map[string]any, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' AND time < '%s' ORDER BY time ASC",
		measurement,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", measurement, err)
	}
	return rows, nil
}

// mergeTrainingPlanDetail enriches calendar-list workouts with real per-day detail from
// the active adaptive training plan, and adds a synthesized entry for any plan day with
// no calendar item at all — which is how rest days show up, since Garmin's calendar list
// never includes them (see sync_training_plan's doc comment in sync.py).
//
// Only enriches workouts with WorkoutID == 0 (i.e. items sync_scheduled_workouts already
// identified as coach-plan-sourced, not self-created via create_workout) — a self-created
// workout that happens to land on the same date as a coach-plan task must not inherit the
// coach's target detail.
//
// Joins on (date, sport), not date alone — verified live 2026-08-07 that the coach plan
// can assign two tasks on the same calendarDate (running + strength_training, a real
// two-a-day). A date-only join would match one task to both calendar entries and drop
// the other's detail.
func mergeTrainingPlanDetail(workouts []garmin.ScheduledWorkout, tasks []garmin.TrainingPlanTask) []garmin.ScheduledWorkout {
	byKey := make(map[string]garmin.TrainingPlanTask, len(tasks))
	for _, t := range tasks {
		byKey[planTaskKey(t.Date, t.Sport)] = t
	}

	merged := make([]garmin.ScheduledWorkout, 0, len(workouts)+len(tasks))
	seen := make(map[string]bool, len(workouts))
	for _, w := range workouts {
		key := planTaskKey(w.Date, w.Sport)
		if t, ok := byKey[key]; ok && w.WorkoutID == 0 {
			w.DistanceM = t.DistanceM
			w.Description = t.Description
			w.RestDay = t.RestDay
			w.Phase = t.Phase
			if w.DurationS == 0 {
				w.DurationS = t.DurationS
			}
		}
		merged = append(merged, w)
		seen[key] = true
	}
	for _, t := range tasks {
		if seen[planTaskKey(t.Date, t.Sport)] {
			continue
		}
		merged = append(merged, garmin.ScheduledWorkout{
			Date:        t.Date,
			Sport:       t.Sport,
			Name:        t.Name,
			DurationS:   t.DurationS,
			DistanceM:   t.DistanceM,
			Description: t.Description,
			RestDay:     t.RestDay,
			Phase:       t.Phase,
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Date != merged[j].Date {
			return merged[i].Date < merged[j].Date
		}
		if merged[i].Sport != merged[j].Sport {
			return merged[i].Sport < merged[j].Sport
		}
		return merged[i].Name < merged[j].Name
	})
	return merged
}

func planTaskKey(date, sport string) string {
	return date + "|" + sport
}
