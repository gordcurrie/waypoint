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

func runPlan(client *influx.Client, weeks int) error {
	if weeks < 1 || weeks > 52 {
		return fmt.Errorf("plan: weeks must be 1–52, got %d", weeks)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Gather 4 weeks of history to inform the plan
	data, err := gatherData(ctx, client, 28)
	if err != nil {
		return err
	}

	provider, err := llm.NewFromEnv()
	if err != nil {
		return fmt.Errorf("llm: %w", err)
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
	fmt.Fprintf(&sb, "Create a %d-week training plan based on my recent fitness data.\n\n", weeks)
	sb.WriteString("RECENT HISTORY (last 28 days):\n")

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

	fmt.Fprintf(&sb, "\nCreate a %d-week plan. Include weekly structure and key sessions.", weeks)
	return sb.String()
}
