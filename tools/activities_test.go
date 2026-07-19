package tools

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/influx"
)

// mockClient implements influxClient for testing.
type mockClient struct {
	rows []map[string]any
	err  error
}

func (m *mockClient) Query(_ context.Context, _ string) ([]map[string]any, error) {
	return m.rows, m.err
}

func (m *mockClient) WritePoints(_ context.Context, _ ...*influx.Point) error {
	return m.err
}

func TestQueryActivities_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	activities, err := queryActivities(context.Background(), client, 7, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 0 {
		t.Errorf("want 0 activities, got %d", len(activities))
	}
}

func TestQueryActivities_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "sport": "running", "activity_id": int64(1), "distance_m": 5000.0},
			{"time": now.Add(-time.Hour).Format(time.RFC3339), "sport": "cycling", "activity_id": int64(2), "distance_m": 20000.0},
		},
	}
	activities, err := queryActivities(context.Background(), client, 7, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(activities) != 2 {
		t.Fatalf("want 2 activities, got %d", len(activities))
	}
	if activities[0].Sport != "running" {
		t.Errorf("first activity sport: got %q, want running", activities[0].Sport)
	}
}

func TestQueryActivities_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("connection refused")}
	_, err := queryActivities(context.Background(), client, 7, 10, "")
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestISOWeekMonday(t *testing.T) {
	tests := []struct {
		in   string // RFC3339 date
		want string // expected Monday
	}{
		{"2026-07-14T10:00:00Z", "2026-07-13"}, // Tuesday → Monday 2026-07-13
		{"2026-07-13T10:00:00Z", "2026-07-13"}, // Monday → same day
		{"2026-07-19T10:00:00Z", "2026-07-13"}, // Sunday → Monday 2026-07-13
		{"2026-07-20T10:00:00Z", "2026-07-20"}, // Monday → same day
	}
	for _, tt := range tests {
		ts, err := time.Parse(time.RFC3339, tt.in)
		if err != nil {
			t.Fatalf("parse %s: %v", tt.in, err)
		}
		if got := isoWeekMonday(ts); got != tt.want {
			t.Errorf("isoWeekMonday(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestQueryWeeklyVolume_AggregatesBySportAndWeek(t *testing.T) {
	monday := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC) // a Monday
	tuesday := monday.AddDate(0, 0, 1)

	client := &mockClient{
		rows: []map[string]any{
			{"time": monday.Format(time.RFC3339), "sport": "running", "distance_m": 5000.0, "duration_s": 1800.0, "training_load": 50.0},
			{"time": tuesday.Format(time.RFC3339), "sport": "running", "distance_m": 8000.0, "duration_s": 2700.0, "training_load": 80.0},
			{"time": monday.Format(time.RFC3339), "sport": "cycling", "distance_m": 30000.0, "duration_s": 3600.0, "training_load": 90.0},
		},
	}

	vol, err := queryWeeklyVolume(context.Background(), client, 2)
	if err != nil {
		t.Fatal(err)
	}

	// Find running entry for week of 2026-07-13
	var running *WeeklyVolume
	for i := range vol {
		if vol[i].Sport == "running" && vol[i].WeekStart == "2026-07-13" {
			running = &vol[i]
		}
	}
	if running == nil {
		t.Fatal("missing running entry for week 2026-07-13")
	}
	if running.Count != 2 {
		t.Errorf("running count: got %d, want 2", running.Count)
	}
	if running.DistanceM != 13000 {
		t.Errorf("running distance: got %g, want 13000", running.DistanceM)
	}
}
