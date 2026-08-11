package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// activityDetailMockClient routes the activityTimeWindow lookup (a query against
// the "activity" measurement) to a single row for activityID at activityTime, and
// everything else (the actual detail-measurement query) to detailRows/detailErr —
// #95's fix means every detail query now issues two queries, not one.
func activityDetailMockClient(
	activityID int64, activityTime time.Time, detailRows []map[string]any, detailErr error,
) *mockClient {
	return &mockClient{
		queryFn: func(_ context.Context, sql string) ([]map[string]any, error) {
			if strings.Contains(sql, "FROM activity WHERE") {
				return []map[string]any{
					{"activity_id": fmt.Sprintf("%d", activityID), "time": activityTime.Format(time.RFC3339)},
				}, nil
			}
			return detailRows, detailErr
		},
	}
}

// activityNotFoundMockClient simulates the activity lookup itself finding nothing.
func activityNotFoundMockClient() *mockClient {
	return &mockClient{
		queryFn: func(_ context.Context, sql string) ([]map[string]any, error) {
			if strings.Contains(sql, "FROM activity WHERE") {
				return nil, nil
			}
			return nil, errors.New("detail query should not run when the activity lookup finds nothing")
		},
	}
}

func TestQueryActivitySplits_Empty(t *testing.T) {
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, nil)
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
	client := activityDetailMockClient(123456, now, []map[string]any{
		{"activity_id": "123456", "lap_index": float64(1), "time": now.Format(time.RFC3339), "distance_m": float64(1000), "duration_s": float64(360)},
		{"activity_id": "123456", "lap_index": float64(2), "time": now.Add(6 * time.Minute).Format(time.RFC3339), "distance_m": float64(1000), "duration_s": float64(355)},
	}, nil)
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
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, errors.New("connection refused"))
	_, err := queryActivitySplits(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestQueryActivitySplits_ActivityNotFound(t *testing.T) {
	client := activityNotFoundMockClient()
	_, err := queryActivitySplits(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error when the activity itself can't be found to bound the query")
	}
}

func TestQueryActivityHRZones_Empty(t *testing.T) {
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, nil)
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
	client := activityDetailMockClient(123456, now, []map[string]any{
		{
			"activity_id": "123456",
			"time":        now.Format(time.RFC3339),
			"z1_s":        float64(1200),
			"z2_s":        float64(2400),
			"z3_s":        float64(600),
			"z4_s":        float64(180),
			"z5_s":        float64(0),
		},
	}, nil)
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
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, errors.New("timeout"))
	_, err := queryActivityHRZones(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestQueryActivityExerciseSets_Empty(t *testing.T) {
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, nil)
	sets, err := queryActivityExerciseSets(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 0 {
		t.Errorf("want 0 sets, got %d", len(sets))
	}
}

func TestQueryActivityExerciseSets_ReturnsSets(t *testing.T) {
	now := time.Now().UTC()
	client := activityDetailMockClient(123456, now, []map[string]any{
		{
			"activity_id": "123456", "set_index": "0", "time": now.Format(time.RFC3339),
			"category": "LUNGE", "exercise_name": "LUNGE", "duration_s": float64(40), "reps": float64(10), "set_type": "ACTIVE",
		},
		{
			"activity_id": "123456", "set_index": "1", "time": now.Add(40 * time.Second).Format(time.RFC3339),
			"duration_s": float64(20), "set_type": "REST",
		},
	}, nil)
	sets, err := queryActivityExerciseSets(context.Background(), client, 123456)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 {
		t.Fatalf("want 2 sets, got %d", len(sets))
	}
	if sets[0].Category != "LUNGE" || sets[0].SetType != "ACTIVE" {
		t.Errorf("first set: got %+v", sets[0])
	}
	if sets[1].Category != "" || sets[1].SetType != "REST" {
		t.Errorf("rest set should have no category: got %+v", sets[1])
	}
}

func TestQueryActivityExerciseSets_PropagatesError(t *testing.T) {
	client := activityDetailMockClient(123456, time.Now().UTC(), nil, errors.New("timeout"))
	_, err := queryActivityExerciseSets(context.Background(), client, 123456)
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestActivityTimeWindow_QueriesAreBoundedByTime(t *testing.T) {
	// #95: every query this package issues for a specific activity_id must also
	// carry a time bound — an unbounded activity_id-only filter forces InfluxDB 3
	// Core to scan every Parquet file in the table's history, which fails once
	// the file count passes its scan limit (hit live at 432 files, 2026-08-09).
	now := time.Now().UTC()
	var sqls []string
	client := &mockClient{
		queryFn: func(_ context.Context, sql string) ([]map[string]any, error) {
			sqls = append(sqls, sql)
			if strings.Contains(sql, "FROM activity WHERE") {
				return []map[string]any{{"activity_id": "123456", "time": now.Format(time.RFC3339)}}, nil
			}
			return nil, nil
		},
	}
	if _, err := queryActivitySplits(context.Background(), client, 123456); err != nil {
		t.Fatal(err)
	}
	if len(sqls) != 2 {
		t.Fatalf("want 2 queries (activity lookup + bounded detail query), got %d: %v", len(sqls), sqls)
	}
	for _, sql := range sqls {
		if !strings.Contains(sql, "time >=") {
			t.Errorf("query has no time lower bound: %s", sql)
		}
	}
}
