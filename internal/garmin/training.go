package garmin

import "time"

// TrainingReadiness represents one row from the "training_readiness" measurement.
type TrainingReadiness struct {
	Time          time.Time
	Score         float64
	HRVStatus     float64
	SleepScore    float64
	RecoveryTimeH float64
	ACWRatio      float64
}

// TrainingReadinessFrom converts a query row from the "training_readiness" measurement.
func TrainingReadinessFrom(row map[string]any) TrainingReadiness {
	return TrainingReadiness{
		Time:          timeFrom(row, "time"),
		Score:         floatFrom(row, "score"),
		HRVStatus:     floatFrom(row, "hrv_status"),
		SleepScore:    floatFrom(row, "sleep_score"),
		RecoveryTimeH: floatFrom(row, "recovery_time_h"),
		ACWRatio:      floatFrom(row, "acw_ratio"),
	}
}

// TrainingStatus represents one row from the "training_status" measurement.
// StatusNum encodes the label: 5.0=peaking, 4.0=maintaining, 3.0=productive,
// 2.0=recovery, 1.0=detraining, 0.0=overreaching.
// StatusNum is a pointer so nil (absent) can be distinguished from 0.0 (overreaching).
type TrainingStatus struct {
	Time          time.Time
	StatusNum     *float64
	VO2MaxRunning float64
	VO2MaxCycling float64
	FitnessAge    float64
}

// TrainingStatusFrom converts a query row from the "training_status" measurement.
func TrainingStatusFrom(row map[string]any) TrainingStatus {
	return TrainingStatus{
		Time:          timeFrom(row, "time"),
		StatusNum:     floatPtrFrom(row, "status_num"),
		VO2MaxRunning: floatFrom(row, "vo2max_running"),
		VO2MaxCycling: floatFrom(row, "vo2max_cycling"),
		FitnessAge:    floatFrom(row, "fitness_age"),
	}
}

// Performance represents one row from the "performance" measurement.
type Performance struct {
	Time       time.Time
	VO2Max     float64
	FitnessAge float64
}

// PerformanceFrom converts a query row from the "performance" measurement.
func PerformanceFrom(row map[string]any) Performance {
	return Performance{
		Time:       timeFrom(row, "time"),
		VO2Max:     floatFrom(row, "vo2max"),
		FitnessAge: floatFrom(row, "fitness_age"),
	}
}

// LactateThreshold represents one row from the "lactate_threshold" measurement.
type LactateThreshold struct {
	Time         time.Time
	LTHeartRate  float64 // lt_hr_bpm
	LTPaceSPerKM float64 // lt_pace_s_per_km
}

// LactateThresholdFrom converts a query row from the "lactate_threshold" measurement.
func LactateThresholdFrom(row map[string]any) LactateThreshold {
	return LactateThreshold{
		Time:         timeFrom(row, "time"),
		LTHeartRate:  floatFrom(row, "lt_hr_bpm"),
		LTPaceSPerKM: floatFrom(row, "lt_pace_s_per_km"),
	}
}

// TrainingLoad represents one row from the "training_load" measurement.
// Written by the Go MCP server on demand, not by the Python sync sidecar.
// ATL = 7-day EMA of training load, CTL = 42-day EMA, TSB = CTL - ATL.
type TrainingLoad struct {
	Time time.Time
	ATL  float64 // atl_7day
	CTL  float64 // ctl_42day
	TSB  float64 // tsb
}

// TrainingLoadFrom converts a query row from the "training_load" measurement.
func TrainingLoadFrom(row map[string]any) TrainingLoad {
	return TrainingLoad{
		Time: timeFrom(row, "time"),
		ATL:  floatFrom(row, "atl_7day"),
		CTL:  floatFrom(row, "ctl_42day"),
		TSB:  floatFrom(row, "tsb"),
	}
}
