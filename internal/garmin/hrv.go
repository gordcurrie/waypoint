package garmin

// HRV represents one row from the "hrv" measurement.
// Status is numerically encoded: 2.0=BALANCED, 1.0=UNBALANCED, 0.0=POOR.
// Status is a pointer so nil (absent) can be distinguished from 0.0 (POOR).
type HRV struct {
	Date           string   `json:"date"`
	WeeklyAvgMS    float64  `json:"weekly_avg_ms"`
	LastNightMS    float64  `json:"last_night_ms"`
	Last5MinHighMS float64  `json:"last_5min_high_ms"`
	Status         *float64 `json:"status,omitempty"`
}

// HRVFrom converts a query row from the "hrv" measurement.
func HRVFrom(row map[string]any) HRV {
	return HRV{
		Date:           timeFrom(row, "time").Format("2006-01-02"),
		WeeklyAvgMS:    roundF(floatFrom(row, "weekly_avg_ms")),
		LastNightMS:    roundF(floatFrom(row, "last_night_ms")),
		Last5MinHighMS: roundF(floatFrom(row, "last_5min_high_ms")),
		Status:         roundFPtr(floatPtrFrom(row, "status")),
	}
}
