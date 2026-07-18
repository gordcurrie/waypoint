package influx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client talks to InfluxDB 3 Core over HTTP (no gRPC/Arrow Flight required).
// Writes use /api/v3/write_lp; queries use /api/v3/query_sql (returns JSON).
type Client struct {
	host     string
	token    string
	database string
	http     *http.Client
}

// New creates a Client from explicit config values.
// host must be a bare http:// or https:// URL with no path (e.g. "http://localhost:8181").
func New(host, token, database string) (*Client, error) {
	if host == "" {
		return nil, fmt.Errorf("influx.New: host is required")
	}
	if database == "" {
		return nil, fmt.Errorf("influx.New: database is required")
	}
	parsed, err := url.ParseRequestURI(host)
	if err != nil {
		return nil, fmt.Errorf("influx.New: invalid host URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("influx.New: host requires http or https scheme, got %q", parsed.Scheme)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("influx.New: host must not include a path, got %q; use scheme://host[:port] only", host)
	}
	return &Client{
		// Reconstruct from parsed components to eliminate SSRF taint.
		host:     parsed.Scheme + "://" + parsed.Host,
		token:    token,
		database: database,
		http:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// configFromEnv reads InfluxDB connection parameters from environment variables.
// It returns an error if INFLUXDB_URL is not set.
func configFromEnv() (host, token, database string, err error) {
	host = os.Getenv("INFLUXDB_URL")
	if host == "" {
		return "", "", "", fmt.Errorf("INFLUXDB_URL not set")
	}
	token = os.Getenv("INFLUXDB_TOKEN")
	database = os.Getenv("INFLUXDB_DATABASE")
	if database == "" {
		database = "garmin"
	}
	return host, token, database, nil
}

// NewFromEnv creates a Client from environment variables:
//   - INFLUXDB_URL      (required)
//   - INFLUXDB_TOKEN    (required for auth; empty string works with --without-auth)
//   - INFLUXDB_DATABASE (defaults to "garmin")
func NewFromEnv() (*Client, error) {
	host, token, database, err := configFromEnv()
	if err != nil {
		return nil, err
	}
	return New(host, token, database)
}

// Close is a no-op — the HTTP client holds no persistent connections that need cleanup.
func (c *Client) Close() error { return nil }

// Query executes a SQL query and returns all rows as maps.
func (c *Client) Query(ctx context.Context, sql string) ([]map[string]any, error) {
	return c.queryJSON(ctx, sql, nil)
}

// QueryWithParams executes a parameterized SQL query.
// Named parameters in the query (e.g. $start) are substituted from params.
func (c *Client) QueryWithParams(ctx context.Context, sql string, params map[string]any) ([]map[string]any, error) {
	return c.queryJSON(ctx, sql, params)
}

// WritePoints writes one or more Points to InfluxDB in line-protocol format.
func (c *Client) WritePoints(ctx context.Context, points ...*Point) error {
	if len(points) == 0 {
		return nil
	}
	var sb strings.Builder
	for i, p := range points {
		if p == nil {
			return fmt.Errorf("influx.WritePoints: point at index %d is nil", i)
		}
		lp := p.LineProtocol()
		if lp == "" {
			return fmt.Errorf("influx.WritePoints: point at index %d has empty measurement or no fields", i)
		}
		if i > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(lp)
	}
	return c.writeLineProtocol(ctx, sb.String())
}

func (c *Client) queryJSON(ctx context.Context, sql string, params map[string]any) ([]map[string]any, error) {
	payload := map[string]any{
		"q":      sql,
		"db":     c.database,
		"format": "json",
	}
	if len(params) > 0 {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("influx.Query marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.host+"/api/v3/query_sql", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("influx.Query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: URL built from config validated at New(); not from request input
	if err != nil {
		return nil, fmt.Errorf("influx.Query: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("influx.Query: status %d: %s", resp.StatusCode, data)
	}

	var rows []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, fmt.Errorf("influx.Query unmarshal: %w", err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

func (c *Client) writeLineProtocol(ctx context.Context, lp string) error {
	u := c.host + "/api/v3/write_lp?" + url.Values{"db": {c.database}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(lp))
	if err != nil {
		return fmt.Errorf("influx.Write request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: URL built from config validated at New(); not from request input
	if err != nil {
		return fmt.Errorf("influx.Write: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("influx.Write: status %d: %s", resp.StatusCode, body)
	}
	return nil
}
