package garmin

// ScheduledWorkout represents one row from the "scheduled_workout" measurement.
type ScheduledWorkout struct {
	ScheduledID int64   `json:"scheduled_id"`
	WorkoutID   int64   `json:"workout_id,omitempty"`
	Date        string  `json:"date"`
	Name        string  `json:"name,omitempty"`
	Sport       string  `json:"sport,omitempty"`
	DurationS   float64 `json:"duration_s,omitempty"`
}

// ScheduledWorkoutFrom converts a query row from the "scheduled_workout" measurement.
func ScheduledWorkoutFrom(row map[string]any) ScheduledWorkout {
	return ScheduledWorkout{
		ScheduledID: int64FromString(row, "scheduled_id"),
		WorkoutID:   int64From(row, "workout_id"),
		Date:        dateFrom(row, "time"),
		Name:        stringFrom(row, "name"),
		Sport:       stringFrom(row, "sport"),
		DurationS:   roundF(floatFrom(row, "duration_s")),
	}
}
