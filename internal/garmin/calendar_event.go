package garmin

// CalendarEvent represents one row from the "calendar_event" measurement — a Garmin
// calendar event (itemType "event"), e.g. the target race an adaptive coach plan
// builds toward. Written by sync_calendar_events in sync.py.
//
// A personal event carries Garmin's own EventID. A shareable event the account
// subscribed to from Garmin's public event listing has no id — it's identified by
// ShareableEventUUID instead (EventID is 0). The same race can appear as both.
//
// DistanceM is derived from Garmin's completionTarget only for units verified live
// ("mile" on a personal event, "meter" on a shareable event); CompletionTargetValue/
// CompletionTargetUnit always carry the raw target so an unmapped unit is still
// visible. Garmin's calendar response has no goal time.
type CalendarEvent struct {
	EventID               int64   `json:"event_id,omitempty"`
	ShareableEventUUID    string  `json:"shareable_event_uuid,omitempty"`
	Date                  string  `json:"date"`
	Name                  string  `json:"name,omitempty"`
	ActivityTypeID        int64   `json:"activity_type_id,omitempty"`
	IsRace                bool    `json:"is_race"`
	PrimaryEvent          bool    `json:"primary_event"`
	StartTimeLocal        string  `json:"start_time_local,omitempty"`
	TimeZoneID            string  `json:"time_zone_id,omitempty"`
	CompletionTargetValue float64 `json:"completion_target_value,omitempty"`
	CompletionTargetUnit  string  `json:"completion_target_unit,omitempty"`
	DistanceM             float64 `json:"distance_m,omitempty"`
	ShareableEvent        bool    `json:"shareable_event"`
	Subscribed            bool    `json:"subscribed"`
	Location              string  `json:"location,omitempty"`
	URL                   string  `json:"url,omitempty"`
	// DeletedAt is the same internal tombstone marker as ScheduledWorkout's (#104):
	// set when an event was moved to another date or removed. queryCalendarEvents drops
	// any row with DeletedAt > 0.
	DeletedAt float64 `json:"-"`
}

// CalendarEventFrom converts a query row from the "calendar_event" measurement.
func CalendarEventFrom(row map[string]any) CalendarEvent {
	return CalendarEvent{
		EventID:            int64FromString(row, "event_id"),
		ShareableEventUUID: stringFrom(row, "shareable_event_uuid"),
		Date:               dateFrom(row, "time"),
		Name:               stringFrom(row, "name"),
		ActivityTypeID:     int64From(row, "activity_type_id"),
		IsRace:             floatFrom(row, "is_race") > 0.5,
		PrimaryEvent:       floatFrom(row, "primary_event") > 0.5,
		StartTimeLocal:     stringFrom(row, "start_time_local"),
		TimeZoneID:         stringFrom(row, "time_zone_id"),
		// Not roundF'd: Garmin's own 2dp value (e.g. 13.11 mi) would lose precision.
		CompletionTargetValue: floatFrom(row, "completion_target_value"),
		CompletionTargetUnit:  stringFrom(row, "completion_target_unit"),
		DistanceM:             roundF(floatFrom(row, "distance_m")),
		ShareableEvent:        floatFrom(row, "shareable_event") > 0.5,
		Subscribed:            floatFrom(row, "subscribed") > 0.5,
		Location:              stringFrom(row, "location"),
		URL:                   stringFrom(row, "url"),
		DeletedAt:             floatFrom(row, "deleted_at"),
	}
}
