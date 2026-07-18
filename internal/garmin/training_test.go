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
	if tr.HRVStatus != 2 {
		t.Errorf("HRVStatus: got %v, want 2", tr.HRVStatus)
	}
	if tr.SleepScore != 78 {
		t.Errorf("SleepScore: got %v, want 78", tr.SleepScore)
	}
	if tr.RecoveryTimeH != 16 {
		t.Errorf("RecoveryTimeH: got %v, want 16", tr.RecoveryTimeH)
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
		"vo2max_cycling": float64(49.1),
		"fitness_age":    float64(32),
	}
	ts := TrainingStatusFrom(row)
	if ts.StatusNum == nil || *ts.StatusNum != 3 {
		t.Errorf("StatusNum: got %v, want 3 (productive)", ts.StatusNum)
	}
	if ts.VO2MaxRunning != 52.4 {
		t.Errorf("VO2MaxRunning: got %v, want 52.4", ts.VO2MaxRunning)
	}
	if ts.VO2MaxCycling != 49.1 {
		t.Errorf("VO2MaxCycling: got %v, want 49.1", ts.VO2MaxCycling)
	}
	if ts.FitnessAge != 32 {
		t.Errorf("FitnessAge: got %v, want 32", ts.FitnessAge)
	}
}

func TestTrainingStatusFrom_StatusNumAbsent(t *testing.T) {
	row := map[string]any{"time": "2026-07-06T00:00:00Z"}
	ts := TrainingStatusFrom(row)
	if ts.StatusNum != nil {
		t.Errorf("StatusNum: got %v, want nil for absent field", ts.StatusNum)
	}
}

func TestPerformanceFrom(t *testing.T) {
	row := map[string]any{
		"time":        "2026-07-06T00:00:00Z",
		"vo2max":      float64(53.2),
		"fitness_age": float64(31),
	}
	p := PerformanceFrom(row)
	if p.VO2Max != 53.2 {
		t.Errorf("VO2Max: got %v, want 53.2", p.VO2Max)
	}
	if p.FitnessAge != 31 {
		t.Errorf("FitnessAge: got %v, want 31", p.FitnessAge)
	}
}

func TestLactateThresholdFrom(t *testing.T) {
	row := map[string]any{
		"time":             "2026-07-06T00:00:00Z",
		"lt_hr_bpm":        float64(168),
		"lt_pace_s_per_km": float64(272),
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
