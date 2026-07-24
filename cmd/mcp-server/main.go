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

	s := mcp.NewServer(&mcp.Implementation{Name: "waypoint", Version: version}, nil)
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
