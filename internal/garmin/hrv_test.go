package garmin

import "testing"

func TestHRVFrom(t *testing.T) {
	row := map[string]any{
		"time":              "2026-07-06T00:00:00Z",
		"weekly_avg_ms":     float64(58),
		"last_night_ms":     float64(61),
		"last_5min_high_ms": float64(72),
		"status":            float64(2), // BALANCED
	}
	h := HRVFrom(row)
	if h.WeeklyAvgMS != 58 {
		t.Errorf("WeeklyAvgMS: got %v, want 58", h.WeeklyAvgMS)
	}
	if h.LastNightMS != 61 {
		t.Errorf("LastNightMS: got %v, want 61", h.LastNightMS)
	}
	if h.Last5MinHighMS != 72 {
		t.Errorf("Last5MinHighMS: got %v, want 72", h.Last5MinHighMS)
	}
	if h.Status == nil || *h.Status != 2 {
		t.Errorf("Status: got %v, want 2 (BALANCED)", h.Status)
	}
}

func TestHRVFrom_StatusAbsent(t *testing.T) {
	row := map[string]any{
		"time":          "2026-07-06T00:00:00Z",
		"weekly_avg_ms": float64(45),
	}
	h := HRVFrom(row)
	if h.Status != nil {
		t.Errorf("Status: got %v, want nil for absent field", h.Status)
	}
}
