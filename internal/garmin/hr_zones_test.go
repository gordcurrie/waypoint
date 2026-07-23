package garmin_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/garmin"
)

func TestActivityHRZonesFrom(t *testing.T) {
	row := map[string]any{
		"activity_id": "1234567890123456",
		"time":        "2026-07-20T08:00:00Z",
		"z1_s":        float64(1200),
		"z2_s":        float64(2400),
		"z3_s":        float64(900),
		"z4_s":        float64(300),
		"z5_s":        float64(60),
	}

	z := garmin.ActivityHRZonesFrom(row)

	if z.ActivityID != 1234567890123456 {
		t.Errorf("ActivityID: got %d, want 1234567890123456", z.ActivityID)
	}
	want := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	if !z.Time.Equal(want) {
		t.Errorf("Time: got %v, want %v", z.Time, want)
	}
	if z.Z1S != 1200 {
		t.Errorf("Z1S: got %v, want 1200", z.Z1S)
	}
	if z.Z2S != 2400 {
		t.Errorf("Z2S: got %v, want 2400", z.Z2S)
	}
	if z.Z3S != 900 {
		t.Errorf("Z3S: got %v, want 900", z.Z3S)
	}
	if z.Z4S != 300 {
		t.Errorf("Z4S: got %v, want 300", z.Z4S)
	}
	if z.Z5S != 60 {
		t.Errorf("Z5S: got %v, want 60", z.Z5S)
	}
}

func TestActivityHRZonesFrom_AbsentZonesOmitted(t *testing.T) {
	row := map[string]any{
		"activity_id": "999",
		"time":        "2026-07-20T08:00:00Z",
		"z1_s":        float64(3000),
		"z2_s":        float64(1000),
		// z3-z5 absent (e.g. cycling with no high-HR zones)
	}
	b, err := json.Marshal(garmin.ActivityHRZonesFrom(row))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, absent := range []string{"z3_s", "z4_s", "z5_s"} {
		if strings.Contains(s, absent) {
			t.Errorf("JSON should omit %q when zero, got: %s", absent, s)
		}
	}
}
