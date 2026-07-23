package influx

// Measurement names used when querying or writing InfluxDB.
// The first 9 constants match what sync/sync.py writes.
// MeasurementTrainingLoad is written by the MCP server's get_training_load tool
// (computed from activity data on demand) and is not written by the Python sync.
const (
	MeasurementActivity           = "activity"
	MeasurementDailyStats         = "daily_stats"
	MeasurementSleep              = "sleep"
	MeasurementHRV                = "hrv"
	MeasurementTrainingReadiness  = "training_readiness"
	MeasurementTrainingStatus     = "training_status"
	MeasurementPerformance        = "performance"
	MeasurementLactateThreshold   = "lactate_threshold"
	MeasurementRespiration        = "respiration"
	MeasurementTrainingLoad       = "training_load"
	MeasurementActivityLap        = "activity_lap"
	MeasurementActivityHRZones    = "activity_hr_zones"
	MeasurementScheduledWorkout   = "scheduled_workout"
)
