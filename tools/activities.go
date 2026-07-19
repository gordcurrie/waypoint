package tools

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

var validSport = regexp.MustCompile(`^[a-z0-9_]+$`)

// WeeklyVolume summarises one sport's training load for a calendar week.
type WeeklyVolume struct {
	WeekStart    string  `json:"week_start"` // ISO date of the Monday
	Sport        string  `json:"sport"`
	DistanceM    float64 `json:"distance_m"`
	DurationS    float64 `json:"duration_s"`
	TrainingLoad float64 `json:"training_load"`
	Count        int     `json:"count"`
}

func registerActivityTools(s *mcp.Server, client influxClient) {
	type listActivitiesInput struct {
		Limit int    `json:"limit,omitempty" jsonschema:"max results, default 10, max 50"`
		Days  int    `json:"days,omitempty"  jsonschema:"lookback window in days, default 30"`
		Sport string `json:"sport,omitempty" jsonschema:"filter by sport type, e.g. running or cycling"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_recent_activities",
		Description: "Return recent Garmin activities including distance, duration, heart rate, and training load.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input listActivitiesInput) (*mcp.CallToolResult, any, error) {
		limit := input.Limit
		if limit <= 0 {
			limit = 10
		} else if limit > 50 {
			limit = 50
		}
		days := input.Days
		if days <= 0 {
			days = 30
		}
		if input.Sport != "" && !validSport.MatchString(input.Sport) {
			return errorResult(fmt.Errorf("get_recent_activities: invalid sport %q — use lowercase letters, digits, and underscores only", input.Sport))
		}

		activities, err := queryActivities(ctx, client, days, limit, input.Sport)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(activities)
	})

	type weeklyVolumeInput struct {
		Weeks int `json:"weeks,omitempty" jsonschema:"number of weeks to include, default 4"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_weekly_volume",
		Description: "Return total distance, duration, and training load per sport per week.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input weeklyVolumeInput) (*mcp.CallToolResult, any, error) {
		weeks := input.Weeks
		if weeks <= 0 {
			weeks = 4
		}
		if weeks > 52 {
			weeks = 52
		}

		vol, err := queryWeeklyVolume(ctx, client, weeks)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(vol)
	})
}

func queryActivities(ctx context.Context, client influxClient, days, limit int, sport string) ([]garmin.Activity, error) {
	if sport != "" && !validSport.MatchString(sport) {
		return nil, fmt.Errorf("get_recent_activities: invalid sport %q — use lowercase letters, digits, and underscores only", sport)
	}
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)

	sportClause := ""
	if sport != "" {
		sportClause = fmt.Sprintf(" AND sport = '%s'", sport)
	}

	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s'%s ORDER BY time DESC LIMIT %d",
		influx.MeasurementActivity, start.Format(time.RFC3339), sportClause, limit,
	)

	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_recent_activities: %w", err)
	}

	activities := make([]garmin.Activity, 0, len(rows))
	for _, row := range rows {
		activities = append(activities, garmin.ActivityFrom(row))
	}
	return activities, nil
}

func queryWeeklyVolume(ctx context.Context, client influxClient, weeks int) ([]WeeklyVolume, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -weeks*7)

	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' AND training_load IS NOT NULL ORDER BY time ASC",
		influx.MeasurementActivity, start.Format(time.RFC3339),
	)

	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_weekly_volume: %w", err)
	}

	// Group by ISO year-week + sport.
	type key struct {
		week  string
		sport string
	}
	byKey := make(map[key]*WeeklyVolume)
	var order []key

	for _, row := range rows {
		a := garmin.ActivityFrom(row)
		weekStart := isoWeekMonday(a.Time)
		k := key{week: weekStart, sport: a.Sport}
		if _, ok := byKey[k]; !ok {
			byKey[k] = &WeeklyVolume{WeekStart: weekStart, Sport: a.Sport}
			order = append(order, k)
		}
		v := byKey[k]
		v.DistanceM += a.DistanceM
		v.DurationS += a.DurationS
		v.TrainingLoad += a.TrainingLoad
		v.Count++
	}

	result := make([]WeeklyVolume, 0, len(order))
	for _, k := range order {
		result = append(result, *byKey[k])
	}
	return result, nil
}

// isoWeekMonday returns the ISO 8601 date string of the Monday that starts the
// calendar week containing t.
func isoWeekMonday(t time.Time) string {
	d := t.UTC()
	weekday := int(d.Weekday())
	if weekday == 0 { // Sunday → 7 in ISO
		weekday = 7
	}
	monday := d.AddDate(0, 0, -(weekday - 1)).Truncate(24 * time.Hour)
	return monday.Format("2006-01-02")
}
