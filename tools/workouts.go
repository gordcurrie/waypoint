package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/garmin/exercises"
	"github.com/gordcurrie/waypoint/internal/influx"
)

// WorkoutStep is a single step in a structured workout.
//
// A strength exercise step sets Category/ExerciseName (a validated pair from Garmin's
// exercise catalog — see search_exercises) and Reps as its end condition. Sets/RestS
// turn the step into a repeated set: sync.py wraps it in a Garmin RepeatGroupDTO with
// a synthesized rest step between each set/iteration, rather than the caller listing out
// each set and rest individually. WeightKg sets a weight target, requires Category/ExerciseName
// (verified live 2026-08-09: Garmin resolves a bare {"unitKey":"kilogram"} weightUnit
// with no unitId needed, and weightValue round-trips unconverted, i.e. it's already kg).
//
// TargetPaceZoneFastSPerKm/TargetPaceZoneSlowSPerKm set a Garmin pace.zone target (the
// watch's pace alert range), given in seconds per km and converted to the m/s Garmin
// stores by sync.py. Verified live 2026-10-12 (probe upload + readback + delete):
// pace.zone (workoutTargetTypeId 6) round-trips unconverted, targetValueOne = the
// faster (higher m/s) bound, targetValueTwo = the slower one.
//
// TargetHRLowBpm/TargetHRHighBpm set a Garmin heart.rate.zone target as a bpm range — the
// form Garmin Coach plan workouts and app-built workouts use (targetValueOne = low,
// targetValueTwo = high; e.g. a coach "155bpm ±10" step is 145/165). Verified live
// 2026-10-19 (probe upload + readback + delete): a 135/150 range round-trips as sent.
type WorkoutStep struct {
	Type         string  `json:"type"`
	DurationS    *int    `json:"duration_s,omitempty"`
	DistanceM    *int    `json:"distance_m,omitempty"`
	Reps         *int    `json:"reps,omitempty"`
	Sets         *int    `json:"sets,omitempty"`
	RestS        *int    `json:"rest_s,omitempty"`
	Category     *string `json:"category,omitempty"`
	ExerciseName *string `json:"exercise_name,omitempty"`
	// Heart rate range in bpm (e.g. 145 and 165). Set both, low < high.
	TargetHRLowBpm  *int `json:"target_hr_low_bpm,omitempty"`
	TargetHRHighBpm *int `json:"target_hr_high_bpm,omitempty"`
	// Pace alert range in s/km (e.g. 300 = 5:00/km). Set both, fast < slow; running only.
	TargetPaceZoneFastSPerKm *float64 `json:"target_pace_zone_fast_s_per_km,omitempty"`
	TargetPaceZoneSlowSPerKm *float64 `json:"target_pace_zone_slow_s_per_km,omitempty"`
	WeightKg                 *float64 `json:"weight_kg,omitempty"`
	Description              string   `json:"description,omitempty"`
}

// WorkoutQueueItem is written to the queue spool dir (see queueDirName) for the Python sidecar to consume.
//
// ScheduleDate (YYYY-MM-DD, optional) also puts the uploaded workout on that day of the
// Garmin calendar via garminconnect's schedule_workout — verified live 2026-10-17 (scheduled
// a real uploaded workout, read it back via get_scheduled_workout_by_id: calendarDate matched).
type WorkoutQueueItem struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Sport        string        `json:"sport"`
	ScheduleDate string        `json:"schedule_date,omitempty"`
	Steps        []WorkoutStep `json:"steps"`
}

var validStepTypes = map[string]bool{
	"warmup": true, "interval": true, "recovery": true,
	"cooldown": true, "steady": true,
}

// "walking" is deliberately excluded: no working Garmin workout-service sportTypeId
// has been found for it (verified 2026-07-28 — see sync.py's _SPORT_TYPES comment).
var validSports = map[string]bool{
	"running": true, "cycling": true,
	"swimming": true, "strength_training": true,
}

