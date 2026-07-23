package garmin

import "time"

// Lap represents one row from the "activity_lap" measurement.
type Lap struct {
	ActivityID     int64     `json:"activity_id"`
	LapIndex       int       `json:"lap_index"`
	Time           time.Time `json:"time"`
	DistanceM      float64   `json:"distance_m,omitempty"`
	DurationS      float64   `json:"duration_s,omitempty"`
	AvgHRBPM       float64   `json:"avg_hr_bpm,omitempty"`
	MaxHRBPM       float64   `json:"max_hr_bpm,omitempty"`
	AvgSpeedMpS    float64   `json:"avg_speed_m_s,omitempty"`
	AvgCadenceSPM  float64   `json:"avg_cadence_spm,omitempty"`
	AvgPowerW      float64   `json:"avg_power_w,omitempty"`
	ElevationGainM float64   `json:"elevation_gain_m,omitempty"`
}

// LapFrom converts a query row from the "activity_lap" measurement into a Lap.
// activity_id is stored as an InfluxDB tag and returned as a string; int64FromString handles that.
func LapFrom(row map[string]any) Lap {
	return Lap{
		ActivityID:     int64FromString(row, "activity_id"),
		LapIndex:       int(floatFrom(row, "lap_index")),
		Time:           timeFrom(row, "time"),
		DistanceM:      roundF(floatFrom(row, "distance_m")),
		DurationS:      roundF(floatFrom(row, "duration_s")),
		AvgHRBPM:       roundF(floatFrom(row, "avg_hr_bpm")),
		MaxHRBPM:       roundF(floatFrom(row, "max_hr_bpm")),
		AvgSpeedMpS:    roundF(floatFrom(row, "avg_speed_m_s")),
		AvgCadenceSPM:  roundF(floatFrom(row, "avg_cadence_spm")),
		AvgPowerW:      roundF(floatFrom(row, "avg_power_w")),
		ElevationGainM: roundF(floatFrom(row, "elevation_gain_m")),
	}
}
