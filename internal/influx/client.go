package influx

import (
	"context"
	"fmt"
	"os"

	"github.com/InfluxCommunity/influxdb3-go/v2/influxdb3"
)

// Client wraps influxdb3.Client with helpers tuned for this project's env vars and schema.
type Client struct {
	db *influxdb3.Client
}

// New creates a Client from explicit config values.
func New(host, token, database string) (*Client, error) {
	db, err := influxdb3.New(influxdb3.ClientConfig{
		Host:     host,
		Token:    token,
		Database: database,
	})
	if err != nil {
		return nil, fmt.Errorf("influx.New: %w", err)
	}
	return &Client{db: db}, nil
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

// Close releases resources held by the client.
func (c *Client) Close() error {
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("influx.Close: %w", err)
	}
	return nil
}

// Query executes a SQL query and returns all rows as maps.
// Callers should use the measurement constants in this package for query construction.
func (c *Client) Query(ctx context.Context, sql string) ([]map[string]any, error) {
	iter, err := c.db.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	return collectRows(iter)
}

// QueryWithParameters executes a parameterized SQL query and returns all rows as maps.
func (c *Client) QueryWithParameters(ctx context.Context, sql string, params influxdb3.QueryParameters) ([]map[string]any, error) {
	iter, err := c.db.QueryWithParameters(ctx, sql, params)
	if err != nil {
		return nil, fmt.Errorf("influx query: %w", err)
	}
	return collectRows(iter)
}

// WritePoints writes line-protocol points to InfluxDB.
func (c *Client) WritePoints(ctx context.Context, points ...*influxdb3.Point) error {
	if err := c.db.WritePoints(ctx, points); err != nil {
		return fmt.Errorf("influx write: %w", err)
	}
	return nil
}

func collectRows(iter *influxdb3.QueryIterator) ([]map[string]any, error) {
	var rows []map[string]any
	for iter.Next() {
		row := iter.Value()
		// Copy to avoid sharing the iterator's internal buffer.
		copied := make(map[string]any, len(row))
		for k, v := range row {
			copied[k] = v
		}
		rows = append(rows, copied)
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("influx iterator: %w", err)
	}
	return rows, nil
}
