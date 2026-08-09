package garmin

import "time"

// ExerciseSet represents one row from the "activity_exercise_set" measurement —
// one set (ACTIVE or REST) within a strength_training activity (#42).
type ExerciseSet struct {
	ActivityID   int64     `json:"activity_id"`
	SetIndex     int       `json:"set_index"`
	Time         time.Time `json:"time"`
	Category     string    `json:"category,omitempty"`
	ExerciseName string    `json:"exercise_name,omitempty"`
	DurationS    float64   `json:"duration_s,omitempty"`
	Reps         float64   `json:"reps,omitempty"`
	WeightKg     float64   `json:"weight_kg,omitempty"`
	SetType      string    `json:"set_type,omitempty"`
}

// ExerciseSetFrom converts a query row from the "activity_exercise_set" measurement.
// activity_id and set_index are stored as InfluxDB tags and returned as strings;
// int64FromString handles that.
func ExerciseSetFrom(row map[string]any) ExerciseSet {
	return ExerciseSet{
		ActivityID:   int64FromString(row, "activity_id"),
		SetIndex:     int(int64FromString(row, "set_index")),
		Time:         timeFrom(row, "time"),
		Category:     stringFrom(row, "category"),
		ExerciseName: stringFrom(row, "exercise_name"),
		DurationS:    roundF(floatFrom(row, "duration_s")),
		Reps:         roundF(floatFrom(row, "reps")),
		WeightKg:     roundF(floatFrom(row, "weight_kg")),
		SetType:      stringFrom(row, "set_type"),
	}
}
