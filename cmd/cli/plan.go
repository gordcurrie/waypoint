package main

import (
	"context"
	"fmt"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gordcurrie/waypoint/internal/influx"
	"github.com/gordcurrie/waypoint/internal/llm"
)

const planSystemPrompt = `You are an expert endurance coach creating a structured training plan.
Use the athlete's recent training data to calibrate the plan appropriately.
Format the plan week by week with specific workouts. Be concrete about duration, intensity, and type.
Keep the plan realistic given the athlete's current fitness (CTL) and form (TSB).`

const planHistoryDays = 28

func runPlan(client *influx.Client, weeks int) error {
	if weeks < 1 || weeks > 52 {
		return fmt.Errorf("plan: weeks must be 1–52, got %d", weeks)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	provider, err := llm.NewFromEnv()
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	data, err := gatherData(ctx, client, planHistoryDays)
	if err != nil {
		return err
	}

	prompt := buildPlanPrompt(weeks, &data)
	response, err := provider.Complete(ctx, planSystemPrompt, prompt)
	if err != nil {
		return fmt.Errorf("llm complete: %w", err)
	}

	fmt.Println(response)
	return nil
}

func buildPlanPrompt(weeks int, d *trainingData) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Create a %d-week training plan based on my recent fitness data.\n\n", weeks) //nolint:gosec // weeks is validated 1-52 by caller
	fmt.Fprintf(&sb, "RECENT HISTORY (last %d days):\n", planHistoryDays)

	if len(d.load) > 0 {
		latest := d.load[len(d.load)-1]
		fmt.Fprintf(&sb, "Current fitness: ATL=%.1f, CTL=%.1f, TSB=%+.1f\n", latest.ATL, latest.CTL, latest.TSB)
	}

	if len(d.activities) > 0 {
		fmt.Fprintf(&sb, "Recent activities (%d):\n", len(d.activities))
		for i := range d.activities {
			a := &d.activities[i]
			fmt.Fprintf(&sb, "  %s %s: %.1fkm, %.0fmin, load=%.0f\n",
				a.Time.Format("2006-01-02"), a.Sport,
				a.DistanceM/1000, a.DurationS/60, a.TrainingLoad,
			)
		}
	}

	if len(d.sleep) > 0 {
		var totalScore float64
		for _, s := range d.sleep {
			totalScore += s.SleepScore
		}
		fmt.Fprintf(&sb, "Avg sleep score: %.0f/100\n", totalScore/float64(len(d.sleep)))
	}

	if len(d.readiness) > 0 {
		r := d.readiness[0]
		fmt.Fprintf(&sb, "Latest readiness: %.0f/100 (HRV status=%.0f)\n", r.Score, r.HRVStatus)
	}

	if len(d.hrv) > 0 {
		var totalHRV float64
		for _, h := range d.hrv {
			totalHRV += h.WeeklyAvgMS
		}
		latest := d.hrv[len(d.hrv)-1]
		status := "unknown"
		if latest.Status != nil {
			switch *latest.Status {
			case 2.0:
				status = "balanced"
			case 1.0:
				status = "unbalanced"
			case 0.0:
				status = "poor"
			case 3.0:
				status = "low-unbalanced"
			}
		}
		fmt.Fprintf(&sb, "HRV: 28d avg=%.0fms, current status=%s\n", totalHRV/float64(len(d.hrv)), status)
	}

	if len(d.dailyStats) > 0 {
		var totalBB float64
		for _, s := range d.dailyStats {
			totalBB += s.BodyBatteryMax
		}
		fmt.Fprintf(&sb, "Avg daily body battery max: %.0f/100\n", totalBB/float64(len(d.dailyStats)))
	}

	fmt.Fprintf(&sb, "\nCreate a %d-week plan. Include weekly structure and key sessions.", weeks) //nolint:gosec // weeks is validated 1-52 by caller
	return sb.String()
}
