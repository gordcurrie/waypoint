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

	"github.com/gordcurrie/waypoint/internal/influx"
	"github.com/gordcurrie/waypoint/tools"
)

var version = "dev"

// serverInstructions is sent to clients on initialize, giving a cold-start
// overview of what data is available and how tools relate to each other —
// most MCP clients surface this automatically without an extra round-trip,
// unlike a resource or prompt the client would have to fetch explicitly.
const serverInstructions = `Waypoint exposes mostly read-only Garmin Connect fitness data (synced to InfluxDB). Two tools have side effects: create_workout always queues a workout for upload, and get_training_load writes computed results back to InfluxDB only when called with write_back=true.

Data domains and their tools:
- Activities: get_recent_activities (list), get_weekly_volume (aggregated by sport/week). Use an activity's activity_id from get_recent_activities with get_activity_splits (per-lap) and get_activity_hr_zones (time in HR zone) for detail on one activity.
- Daily health: get_daily_stats (steps, resting HR, body battery, stress), get_sleep_summary, get_hrv_trend, get_respiration.
- Training status: get_training_status (Garmin's own overreaching/peaking status + VO2max), get_training_readiness (day-to-day readiness score, informed by HRV/sleep — see get_hrv_trend/get_sleep_summary for the underlying detail), get_training_load (computed ATL/CTL/TSB from activity data, not a Garmin field; write_back=true persists it).
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
	flag.StringVar(&transport, "transport", "stdio", "transport: stdio or http")
	flag.StringVar(&addr, "addr", "127.0.0.1:8080", "listen address for http transport")
	flag.StringVar(&dataDir, "data-dir", "./data", "directory for the workout queue shared with the sync sidecar")
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
