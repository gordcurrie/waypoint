package tools

import (
	"context"
	"fmt"
	"time"

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
		days := input.Days
		if days <= 0 {
			days = 90
		} else if days > 365 {
			days = 365
		}
		trend, err := queryPerformanceTrend(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(trend)
	})
}

func queryPerformanceTrend(ctx context.Context, client influxClient, days int) ([]garmin.Performance, error) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	sql := fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time DESC",
		influx.MeasurementPerformance, start.Format(time.RFC3339),
	)
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
