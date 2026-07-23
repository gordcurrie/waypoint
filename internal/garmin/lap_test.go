package garmin_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestLapFrom(t *testing.T) {
	row := map[string]any{
		"activity_id":     "1234567890123456",
		"lap_index":       float64(2),
		"time":            "2026-07-20T08:05:00Z",
		"distance_m":      float64(1000),
		"duration_s":      float64(360),
		"avg_hr_bpm":      float64(148),
		"max_hr_bpm":      float64(162),
		"avg_speed_m_s":   float64(2.78),
		"avg_cadence_spm": float64(172),
		"avg_power_w":     float64(220),
		"elevation_gain_m": float64(12),
	}

	lap := garmin.LapFrom(row)

	if lap.ActivityID != 1234567890123456 {
		t.Errorf("ActivityID: got %d, want 1234567890123456", lap.ActivityID)
	}
	if lap.LapIndex != 2 {
		t.Errorf("LapIndex: got %d, want 2", lap.LapIndex)
	}
	want := time.Date(2026, 7, 20, 8, 5, 0, 0, time.UTC)
	if !lap.Time.Equal(want) {
		t.Errorf("Time: got %v, want %v", lap.Time, want)
	}
	if lap.DistanceM != 1000 {
		t.Errorf("DistanceM: got %v, want 1000", lap.DistanceM)
	}
	if lap.DurationS != 360 {
		t.Errorf("DurationS: got %v, want 360", lap.DurationS)
	}
	if lap.AvgHRBPM != 148 {
		t.Errorf("AvgHRBPM: got %v, want 148", lap.AvgHRBPM)
	}
	if lap.MaxHRBPM != 162 {
		t.Errorf("MaxHRBPM: got %v, want 162", lap.MaxHRBPM)
	}
	if lap.AvgSpeedMpS != 2.8 {
		t.Errorf("AvgSpeedMpS: got %v, want 2.8", lap.AvgSpeedMpS)
	}
	if lap.AvgCadenceSPM != 172 {
		t.Errorf("AvgCadenceSPM: got %v, want 172", lap.AvgCadenceSPM)
	}
	if lap.AvgPowerW != 220 {
		t.Errorf("AvgPowerW: got %v, want 220", lap.AvgPowerW)
	}
	if lap.ElevationGainM != 12 {
		t.Errorf("ElevationGainM: got %v, want 12", lap.ElevationGainM)
	}
}

func TestLapFrom_ActivityIDAsNumericTag(t *testing.T) {
	// InfluxDB may return tags as numeric in some query paths
	row := map[string]any{
		"activity_id": float64(9876543210),
		"time":        "2026-07-20T08:00:00Z",
	}
	lap := garmin.LapFrom(row)
	if lap.ActivityID != 9876543210 {
		t.Errorf("ActivityID from numeric: got %d, want 9876543210", lap.ActivityID)
	}
}

func TestLapFrom_OptionalFieldsOmittedWhenZero(t *testing.T) {
	row := map[string]any{
		"activity_id": "111",
		"lap_index":   float64(1),
		"time":        "2026-07-20T08:00:00Z",
		// distance, duration present; optional biomechanics absent
		"distance_m": float64(500),
		"duration_s": float64(180),
	}
	b, err := json.Marshal(garmin.LapFrom(row))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, absent := range []string{"avg_hr_bpm", "avg_speed_m_s", "avg_cadence_spm", "avg_power_w"} {
		if strings.Contains(s, absent) {
			t.Errorf("JSON should omit %q when zero, got: %s", absent, s)
		}
	}
}
