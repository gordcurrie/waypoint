package tools

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestQueryTrainingStatus_Empty(t *testing.T) {
	client := &mockClient{rows: nil}
	status, err := queryTrainingStatus(context.Background(), client, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 0 {
		t.Errorf("want 0 status records, got %d", len(status))
	}
}

func TestQueryTrainingStatus_ReturnsRows(t *testing.T) {
	now := time.Now().UTC()
	statusNum := float64(3)
	client := &mockClient{
		rows: []map[string]any{
			{"time": now.Format(time.RFC3339), "status_num": statusNum, "vo2max_running": 47.0, "fitness_age": 34.0},
		},
	}
	status, err := queryTrainingStatus(context.Background(), client, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(status) != 1 {
		t.Fatalf("want 1 status record, got %d", len(status))
	}
	if status[0].StatusNum == nil || *status[0].StatusNum != 3 {
		t.Errorf("status_num: got %v, want 3", status[0].StatusNum)
	}
	if status[0].VO2MaxRunning != 47 {
		t.Errorf("vo2max_running: got %g, want 47", status[0].VO2MaxRunning)
	}
}

func TestQueryTrainingStatus_PropagatesError(t *testing.T) {
	client := &mockClient{err: errors.New("timeout")}
	_, err := queryTrainingStatus(context.Background(), client, 30)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

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
