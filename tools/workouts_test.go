package tools

import (
	"context"
	"errors"
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
