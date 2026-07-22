package garmin

// TrainingReadiness represents one row from the "training_readiness" measurement.
type TrainingReadiness struct {
	Date          string  `json:"date"`
	Score         float64 `json:"score"`
	HRVStatus     float64 `json:"hrv_status"`
	SleepScore    float64 `json:"sleep_score"`
	RecoveryTimeH float64 `json:"recovery_time_h"`
	ACWRatio      float64 `json:"acw_ratio"`
}

// TrainingReadinessFrom converts a query row from the "training_readiness" measurement.
func TrainingReadinessFrom(row map[string]any) TrainingReadiness {
	return TrainingReadiness{
		Date:          timeFrom(row, "time").Format("2006-01-02"),
		Score:         roundF(floatFrom(row, "score")),
		HRVStatus:     roundF(floatFrom(row, "hrv_status")),
		SleepScore:    roundF(floatFrom(row, "sleep_score")),
		RecoveryTimeH: roundF(floatFrom(row, "recovery_time_h")),
		ACWRatio:      roundF(floatFrom(row, "acw_ratio")),
	}
}

// TrainingStatus represents one row from the "training_status" measurement.
// StatusNum encodes the label: 5.0=peaking, 4.0=maintaining, 3.0=productive,
// 2.0=recovery, 1.0=detraining, 0.0=overreaching.
// StatusNum is a pointer so nil (absent) can be distinguished from 0.0 (overreaching).
type TrainingStatus struct {
	Date          string   `json:"date"`
	StatusNum     *float64 `json:"status_num,omitempty"`
	VO2MaxRunning float64  `json:"vo2max_running"`
	VO2MaxCycling float64  `json:"vo2max_cycling"`
	FitnessAge    float64  `json:"fitness_age"`
}

// TrainingStatusFrom converts a query row from the "training_status" measurement.
func TrainingStatusFrom(row map[string]any) TrainingStatus {
	return TrainingStatus{
		Date:          timeFrom(row, "time").Format("2006-01-02"),
		StatusNum:     roundFPtr(floatPtrFrom(row, "status_num")),
		VO2MaxRunning: roundF(floatFrom(row, "vo2max_running")),
		VO2MaxCycling: roundF(floatFrom(row, "vo2max_cycling")),
		FitnessAge:    roundF(floatFrom(row, "fitness_age")),
	}
}

// Performance represents one row from the "performance" measurement.
type Performance struct {
	Date       string  `json:"date"`
	VO2Max     float64 `json:"vo2max"`
	FitnessAge float64 `json:"fitness_age"`
}

// PerformanceFrom converts a query row from the "performance" measurement.
func PerformanceFrom(row map[string]any) Performance {
	return Performance{
		Date:       timeFrom(row, "time").Format("2006-01-02"),
		VO2Max:     roundF(floatFrom(row, "vo2max")),
		FitnessAge: roundF(floatFrom(row, "fitness_age")),
	}
}

// LactateThreshold represents one row from the "lactate_threshold" measurement.
type LactateThreshold struct {
	Date         string  `json:"date"`
	LTHeartRate  float64 `json:"lt_hr_bpm"`
	LTPaceSPerKM float64 `json:"lt_pace_s_per_km"`
}

// LactateThresholdFrom converts a query row from the "lactate_threshold" measurement.
func LactateThresholdFrom(row map[string]any) LactateThreshold {
	return LactateThreshold{
		Date:         timeFrom(row, "time").Format("2006-01-02"),
		LTHeartRate:  roundF(floatFrom(row, "lt_hr_bpm")),
		LTPaceSPerKM: roundF(floatFrom(row, "lt_pace_s_per_km")),
	}
}

// TrainingLoad represents one row from the "training_load" measurement.
// Written by the Go MCP server on demand, not by the Python sync sidecar.
// ATL = 7-day EMA of training load, CTL = 42-day EMA, TSB = CTL - ATL.
type TrainingLoad struct {
	Date string  `json:"date"`
	ATL  float64 `json:"atl_7day"`
	CTL  float64 `json:"ctl_42day"`
	TSB  float64 `json:"tsb"`
}

// TrainingLoadFrom converts a query row from the "training_load" measurement.
func TrainingLoadFrom(row map[string]any) TrainingLoad {
	return TrainingLoad{
		Date: timeFrom(row, "time").Format("2006-01-02"),
		ATL:  roundF(floatFrom(row, "atl_7day")),
		CTL:  roundF(floatFrom(row, "ctl_42day")),
		TSB:  roundF(floatFrom(row, "tsb")),
	}
}
