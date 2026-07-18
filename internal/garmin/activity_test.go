package garmin

import (
	"testing"
	"time"
)

func TestActivityFrom(t *testing.T) {
	row := map[string]any{
		"time":          "2026-07-06T10:30:00Z",
		"sport":         "running",
		"activity_id":   float64(1234567890123456),
		"distance_m":    float64(10000),
		"duration_s":    float64(3600),
		"avg_hr_bpm":    float64(145),
		"training_load": float64(82.5),
		// running-specific
		"cadence_avg_spm":    float64(170),
		"ground_contact_time_ms": float64(248),
	}

	a := ActivityFrom(row)

	if a.Sport != "running" {
		t.Errorf("Sport: got %q, want %q", a.Sport, "running")
	}
	if a.ActivityID != 1234567890123456 {
		t.Errorf("ActivityID: got %d, want 1234567890123456", a.ActivityID)
	}
	if a.DistanceM != 10000 {
		t.Errorf("DistanceM: got %v, want 10000", a.DistanceM)
	}
	if a.TrainingLoad != 82.5 {
		t.Errorf("TrainingLoad: got %v, want 82.5", a.TrainingLoad)
	}
	if a.CadenceAvgSPM != 170 {
		t.Errorf("CadenceAvgSPM: got %v, want 170", a.CadenceAvgSPM)
	}
	want := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)
	if !a.Time.Equal(want) {
		t.Errorf("Time: got %v, want %v", a.Time, want)
	}
}

func TestActivityFrom_MissingFields(t *testing.T) {
	// Partial row — only required fields present; missing fields default to zero.
	row := map[string]any{
		"time":  "2026-07-06T10:30:00Z",
		"sport": "cycling",
	}
	a := ActivityFrom(row)
	if a.Sport != "cycling" {
		t.Errorf("Sport: got %q, want %q", a.Sport, "cycling")
	}
	if a.DistanceM != 0 {
		t.Errorf("DistanceM: got %v, want 0 for missing field", a.DistanceM)
	}
	if a.CadenceAvgSPM != 0 {
		t.Errorf("CadenceAvgSPM: got %v, want 0 for non-running activity", a.CadenceAvgSPM)
	}
}
