package garmin

import "time"

// Activity represents one row from the "activity" measurement.
// Running-specific fields (Cadence, GroundContactTime, etc.) are zero for non-running sports.
type Activity struct {
	Time         time.Time
	Sport        string // tag
	ActivityID   int64
	DistanceM    float64
	DurationS    float64
	AvgHRBPM     float64
	MaxHRBPM     float64
	CaloriesKcal float64
	ElevationGainM float64
	AvgSpeedMS   float64
	TrainingLoad float64
	AerobicTE    float64
	AnaerobicTE  float64
	VO2Max       float64
	// Running-specific
	CadenceAvgSPM         float64
	GroundContactTimeMS   float64
	VerticalOscillationMM float64
	StrideLengthMM        float64
	VerticalRatioPct      float64
	AvgPowerW             float64
}

// ActivityFrom converts a query row from the "activity" measurement into an Activity.
func ActivityFrom(row map[string]any) Activity {
	return Activity{
		Time:                  timeFrom(row, "time"),
		Sport:                 stringFrom(row, "sport"),
		ActivityID:            int64From(row, "activity_id"),
		DistanceM:             floatFrom(row, "distance_m"),
		DurationS:             floatFrom(row, "duration_s"),
		AvgHRBPM:              floatFrom(row, "avg_hr_bpm"),
		MaxHRBPM:              floatFrom(row, "max_hr_bpm"),
		CaloriesKcal:          floatFrom(row, "calories_kcal"),
		ElevationGainM:        floatFrom(row, "elevation_gain_m"),
		AvgSpeedMS:            floatFrom(row, "avg_speed_m_s"),
		TrainingLoad:          floatFrom(row, "training_load"),
		AerobicTE:             floatFrom(row, "aerobic_te"),
		AnaerobicTE:           floatFrom(row, "anaerobic_te"),
		VO2Max:                floatFrom(row, "vo2max"),
		CadenceAvgSPM:         floatFrom(row, "cadence_avg_spm"),
		GroundContactTimeMS:   floatFrom(row, "ground_contact_time_ms"),
		VerticalOscillationMM: floatFrom(row, "vertical_oscillation_mm"),
		StrideLengthMM:        floatFrom(row, "stride_length_mm"),
		VerticalRatioPct:      floatFrom(row, "vertical_ratio_pct"),
		AvgPowerW:             floatFrom(row, "avg_power_w"),
	}
}
