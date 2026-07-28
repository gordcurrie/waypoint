package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerHealthTools(s *mcp.Server, client influxClient) {
	type daysInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 7"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_daily_stats",
		Title: "Daily Stats",
		Description: "Return daily Garmin stats: steps, resting HR, body battery, stress, and intensity minutes. " +
			"For sleep, HRV, and respiration detail (also daily) see get_sleep_summary, get_hrv_trend, and get_respiration.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input daysInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 7, 365)
		stats, err := queryDailyStats(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(stats)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_sleep_summary",
		Title: "Sleep Summary",
		Description: "Return recent sleep data: duration, stages, sleep score, SpO2, breathing rate, and stress. " +
			"Garmin's sleep API reports no HRV data — for HRV use get_hrv_trend. " +
			"Feeds into get_training_readiness.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input daysInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 7, 365)
		sleep, err := querySleep(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(sleep)
	})

	type hrvInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 14"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_hrv_trend",
		Title: "HRV Trend",
		Description: "Return HRV trend: weekly average, last-night reading, and status over time. " +
			"Feeds into get_training_readiness's HRV status; get_sleep_summary does not include HRV.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input hrvInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 14, 365)
		hrv, err := queryHRV(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(hrv)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_respiration",
		Title: "Respiration",
		Description: "Return daily respiration data: waking, sleep, highest, and lowest breaths per minute. " +
			"Separate from get_sleep_summary's single avg_breathing_rate — this breaks it out by waking/sleep/high/low.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input daysInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 7, 365)
		resp, err := queryRespiration(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(resp)
	})
}

func queryDailyStats(ctx context.Context, client influxClient, days int) ([]garmin.DailyStats, error) {
	sql := timeRangeQuery(influx.MeasurementDailyStats, days, "DESC")
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
	sql := timeRangeQuery(influx.MeasurementSleep, days, "DESC")
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
	sql := timeRangeQuery(influx.MeasurementHRV, days, "ASC")
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

func queryRespiration(ctx context.Context, client influxClient, days int) ([]garmin.Respiration, error) {
	sql := timeRangeQuery(influx.MeasurementRespiration, days, "ASC")
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_respiration: %w", err)
	}
	result := make([]garmin.Respiration, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.RespirationFrom(row))
	}
	return result, nil
}