func registerWorkoutTools(s *mcp.Server, client influxClient, dataDir string) {
	type scheduledWorkoutsInput struct {
		Days int `json:"days,omitempty" jsonschema:"look-ahead window in days, default 14, max 60"` // max: maxScheduledWorkoutsDays
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_scheduled_workouts",
		Title: "Scheduled Workouts",
		Description: "Return workouts scheduled on the Garmin calendar for the next N days (default 14). Use before create_workout to avoid scheduling conflicts. " +
			"Coach/training-plan-assigned days are enriched with real target detail from the active adaptive plan: duration_s, distance_m, description (a flat display summary, e.g. \"21:00@5:10/km\" or \"137bpm\"), phase (BASE/BUILD/PEAK/TAPER/TARGET_EVENT_DAY), and rest_day. " +
			"description's number is NOT a hard cap — e.g. \"137bpm\" is the MIDPOINT of the real prescribed range, not a ceiling. The actual range is target_type (\"heart_rate\" or \"pace\", absent when the workout has no such target, e.g. strength) with target_lo/target_hi (target_lo is always <= target_hi): bpm bounds for heart_rate (e.g. 124-149 behind a \"137bpm\" description), m/s pace bounds for pace (higher m/s = faster pace, so target_hi is the faster end of the range) — prefer these over parsing description when the real range matters, e.g. judging whether an activity stayed on target. " +
			"Rest days appear here even though they have no real Garmin calendar entry — scheduled_id is 0 for those, since it's a plan entry, not a real calendar item. scheduled_id is also 0 for coach/training-plan-assigned workouts generally (deduped by sport+name rather than Garmin's own id); only self-created workouts (via create_workout) carry a real nonzero scheduled_id.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input scheduledWorkoutsInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 14, maxScheduledWorkoutsDays)
		workouts, err := queryScheduledWorkouts(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(workouts)
	})

	type calendarEventsInput struct {
		Days int `json:"days,omitempty" jsonschema:"look-ahead window in days, default 180, max 180"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_calendar_events",
		Title: "Calendar Events",
		Description: "Return Garmin calendar events (itemType \"event\", e.g. races — is_race marks those) for the next N days (default and max 180 — the sync only fetches ~6 months ahead). " +
			"primary_event marks the event an adaptive coach plan is building toward (see get_scheduled_workouts' phase field). " +
			"A personal event has event_id; a shareable event subscribed to from Garmin's public event listing has shareable_event_uuid instead (shareable_event/subscribed true, plus location/url) — the same race can appear once as each. " +
			"completion_target_value/completion_target_unit are Garmin's raw completionTarget (event distance, e.g. 13.11 mile); distance_m is derived from them only for verified units. " +
			"start_time_local is HH:MM (Garmin's startTimeHhMm) in time_zone_id. Garmin's calendar does not expose a goal time.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input calendarEventsInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 180, 180)
		events, err := queryCalendarEvents(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(events)
	})

	type createWorkoutInput struct {
		Name         string        `json:"name"  jsonschema:"workout name e.g. Tuesday tempo run"`
		Sport        string        `json:"sport" jsonschema:"sport type: running cycling swimming strength_training"`
		ScheduleDate string        `json:"schedule_date,omitempty" jsonschema:"optional YYYY-MM-DD: also schedule the workout on this day of the Garmin calendar; omit to only add it to the workout library"`
		Steps        []WorkoutStep `json:"steps" jsonschema:"ordered list of workout steps"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_workout",
		Title: "Create Workout",
		Description: "Queue a structured workout for upload to Garmin Connect. The Python sidecar uploads it on the next sync run (every 30 minutes by default; set via SYNC_SCHEDULE). Requires --data-dir pointing to the shared sync volume. Returns the queue ID. " +
			fmt.Sprintf("Set schedule_date (YYYY-MM-DD, the user's today up to %d days ahead; a date already past in every time zone is rejected) to also put it on that day of the Garmin calendar so it shows up on the watch; without it the workout only lands in the Garmin Connect workout library. get_scheduled_workouts only sees %d days ahead, so a workout scheduled further out won't appear there (or be conflict-checkable) until it's within that window. ", maxScheduleAheadDays, maxScheduledWorkoutsDays) +
			"Check get_scheduled_workouts first to avoid conflicts. " +
			"Each step needs type (warmup/interval/recovery/cooldown/steady) and exactly one of duration_s, distance_m, or reps. " +
			"For a strength exercise: call search_exercises first to get a valid category/exercise_name pair (free-text guesses are rejected), set reps, and optionally sets + rest_s to repeat it as a set — e.g. sets=3, rest_s=60 becomes \"3 sets of N reps, 60s rest between\" on the watch. Optionally set weight_kg (requires category/exercise_name) for a weight target shown on the watch. " +
			"Optional target, at most one per step: a heart rate range via target_hr_low_bpm + target_hr_high_bpm (e.g. 145 and 165, the same form as Garmin Coach's \"155bpm ±10\"), or a pace zone (Garmin pace.zone, the watch's pace alert range; running only) via target_pace_zone_fast_s_per_km + target_pace_zone_slow_s_per_km in seconds per km, e.g. 300 and 315 for 5:00–5:15/km.",
		// Adds a new file to the queue spool dir — additive, not destructive (nothing is
		// overwritten or deleted), and each call creates a distinct queue entry so it's not
		// idempotent. Explicit because the SDK defaults DestructiveHint to true when unset.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false)},
	}, func(_ context.Context, _ *mcp.CallToolRequest, input createWorkoutInput) (*mcp.CallToolResult, any, error) {
		if input.Name == "" {
			return errorResult(fmt.Errorf("name is required"))
		}
		if !validSports[input.Sport] {
			return errorResult(fmt.Errorf("invalid sport %q (valid: running, cycling, swimming, strength_training)", input.Sport))
		}
		if len(input.Steps) == 0 {
			return errorResult(fmt.Errorf("steps must not be empty"))
		}
		if err := validateScheduleDate(input.ScheduleDate, time.Now()); err != nil {
			return errorResult(err)
		}
		for i := range input.Steps {
			step := &input.Steps[i]
			if !validStepTypes[step.Type] {
				return errorResult(fmt.Errorf("create_workout: step %d: invalid type %q (valid: warmup, interval, recovery, cooldown, steady)", i+1, step.Type))
			}
			if (step.Reps != nil || step.Sets != nil || step.Category != nil) && step.Type != "interval" {
				return errorResult(fmt.Errorf("create_workout: step %d: reps/sets/category/exercise_name require type \"interval\" — Garmin's exercise-performing steps always use that stepType, got %q", i+1, step.Type))
			}
			endConditions := 0
			if step.DurationS != nil {
				endConditions++
			}
			if step.DistanceM != nil {
				endConditions++
			}
			if step.Reps != nil {
				endConditions++
			}
			if endConditions != 1 {
				return errorResult(fmt.Errorf("create_workout: step %d: specify exactly one of duration_s, distance_m, or reps", i+1))
			}
			if (step.TargetHRLowBpm == nil) != (step.TargetHRHighBpm == nil) {
				return errorResult(fmt.Errorf("create_workout: step %d: target_hr_low_bpm and target_hr_high_bpm must be set together", i+1))
			}
			if step.TargetHRLowBpm != nil {
				lo, hi := *step.TargetHRLowBpm, *step.TargetHRHighBpm
				if lo < minHRBpm || hi > maxHRBpm || lo >= hi {
					return errorResult(fmt.Errorf("create_workout: step %d: heart rate range needs %d <= target_hr_low_bpm < target_hr_high_bpm <= %d (got %d, %d)", i+1, minHRBpm, maxHRBpm, lo, hi))
				}
			}
			if (step.TargetPaceZoneFastSPerKm == nil) != (step.TargetPaceZoneSlowSPerKm == nil) {
				return errorResult(fmt.Errorf("create_workout: step %d: target_pace_zone_fast_s_per_km and target_pace_zone_slow_s_per_km must be set together", i+1))
			}
			if step.TargetPaceZoneFastSPerKm != nil {
				fast, slow := *step.TargetPaceZoneFastSPerKm, *step.TargetPaceZoneSlowSPerKm
				if input.Sport != "running" {
					return errorResult(fmt.Errorf("create_workout: step %d: a pace zone target is only supported for running", i+1))
				}
				if step.TargetHRLowBpm != nil {
					return errorResult(fmt.Errorf("create_workout: step %d: set either a heart rate range or a pace zone, not both", i+1))
				}
				if fast <= 0 || slow <= fast {
					return errorResult(fmt.Errorf("create_workout: step %d: pace zone needs 0 < target_pace_zone_fast_s_per_km < target_pace_zone_slow_s_per_km (got %v, %v)", i+1, fast, slow))
				}
			}
			if (step.Category == nil) != (step.ExerciseName == nil) {
				return errorResult(fmt.Errorf("create_workout: step %d: category and exercise_name must be set together", i+1))
			}
			if step.Category != nil && !exercises.Valid(*step.Category, *step.ExerciseName) {
				return errorResult(fmt.Errorf("create_workout: step %d: %q/%q is not a real Garmin exercise — use search_exercises to find a valid pair", i+1, *step.Category, *step.ExerciseName))
			}
			if step.Sets != nil && *step.Sets < 2 {
				return errorResult(fmt.Errorf("create_workout: step %d: sets must be >= 2 (omit sets entirely for a single set)", i+1))
			}
			if step.Sets != nil && (step.RestS == nil || *step.RestS <= 0) {
				return errorResult(fmt.Errorf("create_workout: step %d: sets requires rest_s > 0", i+1))
			}
			if step.Sets == nil && step.RestS != nil {
				return errorResult(fmt.Errorf("create_workout: step %d: rest_s requires sets", i+1))
			}
			if step.WeightKg != nil && step.Category == nil {
				return errorResult(fmt.Errorf("create_workout: step %d: weight_kg requires category/exercise_name", i+1))
			}
			if step.WeightKg != nil && *step.WeightKg <= 0 {
				return errorResult(fmt.Errorf("create_workout: step %d: weight_kg must be > 0", i+1))
			}
		}
		item := WorkoutQueueItem{
			ID:           newQueueID(time.Now()),
			Name:         input.Name,
			Sport:        input.Sport,
			ScheduleDate: input.ScheduleDate,
			Steps:        input.Steps,
		}
		if err := appendToQueue(dataDir, &item); err != nil {
			return errorResult(fmt.Errorf("create_workout: queue write: %w", err))
		}
		resp := map[string]string{"id": item.ID, "name": item.Name, "status": "queued"}
		if item.ScheduleDate != "" {
			resp["schedule_date"] = item.ScheduleDate
		}
		return jsonResult(resp)
	})

	type workoutDetailInput struct {
		WorkoutID int64 `json:"workout_id" jsonschema:"Garmin-assigned workout id (see get_scheduled_workouts' workout_id field)"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_workout_detail",
		Title: "Workout Detail",
		Description: "Return the step-by-step detail Garmin actually stored for a workout, keyed by its Garmin-assigned workout_id (see get_scheduled_workouts). " +
			"Only available for workouts uploaded via create_workout — the sync sidecar records what Garmin's response contained at upload time. Coach-plan workouts and anything built directly in the Garmin Connect app were never uploaded through this queue, so no detail is recorded for them. " +
			"steps is Garmin's own raw step tree (ExecutableStepDTO/RepeatGroupDTO shape) — useful to confirm what actually landed after an upload, since Garmin can silently normalize or reject parts of what was sent.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input workoutDetailInput) (*mcp.CallToolResult, any, error) {
		if input.WorkoutID <= 0 {
			return errorResult(fmt.Errorf("get_workout_detail: workout_id is required"))
		}
		detail, err := queryWorkoutDetail(ctx, client, input.WorkoutID)
		if err != nil {
			return errorResult(err)
		}
		// detail is nil (jsonResult marshals to JSON null) when no detail was recorded
		// for this workout_id — e.g. it wasn't uploaded via create_workout. Not an error
		// condition: consistent with get_activity_hr_zones's no-match handling.
		return jsonResult(detail)
	})
}

// minHRBpm/maxHRBpm bound a heart rate range target — wide enough for any real athlete,
// tight enough to reject a zone number (2) or a pace (300) passed by mistake.
const (
	minHRBpm = 40
	maxHRBpm = 230
)

// maxScheduleAheadDays bounds how far ahead create_workout's schedule_date may be.
const maxScheduleAheadDays = 365

// maxScheduledWorkoutsDays is get_scheduled_workouts' max look-ahead. sync.py's
// SCHEDULED_WORKOUT_LOOKAHEAD_DAYS must match it — anything further out is never synced,
// so the tool would silently return nothing there (test_sync.py checks the two agree).
const maxScheduledWorkoutsDays = 60

// earliestZone/latestZone are the furthest-behind (UTC-12) and furthest-ahead (UTC+14)
// time zones in use. A date before today in earliestZone is already over everywhere on
// Earth; today in latestZone is the latest date that's anyone's "today".
var (
	earliestZone = time.FixedZone("UTC-12", -12*60*60)
	latestZone   = time.FixedZone("UTC+14", 14*60*60)
)

func dateIn(now time.Time, zone *time.Location) time.Time {
	y, m, d := now.In(zone).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// validateScheduleDate rejects a malformed or past schedule_date ("" means unscheduled).
// The MCP server doesn't know the user's time zone, so both bounds are taken in whichever
// zone is most permissive: "past" means before today in UTC-12 (past for every user), and
// "too far ahead" means past maxScheduleAheadDays from today in UTC+14 (so every user,
// wherever they are, can schedule up to their own today + maxScheduleAheadDays).
func validateScheduleDate(s string, now time.Time) error {
	if s == "" {
		return nil
	}
	d, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return fmt.Errorf("create_workout: schedule_date must be YYYY-MM-DD, got %q", s)
	}
	if d.Before(dateIn(now, earliestZone)) {
		return fmt.Errorf("create_workout: schedule_date %s is in the past", s)
	}
	// Upper bound catches typo'd years (2062 for 2026) before they're queued and fail
	// only later, out of sight, in the sync sidecar.
	if latest := dateIn(now, latestZone).AddDate(0, 0, maxScheduleAheadDays); d.After(latest) {
		return fmt.Errorf("create_workout: schedule_date %s is more than %d days ahead", s, maxScheduleAheadDays)
	}
	return nil
}

// queueDirName is the spool directory, under the data dir shared with the sync sidecar,
// holding one <id>.json file per queued workout. Go only ever creates new files there
// (a ".item-*.tmp" dotfile, then linked — or renamed — into place; see appendToQueue); sync.py only rewrites or removes files it has
// read. Neither side ever rewrites a file the other might be writing, so no lock and no
// field-preservation rule is needed — unlike the single shared workout_queue.json this
// replaced (see sync._check_legacy_queue).
const queueDirName = "workout_queue"

func queueDir(dataDir string) string {
	return filepath.Join(dataDir, queueDirName)
}

// appendToQueue adds item to the queue as its own spool file.
func appendToQueue(dataDir string, item *WorkoutQueueItem) error {
	dir := queueDir(dataDir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create queue dir: %w", err)
	}
	data, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal queue item: %w", err)
	}
	// Unique dotfile tmp: sync.py only picks up *.json files not starting with ".", so it
	// never reads a half-written item, and concurrent calls never share a tmp file.
	f, err := os.CreateTemp(dir, ".item-*.tmp")
	if err != nil {
		return fmt.Errorf("create queue tmp: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write queue tmp: %w", err)
	}
	// fsync before it's linked in, so a crash can't leave a truncated <id>.json that sync.py
	// would quarantine, losing the workout.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync queue tmp: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close queue tmp: %w", err)
	}
	// Link, not Rename: it fails if the target exists, so an id collision is an error
	// rather than one queued workout silently replacing another. Where hard links aren't
	// supported (e.g. some SMB/FUSE mounts), fall back to Rename — newQueueID's random
	// suffix already makes a collision vanishingly unlikely.
	final := filepath.Join(dir, item.ID+".json")
	err = os.Link(tmp, final)
	switch {
	case err == nil:
	case errors.Is(err, fs.ErrExist):
		return fmt.Errorf("queue item %s already exists", item.ID)
	case linkUnsupported(err):
		if err := os.Rename(tmp, final); err != nil {
			return fmt.Errorf("queue item %s: %w", item.ID, err)
		}
	default:
		return fmt.Errorf("queue item %s: %w", item.ID, err)
	}
	// fsync the directory too, so the new entry itself survives a crash right after
	// create_workout reports "queued" (sync.py's _write_queue_item does the same). Best
	// effort only: the item is already linked in, so reporting a failure here would make
	// the caller retry create_workout and queue a duplicate workout.
	_ = syncDir(dir)
	return nil
}

// linkUnsupported reports whether a Link error means the filesystem can't hard-link at
// all (EPERM/ENOTSUP/EXDEV, e.g. some SMB/FUSE mounts) — the only case where falling back
// to an overwriting Rename is acceptable. Any other error (EACCES, ENOSPC, …) is real.
func linkUnsupported(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EXDEV)
}

func syncDir(dir string) error {
	d, err := os.Open(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("open queue dir: %w", err)
	}
	defer func() { _ = d.Close() }()
	// Directory fsync isn't supported everywhere (e.g. Windows); the file itself was
	// already synced, so that's not worth failing the call over.
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("sync queue dir: %w", err)
	}
	return nil
}

// newQueueID is a UnixNano timestamp plus a random suffix. It's fixed length, so sync.py's
// plain name sort (_queued_item_paths) is creation order — keep it fixed-length. The
// suffix keeps concurrent create_workout calls unique even where the clock is only
// microsecond-resolution (macOS).
func newQueueID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error (Go 1.24+)
	return fmt.Sprintf("%d-%s", now.UnixNano(), hex.EncodeToString(b[:]))
}

func queryScheduledWorkouts(ctx context.Context, client influxClient, days int) ([]garmin.ScheduledWorkout, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.Add(time.Duration(days) * 24 * time.Hour)

	rows, err := queryMeasurementRange(ctx, client, influx.MeasurementScheduledWorkout, start, end)
	if err != nil {
		return nil, fmt.Errorf("get_scheduled_workouts: %w", err)
	}
	workouts := make([]garmin.ScheduledWorkout, 0, len(rows))
	for _, row := range rows {
		w := garmin.ScheduledWorkoutFrom(row)
		if w.DeletedAt > 0 {
			continue
		}
		workouts = append(workouts, w)
	}
	workouts = dedupeGhostCoachPlanEntries(workouts)

	planRows, err := queryMeasurementRange(ctx, client, influx.MeasurementTrainingPlanTask, start, end)
	if err != nil {
		return nil, fmt.Errorf("get_scheduled_workouts: training plan detail: %w", err)
	}
	tasks := make([]garmin.TrainingPlanTask, 0, len(planRows))
	for _, row := range planRows {
		tasks = append(tasks, garmin.TrainingPlanTaskFrom(row))
	}

	return mergeTrainingPlanDetail(workouts, tasks), nil
}

func queryCalendarEvents(ctx context.Context, client influxClient, days int) ([]garmin.CalendarEvent, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour)
	end := start.Add(time.Duration(days) * 24 * time.Hour)

	rows, err := queryMeasurementRange(ctx, client, influx.MeasurementCalendarEvent, start, end)
	if err != nil {
		return nil, fmt.Errorf("get_calendar_events: %w", err)
	}
	events := make([]garmin.CalendarEvent, 0, len(rows))
	for _, row := range rows {
		e := garmin.CalendarEventFrom(row)
		if e.DeletedAt > 0 {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

func queryWorkoutDetail(ctx context.Context, client influxClient, workoutID int64) (*garmin.WorkoutDetail, error) {
	// Time-bounded for the same reason as tools/splits.go's activityTimeWindow
	// (#95) — an unbounded activity/workout-id-only filter forces InfluxDB 3 Core
	// to scan the table's entire history. workout_detail is written by
	// sync.py's Point(...).time(datetime.now(UTC)) (sync time, not event time),
	// so there's no smaller sibling measurement to look the real timestamp up in
	// first; a generous flat lookback bound is the direct equivalent instead.
	start := time.Now().UTC().Add(-activityLookupHorizon)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE workout_id = '%d' AND time >= '%s' ORDER BY time DESC LIMIT 1",
		influx.MeasurementWorkoutDetail, workoutID, start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_workout_detail: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	detail := garmin.WorkoutDetailFrom(rows[0])
	return &detail, nil
}

func queryMeasurementRange(ctx context.Context, client influxClient, measurement string, start, end time.Time) ([]map[string]any, error) {
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' AND time < '%s' ORDER BY time ASC",
		measurement,
		start.Format(time.RFC3339),
		end.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", measurement, err)
	}
	return rows, nil
}

// mergeTrainingPlanDetail enriches calendar-list workouts with real per-day detail from
// the active adaptive training plan, and adds a synthesized entry for any plan day with
// no calendar item at all — which is how rest days show up, since Garmin's calendar list
// never includes them (see sync_training_plan's doc comment in sync.py).
//
// Only enriches workouts with WorkoutID == 0 (i.e. items sync_scheduled_workouts already
// identified as coach-plan-sourced, not self-created via create_workout) — a self-created
// workout that happens to land on the same date as a coach-plan task must not inherit the
// coach's target detail.
//
// Joins on (date, sport), not date alone — verified live 2026-08-07 that the coach plan
// can assign two tasks on the same calendarDate (running + strength_training, a real
// two-a-day). A date-only join would match one task to both calendar entries and drop
// the other's detail.
func mergeTrainingPlanDetail(workouts []garmin.ScheduledWorkout, tasks []garmin.TrainingPlanTask) []garmin.ScheduledWorkout {
	byKey := make(map[string]garmin.TrainingPlanTask, len(tasks))
	for i := range tasks {
		byKey[planTaskKey(tasks[i].Date, tasks[i].Sport)] = tasks[i]
	}

	merged := make([]garmin.ScheduledWorkout, 0, len(workouts)+len(tasks))
	seen := make(map[string]bool, len(workouts))
	for i := range workouts {
		w := workouts[i]
		key := planTaskKey(w.Date, w.Sport)
		if t, ok := byKey[key]; ok && w.WorkoutID == 0 {
			w.DistanceM = t.DistanceM
			w.Description = t.Description
			w.RestDay = t.RestDay
			w.Phase = t.Phase
			w.TargetType = t.TargetType
			w.TargetLo = t.TargetLo
			w.TargetHi = t.TargetHi
			if w.DurationS == 0 {
				w.DurationS = t.DurationS
			}
		}
		merged = append(merged, w)
		seen[key] = true
	}
	for i := range tasks {
		t := tasks[i]
		if seen[planTaskKey(t.Date, t.Sport)] {
			continue
		}
		merged = append(merged, garmin.ScheduledWorkout{
			Date:        t.Date,
			Sport:       t.Sport,
			Name:        t.Name,
			DurationS:   t.DurationS,
			DistanceM:   t.DistanceM,
			Description: t.Description,
			RestDay:     t.RestDay,
			Phase:       t.Phase,
			TargetType:  t.TargetType,
			TargetLo:    t.TargetLo,
			TargetHi:    t.TargetHi,
		})
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Date != merged[j].Date {
			return merged[i].Date < merged[j].Date
		}
		if merged[i].Sport != merged[j].Sport {
			return merged[i].Sport < merged[j].Sport
		}
		return merged[i].Name < merged[j].Name
	})
	return merged
}

func planTaskKey(date, sport string) string {
	return date + "|" + sport
}

// dedupeGhostCoachPlanEntries collapses stale duplicate scheduled_workout points for
// the same coach-plan item down to one.
//
// sync_scheduled_workouts tags coach-plan items by (sport, workout_name) specifically
// so the plan regenerating the same logical workout under a new calendar-item id
// doesn't pile up ghost duplicates (#85, since InfluxDB 3 Core has no DELETE to clean
// old points up). That fix only stops new duplicates from being written — it can't
// retroactively fix points written by older code before the fix existed. Found live
// 2026-08-10: a single real coach-plan item ("Threshold", 2026-08-11) had 5 points in
// InfluxDB — 4 stale ones from pre-#85 runs (each with a since-churned scheduled_id
// tag, content otherwise identical) plus 1 correctly (sport, workout_name)-tagged
// current one — because those old runs classified this same live item as
// self-created (scheduled_id) instead of coach-plan.
//
// Self-created workouts (WorkoutID != 0) are never touched: those legitimately keep
// a stable, non-churning id, so duplicate detection isn't needed and collapsing two
// distinct same-day-same-name self-created workouts would be a real, if rare, data
// loss. Only entries with WorkoutID == 0 (coach-plan-sourced or a synthesized rest
// day, per ScheduledWorkout's own doc comment) are deduped by (date, sport, name).
//
// Content is otherwise identical by construction, but ScheduledID isn't: a stale
// ghost can carry a nonzero, since-churned scheduled_id (leaked from a pre-#85
// misclassification — see this function's own doc comment above), while the
// correctly-tagged current point always has ScheduledID == 0. Picking an arbitrary
// survivor could leak that stale id back to callers, contradicting
// get_scheduled_workouts' own documented contract ("scheduled_id is also 0 for
// coach/training-plan-assigned workouts generally") — so a ScheduledID == 0 entry
// always wins over one with a nonzero ScheduledID when both exist for the same key.
func dedupeGhostCoachPlanEntries(workouts []garmin.ScheduledWorkout) []garmin.ScheduledWorkout {
	chosen := make(map[string]garmin.ScheduledWorkout, len(workouts))
	keyOrder := make([]string, 0, len(workouts))
	deduped := make([]garmin.ScheduledWorkout, 0, len(workouts))
	for i := range workouts {
		w := workouts[i]
		if w.WorkoutID != 0 {
			deduped = append(deduped, w)
			continue
		}
		key := planTaskKey(w.Date, w.Sport) + "|" + w.Name
		existing, ok := chosen[key]
		if !ok {
			keyOrder = append(keyOrder, key)
			chosen[key] = w
			continue
		}
		if existing.ScheduledID != 0 && w.ScheduledID == 0 {
			chosen[key] = w
		}
	}
	for _, key := range keyOrder {
		deduped = append(deduped, chosen[key])
	}
	return deduped
}
