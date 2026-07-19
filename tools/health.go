package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerHealthTools(s *mcp.Server, client influxClient) {
	type daysInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 7"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_daily_stats",
		Description: "Return daily Garmin stats: steps, resting HR, body battery, stress, and intensity minutes.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input daysInput) (*mcp.CallToolResult, any, error) {
		days := input.Days
		if days <= 0 {
			days = 7
		}
		stats, err := queryDailyStats(ctx, client, days)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(stats)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_sleep_summary",
		Description: "Return recent sleep data: duration, stages, sleep score, HRV, and SpO2.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input daysInput) (*mcp.CallToolResult, any, error) {
		days := input.Days
		if days <= 0 {
			days = 7
		}
		sleep, err := querySleep(ctx, client, days)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(sleep)
	})

	type hrvInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 14"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_hrv_trend",
		Description: "Return HRV trend: weekly average, last-night reading, and status over time.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input hrvInput) (*mcp.CallToolResult, any, error) {
		days := input.Days
		if days <= 0 {
			days = 14
		}
		hrv, err := queryHRV(ctx, client, days)
		if err != nil {
			return nil, nil, err
		}
		return jsonResult(hrv)
	})
}

func queryDailyStats(ctx context.Context, client influxClient, days int) ([]garmin.DailyStats, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC",
		influx.MeasurementDailyStats, start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_daily_stats: %w", err)
	}
	result := make([]garmin.DailyStats, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.DailyStatsFrom(row))
	}
	return result, nil
}

func querySleep(ctx context.Context, client influxClient, days int) ([]garmin.Sleep, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC",
		influx.MeasurementSleep, start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_sleep_summary: %w", err)
	}
	result := make([]garmin.Sleep, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.SleepFrom(row))
	}
	return result, nil
}

func queryHRV(ctx context.Context, client influxClient, days int) ([]garmin.HRV, error) {
	start := time.Now().UTC().Truncate(24 * time.Hour).AddDate(0, 0, -days)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time ASC",
		influx.MeasurementHRV, start.Format(time.RFC3339),
	)
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_hrv_trend: %w", err)
	}
	result := make([]garmin.HRV, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.HRVFrom(row))
	}
	return result, nil
}
