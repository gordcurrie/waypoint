package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryActivitySplits_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	laps, err := queryActivitySplits(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if len(laps) != 0 {
		t.Errorf("want 0 laps, got %d", len(laps))
	}
}

func TestQueryActivitySplits_ReturnsLaps(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"activity_id": "123456", "lap_index": float64(1), "time": now.Format(time.RFC3339), "distance_m": float64(1000), "duration_s": float64(360)},
			{"activity_id": "123456", "lap_index": float64(2), "time": now.Add(6 * time.Minute).Format(time.RFC3339), "distance_m": float64(1000), "duration_s": float64(355)},
		},
	}
	laps, err := queryActivitySplits(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if len(laps) != 2 {
		t.Fatalf("want 2 laps, got %d", len(laps))
	}
	if laps[0].LapIndex != 1 {
		t.Errorf("first lap index: got %d, want 1", laps[0].LapIndex)
	}
	if laps[1].LapIndex != 2 {
		t.Errorf("second lap index: got %d, want 2", laps[1].LapIndex)
	}
}

func TestQueryActivitySplits_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("connection refused")}
	_, err := queryActivitySplits(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestQueryActivityHRZones_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	zones, err := queryActivityHRZones(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if zones != nil {
		t.Errorf("want nil for missing activity, got %+v", zones)
	}
}

func TestQueryActivityHRZones_ReturnsZones(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{
				"activity_id": "123456",
				"time":        now.Format(time.RFC3339),
				"z1_s":        float64(1200),
				"z2_s":        float64(2400),
				"z3_s":        float64(600),
				"z4_s":        float64(180),
				"z5_s":        float64(0),
			},
		},
	}
	zones, err := queryActivityHRZones(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if zones == nil {
		t.Fatal("want zones, got nil")
	}
	if zones.Z1S != 1200 {
		t.Errorf("Z1S: got %v, want 1200", zones.Z1S)
	}
	if zones.Z2S != 2400 {
		t.Errorf("Z2S: got %v, want 2400", zones.Z2S)
	}
}

func TestQueryActivityHRZones_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryActivityHRZones(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
