package tools

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

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
