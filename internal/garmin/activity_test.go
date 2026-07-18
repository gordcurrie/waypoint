package garmin_test

import (
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestActivityFrom(t *testing.T) {
	row := map[string]any{
		"time":                    "2026-07-06T10:30:00Z",
		"sport":                   "running",
		"activity_id":             float64(1234567890123456),
		"distance_m":              float64(10000),
		"duration_s":              float64(3600),
		"avg_hr_bpm":              float64(145),
		"max_hr_bpm":              float64(178),
		"calories_kcal":           float64(620),
		"elevation_gain_m":        float64(85),
		"avg_speed_m_s":           float64(2.78),
		"training_load":           float64(82.5),
		"aerobic_te":              float64(3.2),
		"anaerobic_te":            float64(0.8),
		"vo2max":                  float64(53.0),
		"cadence_avg_spm":         float64(170),
		"ground_contact_time_ms":  float64(248),
		"vertical_oscillation_mm": float64(88),
		"stride_length_mm":        float64(1180),
		"vertical_ratio_pct":      float64(7.4),
		"avg_power_w":             float64(215),
	}

	a := garmin.ActivityFrom(row)

	want := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)
	if !a.Time.Equal(want) {
		t.Errorf("Time: got %v, want %v", a.Time, want)
	}
	if a.Sport != "running" {
		t.Errorf("Sport: got %q, want %q", a.Sport, "running")
	}
	if a.ActivityID != 1234567890123456 {
		t.Errorf("ActivityID: got %d, want 1234567890123456", a.ActivityID)
	}
	if a.DistanceM != 10000 {
		t.Errorf("DistanceM: got %v, want 10000", a.DistanceM)
	}
	if a.DurationS != 3600 {
		t.Errorf("DurationS: got %v, want 3600", a.DurationS)
	}
	if a.AvgHRBPM != 145 {
		t.Errorf("AvgHRBPM: got %v, want 145", a.AvgHRBPM)
	}
	if a.MaxHRBPM != 178 {
		t.Errorf("MaxHRBPM: got %v, want 178", a.MaxHRBPM)
	}
	if a.CaloriesKcal != 620 {
		t.Errorf("CaloriesKcal: got %v, want 620", a.CaloriesKcal)
	}
	if a.ElevationGainM != 85 {
		t.Errorf("ElevationGainM: got %v, want 85", a.ElevationGainM)
	}
	if a.AvgSpeedMpS != 2.78 {
		t.Errorf("AvgSpeedMpS: got %v, want 2.78", a.AvgSpeedMpS)
	}
	if a.TrainingLoad != 82.5 {
		t.Errorf("TrainingLoad: got %v, want 82.5", a.TrainingLoad)
	}
	if a.AerobicTE != 3.2 {
		t.Errorf("AerobicTE: got %v, want 3.2", a.AerobicTE)
	}
	if a.AnaerobicTE != 0.8 {
		t.Errorf("AnaerobicTE: got %v, want 0.8", a.AnaerobicTE)
	}
	if a.VO2Max != 53.0 {
		t.Errorf("VO2Max: got %v, want 53.0", a.VO2Max)
	}
	if a.CadenceAvgSPM != 170 {
		t.Errorf("CadenceAvgSPM: got %v, want 170", a.CadenceAvgSPM)
	}
	if a.GroundContactTimeMS != 248 {
		t.Errorf("GroundContactTimeMS: got %v, want 248", a.GroundContactTimeMS)
	}
	if a.VerticalOscillationMM != 88 {
		t.Errorf("VerticalOscillationMM: got %v, want 88", a.VerticalOscillationMM)
	}
	if a.StrideLengthMM != 1180 {
		t.Errorf("StrideLengthMM: got %v, want 1180", a.StrideLengthMM)
	}
	if a.VerticalRatioPct != 7.4 {
		t.Errorf("VerticalRatioPct: got %v, want 7.4", a.VerticalRatioPct)
	}
	if a.AvgPowerW != 215 {
		t.Errorf("AvgPowerW: got %v, want 215", a.AvgPowerW)
	}
}

func TestActivityFrom_MissingFields(t *testing.T) {
	// Partial row — only required fields present; missing fields default to zero.
	row := map[string]any{
		"time":  "2026-07-06T10:30:00Z",
		"sport": "cycling",
	}
	a := garmin.ActivityFrom(row)
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
