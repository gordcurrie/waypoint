package tools

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// clampInt returns def when n <= 0, otherwise n clamped to max.
func clampInt(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// timeRangeQuery builds a "SELECT * FROM <measurement> WHERE time >= since ORDER BY time <order>"
// query over the window starting `days` ago. order must be "ASC" or "DESC"; panics otherwise,
// since order is always an internal constant, never user input.
func timeRangeQuery(measurement string, days int, order string) string {
	if order != "ASC" && order != "DESC" {
		panic(fmt.Sprintf("timeRangeQuery: order must be ASC or DESC, got %q", order))
	}
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -days)
	return fmt.Sprintf(
		"SELECT * FROM %s WHERE time >= '%s' ORDER BY time %s",
		measurement, start.Format(time.RFC3339), order,
	)
}

// jsonResult marshals v to compact JSON text content.
// Compact (not indented) reduces token usage for LLM consumers.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return errorResult(fmt.Errorf("marshal result: %w", err))
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
	}, nil, nil
}

// textResult wraps a plain string in a TextContent result.
func textResult(s string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}, nil, nil
}

// errorResult returns an isError:true tool result with the error message.
func errorResult(err error) (*mcp.CallToolResult, any, error) {
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}, nil, nil
}
