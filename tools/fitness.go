package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerFitnessTools(s *mcp.Server, client influxClient) {
	type performanceTrendInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 90"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_performance_trend",
		Description: "Return VO2max and Garmin fitness age over time from the performance measurement. " +
			"Trend is only meaningful over longer windows; default lookback is 90 days.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input performanceTrendInput) (*mcp.CallToolResult, any, error) {
		days := clampDays(input.Days, 90, 365)
		trend, err := queryPerformanceTrend(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(trend)
	})

	type lactateThresholdInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 90"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_lactate_threshold",
		Description: "Return lactate threshold heart rate and pace over time from the lactate_threshold measurement. " +
			"Trend is only meaningful over longer windows; default lookback is 90 days. " +
			"Caveat: lt_pace_s_per_km's unit conversion (s/m to s/km) has not been verified against a real " +
			"lactate threshold test — treat this value as unconfirmed until cross-checked against Garmin Connect directly.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input lactateThresholdInput) (*mcp.CallToolResult, any, error) {
		days := clampDays(input.Days, 90, 365)
		trend, err := queryLactateThreshold(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(trend)
	})
}

func queryPerformanceTrend(ctx context.Context, client influxClient, days int) ([]garmin.Performance, error) {
	sql := timeRangeQuery(influx.MeasurementPerformance, days, "ASC")
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_performance_trend: %w", err)
	}
	result := make([]garmin.Performance, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.PerformanceFrom(row))
	}
	return result, nil
}

func queryLactateThreshold(ctx context.Context, client influxClient, days int) ([]garmin.LactateThreshold, error) {
	sql := timeRangeQuery(influx.MeasurementLactateThreshold, days, "ASC")
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_lactate_threshold: %w", err)
	}
	result := make([]garmin.LactateThreshold, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.LactateThresholdFrom(row))
	}
	return result, nil
}
