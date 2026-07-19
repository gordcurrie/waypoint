package analysis

import (
	"context"
	"fmt"
	"time"

	"github.com/gordcurrie/waypoint/internal/influx"
)

const (
	atlDays    = 7
	ctlDays    = 42
	warmupDays = ctlDays * 3 // pre-window history needed to converge CTL EMA
)

// querier is the subset of influx.Client required by Compute.
type querier interface {
	Query(ctx context.Context, sql string) ([]map[string]any, error)
}

// writer is the subset of influx.Client required by WriteResults.
type writer interface {
	WritePoints(ctx context.Context, points ...*influx.Point) error
}

// Result holds ATL/CTL/TSB for one calendar day.
type Result struct {
	Date time.Time `json:"date"`
	ATL  float64   `json:"atl"` // 7-day EMA of daily training load (acute)
	CTL  float64   `json:"ctl"` // 42-day EMA of daily training load (chronic)
	TSB  float64   `json:"tsb"` // training stress balance: CTL - ATL
	Load float64   `json:"load"` // raw total training load for this day
}

// Compute queries activities from InfluxDB and returns ATL/CTL/TSB for each
// day in [today-windowDays+1, today]. Queries warmupDays of extra history
// automatically to converge the EMAs before the output window.
func Compute(ctx context.Context, client querier, windowDays int) ([]Result, error) {
	if windowDays < 1 {
		return nil, fmt.Errorf("analysis.Compute: windowDays must be >= 1, got %d", windowDays)
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	start := today.AddDate(0, 0, -(windowDays - 1 + warmupDays))

	sql := fmt.Sprintf(
		"SELECT time, training_load FROM %s WHERE time >= '%s' AND training_load IS NOT NULL ORDER BY time ASC",
		influx.MeasurementActivity,
		start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("analysis.Compute: %w", err)
	}

	dayLoads := make(map[string]float64, len(rows))
	for _, row := range rows {
		var ts time.Time
		switch t := row["time"].(type) {
		case string:
			ts, _ = time.Parse(time.RFC3339Nano, t)
		case float64:
			ts = time.Unix(0, int64(t)).UTC()
		case int64:
			ts = time.Unix(0, t).UTC()
		}
		var load float64
		switch v := row["training_load"].(type) {
		case float64:
			load = v
		case int64:
			load = float64(v)
		}
		if load == 0 || ts.IsZero() {
			continue
		}
		day := ts.UTC().Truncate(24 * time.Hour).Format("2006-01-02")
		dayLoads[day] += load
	}

	return compute(dayLoads, start, today, windowDays), nil
}

// compute is the pure EMA calculation — separated for testability.
// dayLoads maps "YYYY-MM-DD" to the total training load for that day.
// Returns windowDays results ending at today (inclusive on both ends).
func compute(dayLoads map[string]float64, start, today time.Time, windowDays int) []Result {
	kATL := 1.0 / atlDays
	kCTL := 1.0 / ctlDays

	windowStart := today.AddDate(0, 0, -(windowDays - 1))

	var atl, ctl float64
	var results []Result

	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		load := dayLoads[d.Format("2006-01-02")]
		atl = atl*(1-kATL) + load*kATL
		ctl = ctl*(1-kCTL) + load*kCTL

		if !d.Before(windowStart) {
			results = append(results, Result{
				Date: d,
				ATL:  atl,
				CTL:  ctl,
				TSB:  ctl - atl,
				Load: load,
			})
		}
	}
	return results
}

// WriteResults writes computed ATL/CTL/TSB to the training_load measurement.
func WriteResults(ctx context.Context, client writer, results []Result) error {
	if len(results) == 0 {
		return nil
	}
	points := make([]*influx.Point, 0, len(results))
	for _, r := range results {
		p := influx.NewPoint(influx.MeasurementTrainingLoad).
			SetField("atl_7day", r.ATL).
			SetField("ctl_42day", r.CTL).
			SetField("tsb", r.TSB).
			SetTimestamp(r.Date)
		points = append(points, p)
	}
	if err := client.WritePoints(ctx, points...); err != nil {
		return fmt.Errorf("analysis.WriteResults: %w", err)
	}
	return nil
}
