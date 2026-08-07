package garmin

import "encoding/json"

// WorkoutDetail is one row from the "workout_detail" measurement — the step tree
// Garmin actually stored for an uploaded workout, captured verbatim from
// upload_workout's response by sync.py's _write_workout_detail. Steps is passed
// through as raw JSON (Garmin's own ExecutableStepDTO/RepeatGroupDTO shape) rather
// than remapped into WorkoutStep, since a round-trip check needs to see exactly what
// Garmin stored — including server-assigned fields like stepId that WorkoutStep has
// no field for.
type WorkoutDetail struct {
	WorkoutID int64           `json:"workout_id"`
	Name      string          `json:"name,omitempty"`
	Sport     string          `json:"sport,omitempty"`
	Steps     json.RawMessage `json:"steps"`
}

// WorkoutDetailFrom converts a query row from the "workout_detail" measurement.
func WorkoutDetailFrom(row map[string]any) WorkoutDetail {
	steps := stringFrom(row, "steps_json")
	if steps == "" {
		steps = "[]"
	}
	return WorkoutDetail{
		WorkoutID: int64FromString(row, "workout_id"),
		Name:      stringFrom(row, "name"),
		Sport:     stringFrom(row, "sport"),
		Steps:     json.RawMessage(steps),
	}
}
