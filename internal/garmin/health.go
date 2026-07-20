package garmin

import "time"

// DailyStats represents one row from the "daily_stats" measurement.
type DailyStats struct {
	Time                 time.Time `json:"time"`
	Steps                float64   `json:"steps"`
	RestingHRBPM         float64   `json:"resting_hr_bpm"`
	BodyBatteryMax       float64   `json:"body_battery_max"`
	BodyBatteryMin       float64   `json:"body_battery_min"`
	StressAvg            float64   `json:"stress_avg"`
	ActiveCalories       float64   `json:"active_calories"`
	TotalCalories        float64   `json:"total_calories"`
	FloorsAscended       float64   `json:"floors_ascended"`
	VigorousIntensityMin float64   `json:"vigorous_intensity_min"`
	ModerateIntensityMin float64   `json:"moderate_intensity_min"`
}

// DailyStatsFrom converts a query row from the "daily_stats" measurement.
func DailyStatsFrom(row map[string]any) DailyStats {
	return DailyStats{
		Time:                 timeFrom(row, "time"),
		Steps:                floatFrom(row, "steps"),
		RestingHRBPM:         floatFrom(row, "resting_hr_bpm"),
		BodyBatteryMax:       floatFrom(row, "body_battery_max"),
		BodyBatteryMin:       floatFrom(row, "body_battery_min"),
		StressAvg:            floatFrom(row, "stress_avg"),
		ActiveCalories:       floatFrom(row, "active_calories"),
		TotalCalories:        floatFrom(row, "total_calories"),
		FloorsAscended:       floatFrom(row, "floors_ascended"),
		VigorousIntensityMin: floatFrom(row, "vigorous_intensity_min"),
		ModerateIntensityMin: floatFrom(row, "moderate_intensity_min"),
	}
}

// Sleep represents one row from the "sleep" measurement.
type Sleep struct {
	Time             time.Time `json:"time"`
	TotalSleepS      float64   `json:"total_sleep_s"`
	DeepSleepS       float64   `json:"deep_sleep_s"`
	LightSleepS      float64   `json:"light_sleep_s"`
	REMSleepS        float64   `json:"rem_sleep_s"`
	AwakeS           float64   `json:"awake_s"`
	SleepScore       float64   `json:"sleep_score"`
	AvgHRVMS         float64   `json:"avg_hrv_ms"`
	AvgSpO2Pct       float64   `json:"avg_spo2_pct"`
	AvgBreathingRate float64   `json:"avg_breathing_rate"`
	AvgStress        float64   `json:"avg_stress"`
}

// SleepFrom converts a query row from the "sleep" measurement.
func SleepFrom(row map[string]any) Sleep {
	return Sleep{
		Time:             timeFrom(row, "time"),
		TotalSleepS:      floatFrom(row, "total_sleep_s"),
		DeepSleepS:       floatFrom(row, "deep_sleep_s"),
		LightSleepS:      floatFrom(row, "light_sleep_s"),
		REMSleepS:        floatFrom(row, "rem_sleep_s"),
		AwakeS:           floatFrom(row, "awake_s"),
		SleepScore:       floatFrom(row, "sleep_score"),
		AvgHRVMS:         floatFrom(row, "avg_hrv_ms"),
		AvgSpO2Pct:       floatFrom(row, "avg_spo2_pct"),
		AvgBreathingRate: floatFrom(row, "avg_breathing_rate"),
		AvgStress:        floatFrom(row, "avg_stress"),
	}
}

// Respiration represents one row from the "respiration" measurement.
type Respiration struct {
	Time          time.Time `json:"time"`
	AvgWakingBRPM float64   `json:"avg_waking_brpm"`
	AvgSleepBRPM  float64   `json:"avg_sleep_brpm"`
	HighestBRPM   float64   `json:"highest_brpm"`
	LowestBRPM    float64   `json:"lowest_brpm"`
}

// RespirationFrom converts a query row from the "respiration" measurement.
func RespirationFrom(row map[string]any) Respiration {
	return Respiration{
		Time:          timeFrom(row, "time"),
		AvgWakingBRPM: floatFrom(row, "avg_waking_brpm"),
		AvgSleepBRPM:  floatFrom(row, "avg_sleep_brpm"),
		HighestBRPM:   floatFrom(row, "highest_brpm"),
		LowestBRPM:    floatFrom(row, "lowest_brpm"),
	}
}
