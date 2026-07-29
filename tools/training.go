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
		Name:  "get_training_status",
		Title: "Training Status",
		Description: "Return Garmin's own training status including overall status (0=overreaching … 5=peaking), " +
			"VO2max estimates for running and cycling, and Garmin fitness age. " +
			"This is Garmin's assessment, not a computed value — for ATL/CTL/TSB computed from activity data see get_training_load.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input statusInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 14, 365)
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
		Name:  "get_training_load",
		Title: "Training Load (ATL/CTL/TSB)",
		Description: "Compute ATL (acute training load, 7-day EMA), CTL (chronic training load, 42-day EMA), " +
			"and TSB (training stress balance = CTL - ATL) from activity data. This is computed on demand, not a " +
			"Garmin-reported field — for Garmin's own status assessment see get_training_status. " +
			"Set write_back=true to also persist results to InfluxDB for Grafana; the tool always returns the computed values regardless.",
		// write_back=true performs an additive write (new points, no delete/mutate of
		// unrelated data) — not destructive, so DestructiveHint is explicit here rather
		// than left unset (which defaults to the conservative/destructive assumption per
		// MCP spec). IdempotentHint is left at its false default: repeated calls land on
		// the same InfluxDB points, but whether same-timestamp writes overwrite-in-place
		// vs. behave otherwise isn't verified for InfluxDB 3 Core specifically, so this
		// doesn't advertise a retry-safety guarantee it hasn't confirmed.
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false)},
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
		Name:  "get_training_readiness",
		Title: "Training Readiness",
		Description: "Return Garmin training readiness scores including HRV status, sleep score, and acute/chronic workload ratio (acw_pct). " +
			"For the underlying HRV/sleep detail behind this score see get_hrv_trend and get_sleep_summary.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input readinessInput) (*mcp.CallToolResult, any, error) {
		days := clampInt(input.Days, 7, 365)
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
