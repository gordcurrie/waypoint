// Command waypoint is a CLI for querying and analyzing Garmin fitness data.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/gordcurrie/waypoint/internal/influx"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return usage()
	}

	// Validate subcommand before connecting to InfluxDB so unknown commands
	// show usage rather than an infrastructure error.
	switch os.Args[1] {
	case "status", "analyze", "plan":
	default:
		return usage()
	}

	client, err := influx.NewFromEnv()
	if err != nil {
		return fmt.Errorf("influx: %w", err)
	}
	defer func() { _ = client.Close() }()

	switch os.Args[1] {
	case "status":
		return runStatus(client)
	case "analyze":
		period := "week"
		if len(os.Args) >= 3 {
			period = os.Args[2]
		}
		return runAnalyze(client, period)
	case "plan":
		weeks := 4
		if len(os.Args) >= 3 {
			n, parseErr := strconv.Atoi(os.Args[2])
			if parseErr != nil {
				return fmt.Errorf("plan: invalid weeks %q", os.Args[2])
			}
			weeks = n
		}
		return runPlan(client, weeks)
	default:
		return usage()
	}
}

func usage() error {
	fmt.Fprintln(os.Stderr, "usage: waypoint <command> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "commands:")
	fmt.Fprintln(os.Stderr, "  status               show current ATL/CTL/TSB and training readiness")
	fmt.Fprintln(os.Stderr, "  analyze [week|month]  AI analysis of recent training")
	fmt.Fprintln(os.Stderr, "  plan [weeks]          generate a training plan (default: 4 weeks)")
	return fmt.Errorf("unknown command")
}
