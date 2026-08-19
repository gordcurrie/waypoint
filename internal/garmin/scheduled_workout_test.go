package garmin_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestScheduledWorkoutFrom(t *testing.T) {
	row := map[string]any{
		"scheduled_id": "7654321",
		"workout_id":   float64(1234567),
		"time":         "2026-07-25T00:00:00Z",
		"name":         "Easy Run",
		"sport":        "running",
		"duration_s":   float64(1800),
	}

	w := garmin.ScheduledWorkoutFrom(row)

	if w.ScheduledID != 7654321 {
		t.Errorf("ScheduledID: got %d, want 7654321", w.ScheduledID)
	}
	if w.WorkoutID != 1234567 {
		t.Errorf("WorkoutID: got %d, want 1234567", w.WorkoutID)
	}
	if w.Date != "2026-07-25" {
		t.Errorf("Date: got %q, want 2026-07-25", w.Date)
	}
	if w.Name != "Easy Run" {
		t.Errorf("Name: got %q, want Easy Run", w.Name)
	}
	if w.Sport != "running" {
		t.Errorf("Sport: got %q, want running", w.Sport)
	}
	if w.DurationS != 1800 {
		t.Errorf("DurationS: got %v, want 1800", w.DurationS)
	}
}

func TestScheduledWorkoutFrom_DeletedAt(t *testing.T) {
	row := map[string]any{
		"time":       "2026-08-17T00:00:00Z",
		"sport":      "running",
		"deleted_at": float64(1786900000),
	}
	w := garmin.ScheduledWorkoutFrom(row)
	if w.DeletedAt != 1786900000 {
		t.Errorf("DeletedAt: got %v, want 1786900000", w.DeletedAt)
	}
}

func TestScheduledWorkoutFrom_DeletedAtNotInJSON(t *testing.T) {
	// DeletedAt is an internal filter signal (#104), not part of the tool's JSON
	// contract — must never leak to callers even when set.
	row := map[string]any{
		"time":       "2026-08-17T00:00:00Z",
		"sport":      "running",
		"deleted_at": float64(1786900000),
	}
	b, err := json.Marshal(garmin.ScheduledWorkoutFrom(row))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "deleted_at") {
		t.Errorf("JSON must never contain deleted_at, got: %s", string(b))
	}
}

func TestScheduledWorkoutFrom_OptionalFieldsOmitted(t *testing.T) {
	row := map[string]any{
		"scheduled_id": "999",
		"time":         "2026-07-28T00:00:00Z",
	}
	b, err := json.Marshal(garmin.ScheduledWorkoutFrom(row))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, absent := range []string{"workout_id", "name", "sport", "duration_s"} {
		if strings.Contains(s, absent) {
			t.Errorf("JSON should omit %q when zero, got: %s", absent, s)
		}
	}
}
