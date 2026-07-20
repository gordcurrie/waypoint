package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// fakeInflux returns a test server that responds with an empty JSON array for
// any request — sufficient for command-dispatch tests that don't reach query logic.
func fakeInflux(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]any{})
	}))
}

func withArgs(t *testing.T, args ...string) func() {
	t.Helper()
	old := os.Args
	os.Args = append([]string{"waypoint"}, args...)
	return func() { os.Args = old }
}

func TestRun_noArgs(t *testing.T) {
	defer withArgs(t)()

	err := run()
	if err == nil || err.Error() != "unknown command" {
		t.Errorf("got %v, want 'unknown command'", err)
	}
}

func TestRun_unknownCommand_noInfluxNeeded(t *testing.T) {
	// Unknown command must return usage error BEFORE attempting influx connection.
	// INFLUXDB_URL intentionally unset — if influx is contacted, the test will fail
	// with an influx error rather than "unknown command".
	t.Setenv("INFLUXDB_URL", "")
	defer withArgs(t, "badcmd")()

	err := run()
	if err == nil || err.Error() != "unknown command" {
		t.Errorf("got %v, want 'unknown command' (not an influx error)", err)
	}
}

func TestRun_planInvalidWeeks_notANumber(t *testing.T) {
	srv := fakeInflux(t)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")
	defer withArgs(t, "plan", "4abc")()

	err := run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid weeks") {
		t.Errorf("got %v, want 'invalid weeks' error", err)
	}
}

func TestRun_planZeroWeeks(t *testing.T) {
	srv := fakeInflux(t)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")
	defer withArgs(t, "plan", "0")()

	err := run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "1") {
		t.Errorf("got %v, want error mentioning valid range", err)
	}
}

func TestRun_plan53Weeks(t *testing.T) {
	srv := fakeInflux(t)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")
	defer withArgs(t, "plan", "53")()

	err := run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "52") {
		t.Errorf("got %v, want error mentioning max weeks", err)
	}
}

func TestRun_analyzeInvalidPeriod(t *testing.T) {
	srv := fakeInflux(t)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")
	defer withArgs(t, "analyze", "quarterly")()

	err := run()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "quarterly") {
		t.Errorf("got %v, want error mentioning invalid period", err)
	}
}
