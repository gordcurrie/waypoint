package tools

import (
	"context"

	"github.com/gordcurrie/waypoint/internal/influx"
)

// influxClient is the interface the tools layer requires.
// *influx.Client satisfies this automatically.
type influxClient interface {
	Query(ctx context.Context, sql string) ([]map[string]any, error)
	WritePoints(ctx context.Context, points ...*influx.Point) error
}
