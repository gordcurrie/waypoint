package tools

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callCreateWorkout registers the workout tools against a fresh in-memory session
// and calls create_workout with the given arguments, returning the tool result.
func callCreateWorkout(t *testing.T, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: "test"}, nil)
	registerWorkoutTools(s, &mockClient{}, t.TempDir())

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

	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_workout", Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCreateWorkout_RepsEndCondition(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bench day",
		"sport": "strength_training",
		"steps": []map[string]any{
			{"type": "interval", "reps": 8, "category": "BENCH_PRESS", "exercise_name": "BARBELL_BENCH_PRESS"},
		},
	})
	if result.IsError {
		t.Fatalf("want success, got error: %v", result.Content)
	}
}

func TestCreateWorkout_NoEndCondition(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{{"type": "interval"}},
	})
	if !result.IsError {
		t.Fatal("want error when no end condition is specified")
	}
}

func TestCreateWorkout_TwoEndConditions(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "running",
		"steps": []map[string]any{{"type": "interval", "duration_s": 60, "reps": 8}},
	})
	if !result.IsError {
		t.Fatal("want error when both duration_s and reps are specified")
	}
}

func TestCreateWorkout_ExerciseNameWithoutCategory(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{{"type": "interval", "reps": 8, "exercise_name": "BARBELL_BENCH_PRESS"}},
	})
	if !result.IsError {
		t.Fatal("want error when exercise_name is set without category")
	}
}

func TestCreateWorkout_InvalidExercisePair(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{
			{"type": "interval", "reps": 8, "category": "NOT_A_CATEGORY", "exercise_name": "NOT_AN_EXERCISE"},
		},
	})
	if !result.IsError {
		t.Fatal("want error for a category/exercise_name pair not in the catalog")
	}
}

func TestCreateWorkout_ValidSetsAndRest(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bench day",
		"sport": "strength_training",
		"steps": []map[string]any{
			{
				"type": "interval", "reps": 8, "sets": 3, "rest_s": 60,
				"category": "BENCH_PRESS", "exercise_name": "BARBELL_BENCH_PRESS",
			},
		},
	})
	if result.IsError {
		t.Fatalf("want success, got error: %v", result.Content)
	}
}

func TestCreateWorkout_SetsOfOneRejected(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{{"type": "interval", "reps": 8, "sets": 1, "rest_s": 60}},
	})
	if !result.IsError {
		t.Fatal("want error when sets=1 (a no-op wrapper)")
	}
}

func TestCreateWorkout_SetsWithoutRestRejected(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{{"type": "interval", "reps": 8, "sets": 3}},
	})
	if !result.IsError {
		t.Fatal("want error when sets is set without rest_s")
	}
}

func TestCreateWorkout_RestWithoutSetsRejected(t *testing.T) {
	result := callCreateWorkout(t, map[string]any{
		"name":  "Bad",
		"sport": "strength_training",
		"steps": []map[string]any{{"type": "interval", "reps": 8, "rest_s": 60}},
	})
	if !result.IsError {
		t.Fatal("want error when rest_s is set without sets")
	}
}

func TestQueryScheduledWorkouts_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	workouts, err := queryScheduledWorkouts(context.Background(), client, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(workouts) != 0 {
		t.Errorf("want 0 workouts, got %d", len(workouts))
	}
}

func TestQueryScheduledWorkouts_ReturnsWorkouts(t *testing.T) {
	tomorrow := time.Now().UTC().Add(24 * time.Hour)
	client := &mockClient{
		rows: []map[string]any{
			{
				"scheduled_id": "111222333",
				"workout_id":   float64(444555666),
				"time":         tomorrow.Format(time.RFC3339),
				"name":         "Tempo Run",
				"sport":        "running",
				"duration_s":   float64(2700),
			},
		},
	}
	workouts, err := queryScheduledWorkouts(context.Background(), client, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(workouts) != 1 {
		t.Fatalf("want 1 workout, got %d", len(workouts))
	}
	if workouts[0].Name != "Tempo Run" {
		t.Errorf("Name: got %q, want Tempo Run", workouts[0].Name)
	}
	if workouts[0].DurationS != 2700 {
		t.Errorf("DurationS: got %v, want 2700", workouts[0].DurationS)
	}
}

func TestQueryScheduledWorkouts_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("connection refused")}
	_, err := queryScheduledWorkouts(context.Background(), client, 14)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestLoadQueue_EmptyWhenFileAbsent(t *testing.T) {
	dir := t.TempDir()
	items, err := loadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Errorf("want empty queue, got %d items", len(items))
	}
}

