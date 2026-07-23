package garmin

import "time"

// ActivityHRZones represents one row from the "activity_hr_zones" measurement.
// Each field is seconds spent in that heart rate zone during the activity.
type ActivityHRZones struct {
	ActivityID int64     `json:"activity_id"`
	Time       time.Time `json:"time"`
	Z1S        float64   `json:"z1_s,omitempty"`
	Z2S        float64   `json:"z2_s,omitempty"`
	Z3S        float64   `json:"z3_s,omitempty"`
	Z4S        float64   `json:"z4_s,omitempty"`
	Z5S        float64   `json:"z5_s,omitempty"`
}

// ActivityHRZonesFrom converts a query row from the "activity_hr_zones" measurement.
// activity_id is stored as an InfluxDB tag and returned as a string; int64FromString handles that.
func ActivityHRZonesFrom(row map[string]any) ActivityHRZones {
	return ActivityHRZones{
		ActivityID: int64FromString(row, "activity_id"),
		Time:       timeFrom(row, "time"),
		Z1S:        roundF(floatFrom(row, "z1_s")),
		Z2S:        roundF(floatFrom(row, "z2_s")),
		Z3S:        roundF(floatFrom(row, "z3_s")),
		Z4S:        roundF(floatFrom(row, "z4_s")),
		Z5S:        roundF(floatFrom(row, "z5_s")),
	}
}
