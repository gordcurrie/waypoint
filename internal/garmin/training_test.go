package garmin

import "testing"

func TestTrainingReadinessFrom(t *testing.T) {
	row := map[string]any{
		"time":            "2026-07-06T00:00:00Z",
		"score":           float64(74),
		"hrv_status":      float64(2),
		"sleep_score":     float64(78),
		"recovery_time_h": float64(16),
		"acw_ratio":       float64(0.85),
	}
	tr := TrainingReadinessFrom(row)
	if tr.Score != 74 {
		t.Errorf("Score: got %v, want 74", tr.Score)
	}
	if tr.ACWRatio != 0.85 {
		t.Errorf("ACWRatio: got %v, want 0.85", tr.ACWRatio)
	}
}

func TestTrainingStatusFrom(t *testing.T) {
	row := map[string]any{
		"time":           "2026-07-06T00:00:00Z",
		"status_num":     float64(3), // productive
		"vo2max_running": float64(52.4),
		"fitness_age":    float64(32),
	}
	ts := TrainingStatusFrom(row)
	if ts.StatusNum != 3 {
		t.Errorf("StatusNum: got %v, want 3 (productive)", ts.StatusNum)
	}
	if ts.VO2MaxRunning != 52.4 {
		t.Errorf("VO2MaxRunning: got %v, want 52.4", ts.VO2MaxRunning)
	}
}

func TestLactateThresholdFrom(t *testing.T) {
	row := map[string]any{
		"time":              "2026-07-06T00:00:00Z",
		"lt_hr_bpm":         float64(168),
		"lt_pace_s_per_km":  float64(272),
	}
	lt := LactateThresholdFrom(row)
	if lt.LTHeartRate != 168 {
		t.Errorf("LTHeartRate: got %v, want 168", lt.LTHeartRate)
	}
	if lt.LTPaceSPerKM != 272 {
		t.Errorf("LTPaceSPerKM: got %v, want 272", lt.LTPaceSPerKM)
	}
}

func TestTrainingLoadFrom(t *testing.T) {
	row := map[string]any{
		"time":      "2026-07-06T00:00:00Z",
		"atl_7day":  float64(65.2),
		"ctl_42day": float64(58.8),
		"tsb":       float64(-6.4),
	}
	tl := TrainingLoadFrom(row)
	if tl.ATL != 65.2 {
		t.Errorf("ATL: got %v, want 65.2", tl.ATL)
	}
	if tl.CTL != 58.8 {
		t.Errorf("CTL: got %v, want 58.8", tl.CTL)
	}
	if tl.TSB != -6.4 {
		t.Errorf("TSB: got %v, want -6.4", tl.TSB)
	}
}
