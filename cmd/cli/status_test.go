package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gordcurrie/waypoint/internal/influx"
)

func fakeInfluxWithData(t *testing.T, rows []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rows)
	}))
}

func TestQueryReadiness_empty(t *testing.T) {
	srv := fakeInflux(t)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")

	client, err := influx.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	got, err := queryReadiness(context.Background(), client, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d rows", len(got))
	}
}

func TestQueryReadiness_mapsFields(t *testing.T) {
	rows := []map[string]any{
		{
			"time":             "2026-07-20T00:00:00Z",
			"score":            85.0,
			"hrv_status":       2.0,
			"sleep_score":      78.0,
			"recovery_time_h":  4.0,
			"acw_ratio":        1.1,
		},
	}
	srv := fakeInfluxWithData(t, rows)
	defer srv.Close()
	t.Setenv("INFLUXDB_URL", srv.URL)
	t.Setenv("INFLUXDB_DATABASE", "test")

	client, err := influx.NewFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	got, err := queryReadiness(context.Background(), client, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got))
	}
	r := got[0]
	if r.Score != 85.0 {
		t.Errorf("Score: got %.1f, want 85.0", r.Score)
	}
	if r.HRVStatus != 2.0 {
		t.Errorf("HRVStatus: got %.1f, want 2.0", r.HRVStatus)
	}
	if r.SleepScore != 78.0 {
		t.Errorf("SleepScore: got %.1f, want 78.0", r.SleepScore)
	}
	if r.RecoveryTimeH != 4.0 {
		t.Errorf("RecoveryTimeH: got %.1f, want 4.0", r.RecoveryTimeH)
	}
}
