package garmin_test

import (
	"testing"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestCalendarEventFrom(t *testing.T) {
	row := map[string]any{
		"event_id":                "28702711",
		"time":                    "2026-10-18T00:00:00Z",
		"name":                    "Test Half",
		"activity_type_id":        float64(1),
		"is_race":                 float64(1),
		"primary_event":           float64(1),
		"start_time_local":        "08:00",
		"time_zone_id":            "America/Chicago",
		"completion_target_value": 13.11,
		"completion_target_unit":  "mile",
		"distance_m":              21098.5,
		"deleted_at":              float64(0),
	}

	e := garmin.CalendarEventFrom(row)

	if e.EventID != 28702711 {
		t.Errorf("EventID: got %d, want 28702711", e.EventID)
	}
	if e.Date != "2026-10-18" {
		t.Errorf("Date: got %q, want 2026-10-18", e.Date)
	}
	if e.Name != "Test Half" {
		t.Errorf("Name: got %q, want Test Half", e.Name)
	}
	if e.ActivityTypeID != 1 {
		t.Errorf("ActivityTypeID: got %d, want 1", e.ActivityTypeID)
	}
	if !e.IsRace || !e.PrimaryEvent {
		t.Errorf("IsRace/PrimaryEvent: got %v/%v, want true/true", e.IsRace, e.PrimaryEvent)
	}
	if e.StartTimeLocal != "08:00" || e.TimeZoneID != "America/Chicago" {
		t.Errorf("start: got %q %q", e.StartTimeLocal, e.TimeZoneID)
	}
	if e.CompletionTargetValue != 13.11 || e.CompletionTargetUnit != "mile" {
		t.Errorf("target: got %v %q, want 13.11 mile", e.CompletionTargetValue, e.CompletionTargetUnit)
	}
	if e.DistanceM != 21098.5 {
		t.Errorf("DistanceM: got %v, want 21098.5", e.DistanceM)
	}
	if e.DeletedAt != 0 {
		t.Errorf("DeletedAt: got %v, want 0", e.DeletedAt)
	}
}

func TestCalendarEventFrom_FalseFlags(t *testing.T) {
	e := garmin.CalendarEventFrom(map[string]any{
		"event_id": "1", "time": "2026-11-01T00:00:00Z",
		"is_race": float64(0), "primary_event": float64(0),
	})
	if e.IsRace || e.PrimaryEvent {
		t.Errorf("IsRace/PrimaryEvent: got %v/%v, want false/false", e.IsRace, e.PrimaryEvent)
	}
}
