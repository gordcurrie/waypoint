package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryDailyStats_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	stats, err := queryDailyStats(context.Background(), client, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Errorf("want 0 stats, got %d", len(stats))
	}
}

func TestQueryDailyStats_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "steps": 8500.0, "resting_hr_bpm": 52.0, "body_battery_max": 85.0},
		},
	}
	stats, err := queryDailyStats(context.Background(), client, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("want 1 stat, got %d", len(stats))
	}
	if stats[0].Steps != 8500 {
		t.Errorf("steps: got %g, want 8500", stats[0].Steps)
	}
}

func TestQueryDailyStats_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryDailyStats(context.Background(), client, 7)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestQuerySleep_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "total_sleep_s": 27000.0, "sleep_score": 78.0},
		},
	}
	sleep, err := querySleep(context.Background(), client, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(sleep) != 1 {
		t.Fatalf("want 1 sleep record, got %d", len(sleep))
	}
	if sleep[0].SleepScore != 78 {
		t.Errorf("sleep score: got %g, want 78", sleep[0].SleepScore)
	}
}

func TestQueryHRV_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "weekly_avg_ms": 42.0, "last_night_ms": 38.0},
		},
	}
	hrv, err := queryHRV(context.Background(), client, 14)
	if err != nil {
		t.Fatal(err)
	}
	if len(hrv) != 1 {
		t.Fatalf("want 1 HRV record, got %d", len(hrv))
	}
	if hrv[0].WeeklyAvgMS != 42 {
		t.Errorf("weekly avg: got %g, want 42", hrv[0].WeeklyAvgMS)
	}
}