func TestSaveAndLoadQueue_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dur := 600
	want := []WorkoutQueueItem{
		{ID: "123", Name: "Warmup run", Sport: "running", Steps: []WorkoutStep{
			{Type: "warmup", DurationS: &dur, Description: "easy"},
		}},
	}
	if err := saveQueue(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 item, got %d", len(got))
	}
	if got[0].ID != "123" || got[0].Name != "Warmup run" {
		t.Errorf("round-trip mismatch: got %+v", got[0])
	}
	if got[0].Steps[0].DurationS == nil || *got[0].Steps[0].DurationS != 600 {
		t.Errorf("step DurationS round-trip failed")
	}
}

func TestSaveAndLoadQueue_RoundTrip_StrengthFields(t *testing.T) {
	dir := t.TempDir()
	reps, sets, restS := 8, 3, 60
	category, exerciseName := "BENCH_PRESS", "BARBELL_BENCH_PRESS"
	want := []WorkoutQueueItem{
		{ID: "456", Name: "Bench day", Sport: "strength_training", Steps: []WorkoutStep{
			{
				Type: "interval", Reps: &reps, Sets: &sets, RestS: &restS,
				Category: &category, ExerciseName: &exerciseName,
			},
		}},
	}
	if err := saveQueue(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := loadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 item, got %d", len(got))
	}
	step := got[0].Steps[0]
	if step.Reps == nil || *step.Reps != 8 {
		t.Errorf("Reps round-trip failed: got %v", step.Reps)
	}
	if step.Sets == nil || *step.Sets != 3 {
		t.Errorf("Sets round-trip failed: got %v", step.Sets)
	}
	if step.RestS == nil || *step.RestS != 60 {
		t.Errorf("RestS round-trip failed: got %v", step.RestS)
	}
	if step.Category == nil || *step.Category != "BENCH_PRESS" {
		t.Errorf("Category round-trip failed: got %v", step.Category)
	}
	if step.ExerciseName == nil || *step.ExerciseName != "BARBELL_BENCH_PRESS" {
		t.Errorf("ExerciseName round-trip failed: got %v", step.ExerciseName)
	}
}

func TestAppendToQueue_AccumulatesItems(t *testing.T) {
	dir := t.TempDir()
	dur := 1200
	a := WorkoutQueueItem{ID: "a", Name: "First", Sport: "running", Steps: []WorkoutStep{{Type: "interval", DurationS: &dur}}}
	b := WorkoutQueueItem{ID: "b", Name: "Second", Sport: "cycling", Steps: []WorkoutStep{{Type: "steady", DurationS: &dur}}}
	if err := appendToQueue(dir, a); err != nil {
		t.Fatal(err)
	}
	if err := appendToQueue(dir, b); err != nil {
		t.Fatal(err)
	}
	items, err := loadQueue(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	if items[0].ID != "a" || items[1].ID != "b" {
		t.Errorf("order mismatch: got %q %q", items[0].ID, items[1].ID)
	}
}

func TestAppendToQueue_WritesToTmpThenRenames(t *testing.T) {
	dir := t.TempDir()
	dur := 300
	item := WorkoutQueueItem{ID: "x", Name: "Test", Sport: "running", Steps: []WorkoutStep{{Type: "cooldown", DurationS: &dur}}}
	if err := appendToQueue(dir, item); err != nil {
		t.Fatal(err)
	}
	// tmp file must be gone after atomic rename
	if _, err := os.Stat(queuePath(dir) + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file still present after append")
	}
}
