package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/analysis"
	"github.com/gordcurrie/waypoint/internal/garmin"
	"github.com/gordcurrie/waypoint/internal/influx"
)

func registerTrainingTools(s *mcp.Server, client influxClient) {
	type statusInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 14"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_training_status",
		Description: "Return Garmin training status including overall status (0=overreaching … 5=peaking), " +
			"VO2max estimates for running and cycling, and Garmin fitness age.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
		days := clampDays(input.Days, 14, 365)
		status, err := queryTrainingStatus(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(status)
	})

	type trainingLoadInput struct {
		WindowDays int  `json:"window_days,omitempty" jsonschema:"days of ATL/CTL/TSB history to return, default 42"`
		WriteBack  bool `json:"write_back,omitempty"  jsonschema:"if true, persist results to the training_load measurement for Grafana"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_training_load",
		Description: "Compute ATL (acute training load, 7-day EMA), CTL (chronic training load, 42-day EMA), " +
			"and TSB (training stress balance = CTL - ATL) from activity data. " +
			"Set write_back=true to persist results to InfluxDB for Grafana.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input trainingLoadInput) (*mcp.CallToolResult, any, error) {
		windowDays := input.WindowDays
		if windowDays <= 0 {
			windowDays = 42
		}

		results, err := analysis.Compute(ctx, client, windowDays)
		if err != nil {
			return errorResult(fmt.Errorf("get_training_load: %w", err))
		}

		if input.WriteBack {
			if werr := analysis.WriteResults(ctx, client, results); werr != nil {
				type response struct {
					Results    []analysis.Result `json:"results"`
					WriteError string            `json:"write_error"`
				}
				return jsonResult(response{Results: results, WriteError: werr.Error()})
			}
		}

		return jsonResult(results)
	})

	type readinessInput struct {
		Days int `json:"days,omitempty" jsonschema:"lookback window in days, default 7"`
	}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_training_readiness",
		Description: "Return Garmin training readiness scores including HRV status, sleep score, and acute/chronic workload ratio.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input readinessInput) (*mcp.CallToolResult, any, error) {
		days := clampDays(input.Days, 7, 365)
		readiness, err := queryTrainingReadiness(ctx, client, days)
		if err != nil {
			return errorResult(err)
		}
		return jsonResult(readiness)
	})
}

func queryTrainingStatus(ctx context.Context, client influxClient, days int) ([]garmin.TrainingStatus, error) {
	sql := timeRangeQuery(influx.MeasurementTrainingStatus, days, "DESC")
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_training_status: %w", err)
	}
	result := make([]garmin.TrainingStatus, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.TrainingStatusFrom(row))
	}
	return result, nil
}

func queryTrainingReadiness(ctx context.Context, client influxClient, days int) ([]garmin.TrainingReadiness, error) {
	sql := timeRangeQuery(influx.MeasurementTrainingReadiness, days, "DESC")
	rows, err := client.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("get_training_readiness: %w", err)
	}
	result := make([]garmin.TrainingReadiness, 0, len(rows))
	for _, row := range rows {
		result = append(result, garmin.TrainingReadinessFrom(row))
	}
	return result, nil
}
