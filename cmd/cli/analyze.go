package main

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gordcurrie/waypoint/internal/analysis"
	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
	"github.com/gordcurrie/waypoint/internal/llm"
)

const analyzeSystemPrompt = `You are an expert endurance coach analyzing an athlete's training data.
Provide a concise, actionable analysis. Focus on patterns, recovery status, and key insights.
Use concrete numbers from the data. Keep response under 400 words.`

func runAnalyze(client *influx.Client, period string) error {
	var days int
	switch period {
	case "week":
		days = 7
	case "month":
		days = 30
	default:
		return fmt.Errorf("analyze: unknown period %q, use week or month", period)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	data, err := gatherData(ctx, client, days)
	if err != nil {
		return err
	}

	provider, err := llm.NewFromEnv()
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	prompt := buildAnalyzePrompt(period, days, &data)
	response, err := provider.Complete(ctx, analyzeSystemPrompt, prompt)
	if err != nil {
		return fmt.Errorf("llm complete: %w", err)
	}

	fmt.Println(response)
	return nil
}

type trainingData struct {
	activities []garmin.Activity
	sleep      []garmin.Sleep
	hrv        []garmin.HRV
	dailyStats []garmin.DailyStats
	readiness  []garmin.TrainingReadiness
	load       []analysis.Result
}

func gatherData(ctx context.Context, client *influx.Client, days int) (trainingData, error) {
	var d trainingData
	var err error

	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)
	startStr := start.Format(time.RFC3339)

	rows, err := client.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC LIMIT 50",
		influx.MeasurementActivity, startStr,
	))
	if err != nil {
		return d, fmt.Errorf("activities: %w", err)
	}
	d.activities = make([]garmin.Activity, 0, len(rows))
	for _, row := range rows {
		d.activities = append(d.activities, garmin.ActivityFrom(row))
	}

	rows, err = client.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC LIMIT %d",
		influx.MeasurementSleep, startStr, days,
	))
	if err != nil {
		return d, fmt.Errorf("sleep: %w", err)
	}
	d.sleep = make([]garmin.Sleep, 0, len(rows))
	for _, row := range rows {
		d.sleep = append(d.sleep, garmin.SleepFrom(row))
	}

	rows, err = client.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time ASC LIMIT %d",
		influx.MeasurementHRV, startStr, days,
	))
	if err != nil {
		return d, fmt.Errorf("hrv: %w", err)
	}
	d.hrv = make([]garmin.HRV, 0, len(rows))
	for _, row := range rows {
		d.hrv = append(d.hrv, garmin.HRVFrom(row))
	}

	rows, err = client.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC LIMIT %d",
		influx.MeasurementDailyStats, startStr, days,
	))
	if err != nil {
		return d, fmt.Errorf("daily stats: %w", err)
	}
	d.dailyStats = make([]garmin.DailyStats, 0, len(rows))
	for _, row := range rows {
		d.dailyStats = append(d.dailyStats, garmin.DailyStatsFrom(row))
	}

	rows, err = client.Query(ctx, fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC LIMIT %d",
		influx.MeasurementTrainingReadiness, startStr, days,
	))
	if err != nil {
		return d, fmt.Errorf("readiness: %w", err)
	}
	d.readiness = make([]garmin.TrainingReadiness, 0, len(rows))
	for _, row := range rows {
		d.readiness = append(d.readiness, garmin.TrainingReadinessFrom(row))
	}

	d.load, err = analysis.Compute(ctx, client, days)
	if err != nil {
		return d, fmt.Errorf("training load: %w", err)
	}

	return d, nil
}

func buildAnalyzePrompt(period string, days int, d *trainingData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Analyze my last %s (%d days) of training data:\n\n", period, days) //nolint:gosec // period is validated to "week"/"month" by caller

	if len(d.load) > 0 {
		latest := d.load[len(d.load)-1]
		fmt.Fprintf(&sb, "TRAINING LOAD (today %s):\n", latest.Date.Format("2006-01-02"))
		fmt.Fprintf(&sb, "  ATL (acute 7d): %.1f\n", latest.ATL)
		fmt.Fprintf(&sb, "  CTL (chronic 42d): %.1f\n", latest.CTL)
		fmt.Fprintf(&sb, "  TSB (form): %+.1f\n\n", latest.TSB)
	}

	if len(d.readiness) > 0 {
		r := d.readiness[0]
		fmt.Fprintf(&sb, "READINESS (%s): score=%.0f, hrv_status=%.0f, sleep_score=%.0f\n\n",
			r.Time.Format("2006-01-02"), r.Score, r.HRVStatus, r.SleepScore)
	}

	if len(d.activities) > 0 {
		fmt.Fprintf(&sb, "ACTIVITIES (%d total):\n", len(d.activities))
		for i := range d.activities {
			a := &d.activities[i]
			fmt.Fprintf(&sb, "  %s %s: dist=%.1fkm, dur=%.0fmin, hr=%.0f/%.0f bpm, load=%.0f\n",
				a.Time.Format("2006-01-02"), a.Sport,
				a.DistanceM/1000, a.DurationS/60,
				a.AvgHRBPM, a.MaxHRBPM,
				a.TrainingLoad,
			)
		}
		sb.WriteString("\n")
	}

	if len(d.sleep) > 0 {
		sb.WriteString("SLEEP:\n")
		for _, s := range d.sleep {
			fmt.Fprintf(&sb, "  %s: total=%.1fh, deep=%.1fh, rem=%.1fh, score=%.0f, hrv=%.0fms\n",
				s.Time.Format("2006-01-02"),
				s.TotalSleepS/3600, s.DeepSleepS/3600, s.REMSleepS/3600,
				s.SleepScore, s.AvgHRVMS,
			)
		}
		sb.WriteString("\n")
	}

	if len(d.hrv) > 0 {
		sb.WriteString("HRV:\n")
		for _, h := range d.hrv {
			status := "unknown"
			if h.Status != nil {
				switch *h.Status {
				case 2.0:
					status = "balanced"
				case 1.0:
					status = "unbalanced"
				case 0.0:
					status = "poor"
				}
			}
			fmt.Fprintf(&sb, "  %s: weekly_avg=%.0fms, last_night=%.0fms, status=%s\n",
				h.Time.Format("2006-01-02"), h.WeeklyAvgMS, h.LastNightMS, status,
			)
		}
		sb.WriteString("\n")
	}

	if len(d.dailyStats) > 0 {
		sb.WriteString("DAILY STATS:\n")
		for _, s := range d.dailyStats {
			fmt.Fprintf(&sb, "  %s: steps=%.0f, resting_hr=%.0f, body_battery=%.0f-%.0f, stress=%.0f\n",
				s.Time.Format("2006-01-02"),
				s.Steps, s.RestingHRBPM,
				s.BodyBatteryMin, s.BodyBatteryMax,
				s.StressAvg,
			)
		}
	}

	return sb.String()
}
