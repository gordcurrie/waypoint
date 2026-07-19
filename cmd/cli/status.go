package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gordcurrie/waypoint/internal/analysis"
	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func runStatus(client *influx.Client) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	results, err := analysis.Compute(ctx, client, 1)
	if err != nil {
		return fmt.Errorf("training load: %w", err)
	}

	readiness, err := queryReadiness(ctx, client, 1)
	if err != nil {
		return fmt.Errorf("readiness: %w", err)
	}

	if len(results) > 0 {
		r := results[len(results)-1]
		fmt.Printf("Training Load  (%s)\n", r.Date.Format("2006-01-02"))
		fmt.Printf("  ATL  (acute 7d):   %.1f\n", r.ATL)
		fmt.Printf("  CTL  (chronic 42d): %.1f\n", r.CTL)
		fmt.Printf("  TSB  (form):       %+.1f\n", r.TSB)
	} else {
		fmt.Fprintln(os.Stderr, "no training load data")
	}

	if len(readiness) > 0 {
		r := readiness[0]
		fmt.Printf("\nTraining Readiness  (%s)\n", r.Time.Format("2006-01-02"))
		fmt.Printf("  Score:        %.0f/100\n", r.Score)
		fmt.Printf("  HRV status:   %.0f\n", r.HRVStatus)
		fmt.Printf("  Sleep score:  %.0f\n", r.SleepScore)
		if r.RecoveryTimeH > 0 {
			fmt.Printf("  Recovery:     %.0fh remaining\n", r.RecoveryTimeH)
		}
	} else {
		fmt.Fprintln(os.Stderr, "no readiness data")
	}

	return nil
}

func queryReadiness(ctx context.Context, client *influx.Client, days int) ([]garmin.TrainingReadiness, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC LIMIT %d",
		influx.MeasurementTrainingReadiness, start.Format(time.RFC3339), days,
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("query readiness: %w", err)
	}
	result := make([]garmin.TrainingReadiness, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.TrainingReadinessFrom(row))
	}
	return result, nil
}
