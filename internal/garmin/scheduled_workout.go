package garmin

// ScheduledWorkout represents one row from the "scheduled_workout" measurement,
// optionally enriched with per-day detail from the active adaptive coach training
// plan (see mergeTrainingPlanDetail in tools/workouts.go). ScheduledID is 0 for a
// rest day synthesized purely from the training plan — there's no real calendar
// item behind it, only a plan entry.
type ScheduledWorkout struct {
	ScheduledID int64   `json:"scheduled_id"`
	WorkoutID   int64   `json:"workout_id,omitempty"`
	Date        string  `json:"date"`
	Name        string  `json:"name,omitempty"`
	Sport       string  `json:"sport,omitempty"`
	DurationS   float64 `json:"duration_s,omitempty"`
	DistanceM   float64 `json:"distance_m,omitempty"`
	Description string  `json:"description,omitempty"`
	RestDay     bool    `json:"rest_day,omitempty"`
	Phase       string  `json:"phase,omitempty"`
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
