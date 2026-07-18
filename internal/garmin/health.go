package garmin

import "time"

// DailyStats represents one row from the "daily_stats" measurement.
type DailyStats struct {
	Time                 time.Time
	Steps                float64
	RestingHRBPM         float64
	BodyBatteryMax       float64
	BodyBatteryMin       float64
	StressAvg            float64
	ActiveCalories       float64
	TotalCalories        float64
	FloorsAscended       float64
	VigorousIntensityMin float64
	ModerateIntensityMin float64
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
	Time             time.Time
	TotalSleepS      float64
	DeepSleepS       float64
	LightSleepS      float64
	REMSleepS        float64
	AwakeS           float64
	SleepScore       float64
	AvgHRVMS         float64
	AvgSpO2Pct       float64
	AvgBreathingRate float64
	AvgStress        float64
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
	Time          time.Time
	AvgWakingBRPM float64
	AvgSleepBRPM  float64
	HighestBRPM   float64
	LowestBRPM    float64
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
