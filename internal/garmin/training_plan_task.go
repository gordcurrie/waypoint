package garmin

// TrainingPlanTask represents one row from the "training_plan_task" measurement —
// per-day detail from the active adaptive coach training plan (duration, distance,
// pace/HR target, rest-day flag, training phase). Merged into ScheduledWorkout by
// mergeTrainingPlanDetail rather than exposed as its own tool result type.
type TrainingPlanTask struct {
	Date        string  `json:"date"`
	Sport       string  `json:"sport,omitempty"`
	Name        string  `json:"name,omitempty"`
	Description string  `json:"description,omitempty"`
	DurationS   float64 `json:"duration_s,omitempty"`
	DistanceM   float64 `json:"distance_m,omitempty"`
	RestDay     bool    `json:"rest_day,omitempty"`
	Phase       string  `json:"phase,omitempty"`
}

// TrainingPlanTaskFrom converts a query row from the "training_plan_task" measurement.
func TrainingPlanTaskFrom(row map[string]any) TrainingPlanTask {
	return TrainingPlanTask{
		Date:        dateFrom(row, "time"),
		Sport:       stringFrom(row, "sport"),
		Name:        stringFrom(row, "name"),
		Description: stringFrom(row, "description"),
		DurationS:   roundF(floatFrom(row, "duration_s")),
		DistanceM:   roundF(floatFrom(row, "distance_m")),
		RestDay:     floatFrom(row, "rest_day") > 0.5,
		Phase:       stringFrom(row, "phase"),
	}
}
