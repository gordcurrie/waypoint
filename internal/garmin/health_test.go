package garmin_test

import (
	"testing"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestDailyStatsFrom(t *testing.T) {
	row := map[string]any{
		"time":             "2026-07-06T00:00:00Z",
		"steps":            float64(8500),
		"resting_hr_bpm":   float64(52),
		"body_battery_max": float64(95),
		"body_battery_min": float64(20),
		"stress_avg":       float64(28),
	}
	s := garmin.DailyStatsFrom(row)
	if s.Steps != 8500 {
		t.Errorf("Steps: got %v, want 8500", s.Steps)
	}
	if s.RestingHRBPM != 52 {
		t.Errorf("RestingHRBPM: got %v, want 52", s.RestingHRBPM)
	}
	if s.TotalCalories != 0 {
		t.Errorf("TotalCalories: got %v, want 0 for missing field", s.TotalCalories)
	}
}

func TestSleepFrom(t *testing.T) {
	row := map[string]any{
		"time":          "2026-07-06T00:00:00Z",
		"total_sleep_s": float64(27000),
		"deep_sleep_s":  float64(5400),
		"sleep_score":   float64(78),
		"avg_hrv_ms":    float64(62),
	}
	s := garmin.SleepFrom(row)
	if s.TotalSleepS != 27000 {
		t.Errorf("TotalSleepS: got %v, want 27000", s.TotalSleepS)
	}
	if s.SleepScore != 78 {
		t.Errorf("SleepScore: got %v, want 78", s.SleepScore)
	}
	if s.REMSleepS != 0 {
		t.Errorf("REMSleepS: got %v, want 0 for missing field", s.REMSleepS)
	}
}

func TestRespirationFrom(t *testing.T) {
	row := map[string]any{
		"time":            "2026-07-06T00:00:00Z",
		"avg_waking_brpm": float64(14.5),
		"avg_sleep_brpm":  float64(13.2),
		"highest_brpm":    float64(18),
		"lowest_brpm":     float64(11),
	}
	r := garmin.RespirationFrom(row)
	if r.AvgWakingBRPM != 14.5 {
		t.Errorf("AvgWakingBRPM: got %v, want 14.5", r.AvgWakingBRPM)
	}
	if r.LowestBRPM != 11 {
		t.Errorf("LowestBRPM: got %v, want 11", r.LowestBRPM)
	}
}
