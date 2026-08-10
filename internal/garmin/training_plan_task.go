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
	// TargetType/TargetLo/TargetHi (#97) are the real prescribed HR-zone (bpm) or
	// pace-zone (m/s) range behind Description's flat summary — e.g. Description
	// "137bpm" is the midpoint of a real TargetLo=124/TargetHi=149 range, not a cap.
	// Empty/zero when the workout has no such target (e.g. strength).
	TargetType string  `json:"target_type,omitempty"`
	TargetLo   float64 `json:"target_lo,omitempty"`
	TargetHi   float64 `json:"target_hi,omitempty"`
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
		TargetType:  stringFrom(row, "target_type"),
		TargetLo:    roundF(floatFrom(row, "target_lo")),
		TargetHi:    roundF(floatFrom(row, "target_hi")),
	}
}
