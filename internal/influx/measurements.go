package influx

// Measurement names — must match what sync/sync.py writes.
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
)
