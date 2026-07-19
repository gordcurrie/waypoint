package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryTrainingReadiness_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	readiness, err := queryTrainingReadiness(context.Background(), client, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness) != 0 {
		t.Errorf("want 0 readiness records, got %d", len(readiness))
	}
}

func TestQueryTrainingReadiness_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "score": 74.0, "hrv_status": 2.0, "sleep_score": 78.0},
		},
	}
	readiness, err := queryTrainingReadiness(context.Background(), client, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(readiness) != 1 {
		t.Fatalf("want 1 readiness record, got %d", len(readiness))
	}
	if readiness[0].Score != 74 {
		t.Errorf("score: got %g, want 74", readiness[0].Score)
	}
}

func TestQueryTrainingReadiness_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryTrainingReadiness(context.Background(), client, 7)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}
