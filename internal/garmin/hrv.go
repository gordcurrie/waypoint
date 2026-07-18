package garmin

import "time"

// HRV represents one row from the "hrv" measurement.
// Status is numerically encoded: 2.0=BALANCED, 1.0=UNBALANCED, 0.0=POOR.
// Status is a pointer so nil (absent) can be distinguished from 0.0 (POOR).
type HRV struct {
	Time           time.Time
	WeeklyAvgMS    float64
	LastNightMS    float64
	Last5MinHighMS float64
	Status         *float64
}

// HRVFrom converts a query row from the "hrv" measurement.
func HRVFrom(row map[string]any) HRV {
	return HRV{
		Time:           timeFrom(row, "time"),
		WeeklyAvgMS:    floatFrom(row, "weekly_avg_ms"),
		LastNightMS:    floatFrom(row, "last_night_ms"),
		Last5MinHighMS: floatFrom(row, "last_5min_high_ms"),
		Status:         floatPtrFrom(row, "status"),
	}
}
