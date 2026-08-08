// Command mcp-server is an MCP server exposing Garmin fitness data from InfluxDB.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/gordcurrie/waypoint/internal/analysis"
	"github.com/gordcurrie/waypoint/internal/influx"
	"github.com/gordcurrie/waypoint/tools"
)

var version = "dev"

// serverInstructions is sent to clients on initialize, giving a cold-start
// overview of what data is available and how tools relate to each other —
// most MCP clients surface this automatically without an extra round-trip,
// unlike a resource or prompt the client would have to fetch explicitly.
const serverInstructions = `Waypoint exposes mostly read-only Garmin Connect fitness data (synced to InfluxDB). Two tools have side effects: create_workout always queues a workout for upload, and get_training_load writes computed results back to InfluxDB when called with write_back=true (in --transport=http mode, a background loop also does this write on its own fixed interval regardless of any tool call — see -training-load-interval).

Data domains and their tools:
- Activities: get_recent_activities (list), get_weekly_volume (aggregated by sport/week). Use an activity's activity_id from get_recent_activities with get_activity_splits (per-lap) and get_activity_hr_zones (time in HR zone) for detail on one activity.
- Daily health: get_daily_stats (steps, resting HR, body battery, stress), get_sleep_summary, get_hrv_trend, get_respiration.
- Training status: get_training_status (Garmin's own overreaching/peaking status + VO2max), get_training_readiness (day-to-day readiness score, informed by HRV/sleep — see get_hrv_trend/get_sleep_summary for the underlying detail), get_training_load (computed ATL/CTL/TSB from activity data, not a Garmin field; write_back=true persists it on demand).
- Longer-term fitness: get_performance_trend (VO2max/fitness age over months), get_lactate_threshold.
- Workouts: get_scheduled_workouts (check the calendar before scheduling to avoid conflicts) and create_workout (queues a workout for upload on the next sync run). For any strength_training step, call search_exercises first to get a valid category/exercise_name pair — free-text guesses are rejected.

When in doubt about which tool answers "what training data exists for date X", start with get_recent_activities and get_daily_stats — most other tools narrow or aggregate from there.`

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	var transport, addr, dataDir string
	var trainingLoadInterval time.Duration
	flag.StringVar(&transport, "transport", "stdio", "transport: stdio or http")
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address for http transport")
	flag.StringVar(&dataDir, "data-dir", "./data", "directory for the workout queue shared with the sync sidecar")
	flag.DurationVar(&trainingLoadInterval, "training-load-interval", 30*time.Minute,
		"how often to recompute and persist training_load in http transport mode (0 disables). No effect in stdio mode.")
	flag.Parse()

	client, err := influx.NewFromEnv()
	if err != nil {
		return fmt.Errorf("influx client: %w", err)
	}
	defer func() { _ = client.Close() }()

	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: version}, &mcp.ServerOptions{
		Instructions: serverInstructions,
	})
	tools.RegisterAll(s, client, dataDir)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch transport {
	case "stdio":
		if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("stdio: %w", err)
		}
	case "http":
		handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server { return s }, nil)
		httpServer := &http.Server{
			Addr:              addr,
			Handler:           http.MaxBytesHandler(handler, 4<<20),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
		}
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // parent ctx is done; need fresh context for graceful shutdown
				slog.Error("shutdown", "err", err)
			}
		}()
		if trainingLoadInterval > 0 {
			go runTrainingLoadLoop(ctx, client, trainingLoadInterval)
		}
		slog.Warn("HTTP transport has no authentication — bind to localhost or protect with a reverse proxy")
		slog.Info("listening", "addr", addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http: %w", err)
		}
	default:
		return fmt.Errorf("unknown transport %q: use stdio or http", transport)
	}
	return nil
}

// trainingLoadClient is the subset of *influx.Client that analysis.Compute and
// analysis.WriteResults need, kept local so runTrainingLoadLoop can be tested
// against a fake without touching a real InfluxDB.
type trainingLoadClient interface {
	Query(ctx context.Context, sql string) ([]map[string]any, error)
	WritePoints(ctx context.Context, points ...*influx.Point) error
}

// runTrainingLoadLoop periodically recomputes and persists ATL/CTL/TSB so the
// training_load Grafana panel doesn't depend on an MCP client having called
// get_training_load with write_back=true (#90) — a dashboard-first workflow has no
// such caller, and the panel was found 4 days stale in practice. Runs once
// immediately, then on the given interval, only in the long-lived http-transport
// process: stdio mode is spawned fresh per Claude session and never lives long
// enough for a background loop to matter.
func runTrainingLoadLoop(ctx context.Context, client trainingLoadClient, interval time.Duration) {
	const windowDays = 42

	compute := func() {
		results, err := analysis.Compute(ctx, client, windowDays)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				slog.Error("training_load: background compute failed", "err", err)
			}
			return
		}
		if err := analysis.WriteResults(ctx, client, results); err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				slog.Error("training_load: background write failed", "err", err)
			}
		}
	}

	compute()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			compute()
		}
	}
}
