package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryPerformanceTrend_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	trend, err := queryPerformanceTrend(context.Background(), client, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 0 {
		t.Errorf("want 0 trend records, got %d", len(trend))
	}
}

func TestQueryPerformanceTrend_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "vo2max": float64(47), "fitness_age": float64(34)},
		},
	}
	trend, err := queryPerformanceTrend(context.Background(), client, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 1 {
		t.Fatalf("want 1 trend record, got %d", len(trend))
	}
	if trend[0].VO2Max == nil || *trend[0].VO2Max != 47 {
		t.Errorf("vo2max: got %v, want 47", trend[0].VO2Max)
	}
	if trend[0].FitnessAge == nil || *trend[0].FitnessAge != 34 {
		t.Errorf("fitness_age: got %v, want 34", trend[0].FitnessAge)
	}
}

func TestQueryPerformanceTrend_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryPerformanceTrend(context.Background(), client, 90)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestQueryLactateThreshold_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	trend, err := queryLactateThreshold(context.Background(), client, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 0 {
		t.Errorf("want 0 trend records, got %d", len(trend))
	}
}

func TestQueryLactateThreshold_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "lt_hr_bpm": float64(168), "lt_pace_s_per_km": float64(272)},
		},
	}
	trend, err := queryLactateThreshold(context.Background(), client, 90)
	if err != nil {
		t.Fatal(err)
	}
	if len(trend) != 1 {
		t.Fatalf("want 1 trend record, got %d", len(trend))
	}
	if trend[0].LTHeartRate != 168 {
		t.Errorf("lt_hr_bpm: got %g, want 168", trend[0].LTHeartRate)
	}
	if trend[0].LTPaceSPerKM != 272 {
		t.Errorf("lt_pace_s_per_km: got %g, want 272", trend[0].LTPaceSPerKM)
	}
}

func TestQueryLactateThreshold_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryLactateThreshold(context.Background(), client, 90)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
