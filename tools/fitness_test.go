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
	if trend[0].VO2Max != 47 {
		t.Errorf("vo2max: got %g, want 47", trend[0].VO2Max)
	}
	if trend[0].FitnessAge != 34 {
		t.Errorf("fitness_age: got %g, want 34", trend[0].FitnessAge)
	}
}

func TestQueryPerformanceTrend_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryPerformanceTrend(context.Background(), client, 90)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
