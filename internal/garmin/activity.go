package garmin

import "time"

// Activity represents one row from the "activity" measurement.
// Running-specific fields (Cadence, GroundContactTime, etc.) are zero for non-running sports.
type Activity struct {
	Time           time.Time `json:"time"`
	Sport          string    `json:"sport"` // tag
	ActivityID     int64     `json:"activity_id"`
	DistanceM      float64   `json:"distance_m"`
	DurationS      float64   `json:"duration_s"`
	AvgHRBPM       float64   `json:"avg_hr_bpm"`
	MaxHRBPM       float64   `json:"max_hr_bpm"`
	CaloriesKcal   float64   `json:"calories_kcal"`
	ElevationGainM float64   `json:"elevation_gain_m"`
	AvgSpeedMpS    float64   `json:"avg_speed_m_s"`
	TrainingLoad   float64   `json:"training_load"`
	AerobicTE      float64   `json:"aerobic_te"`
	AnaerobicTE    float64   `json:"anaerobic_te"`
	VO2Max         float64   `json:"vo2max"`
	// Running-specific
	CadenceAvgSPM         float64 `json:"cadence_avg_spm"`
	GroundContactTimeMS   float64 `json:"ground_contact_time_ms"`
	VerticalOscillationMM float64 `json:"vertical_oscillation_mm"`
	StrideLengthMM        float64 `json:"stride_length_mm"`
	VerticalRatioPct      float64 `json:"vertical_ratio_pct"`
	AvgPowerW             float64 `json:"avg_power_w"`
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
		AvgSpeedMpS:           floatFrom(row, "avg_speed_m_s"),
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
