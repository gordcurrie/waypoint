package main

import (
	"strings"
	"testing"
	"time"

	"github.com/gordcurrie/waypoint/internal/analysis"
	"github.com/gordcurrie/waypoint/internal/garmin"
)

var testTime = time.Date(2026, 7, 14, 0, 0, 0, 0, time.UTC)

func TestBuildAnalyzePrompt_emptyData(t *testing.T) {
	d := &trainingData{}
	got := buildAnalyzePrompt("week", 7, d)
	if !strings.Contains(got, "week") || !strings.Contains(got, "7 days") {
		t.Errorf("missing header info in:\n%s", got)
	}
}

func TestBuildAnalyzePrompt_withLoad(t *testing.T) {
	d := &trainingData{
		load: []analysis.Result{
			{Date: testTime, ATL: 45.5, CTL: 50.0, TSB: -4.5},
		},
	}
	got := buildAnalyzePrompt("week", 7, d)
	for _, want := range []string{"TRAINING LOAD", "45.5", "50.0", "-4.5"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in prompt:\n%s", want, got)
		}
	}
}

func TestBuildAnalyzePrompt_usesLastLoadEntry(t *testing.T) {
	// Compute returns results in ascending date order; last entry is today.
	d := &trainingData{
		load: []analysis.Result{
			{Date: testTime.AddDate(0, 0, -6), ATL: 10.0, CTL: 20.0, TSB: -10.0},
			{Date: testTime, ATL: 45.5, CTL: 50.0, TSB: -4.5},
		},
	}
	got := buildAnalyzePrompt("week", 7, d)
	if strings.Contains(got, "10.0") {
		t.Error("prompt used first (oldest) load entry instead of last (today)")
	}
	if !strings.Contains(got, "45.5") {
		t.Error("prompt missing today's ATL value")
	}
}

func TestBuildAnalyzePrompt_withActivities(t *testing.T) {
	d := &trainingData{
		activities: []garmin.Activity{
			{
				Time:         testTime,
				Sport:        "running",
				DistanceM:    10000,
				DurationS:    3600,
				AvgHRBPM:     145,
				MaxHRBPM:     170,
				TrainingLoad: 85,
			},
		},
	}
	got := buildAnalyzePrompt("week", 7, d)
	for _, want := range []string{"ACTIVITIES", "running", "10.0km", "60min", "145"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in prompt:\n%s", want, got)
		}
	}
}

func TestBuildAnalyzePrompt_HRVStatus(t *testing.T) {
	balanced := 2.0
	unbalanced := 1.0
	poor := 0.0
	lowUnbalanced := 3.0
	unknown := 99.0

	cases := []struct {
		status *float64
		want   string
	}{
		{&balanced, "balanced"},
		{&unbalanced, "unbalanced"},
		{&poor, "poor"},
		{&lowUnbalanced, "low-unbalanced"},
		{&unknown, "unknown"},
		{nil, "unknown"},
	}

	for _, tc := range cases {
		d := &trainingData{
			hrv: []garmin.HRV{{Date: testTime.Format("2006-01-02"), WeeklyAvgMS: 55, LastNightMS: 52, Status: tc.status}},
		}
		got := buildAnalyzePrompt("week", 7, d)
		if !strings.Contains(got, tc.want) {
			t.Errorf("status %v: missing %q in prompt", tc.status, tc.want)
		}
	}
}

func TestBuildAnalyzePrompt_month(t *testing.T) {
	d := &trainingData{}
	got := buildAnalyzePrompt("month", 30, d)
	if !strings.Contains(got, "month") || !strings.Contains(got, "30 days") {
		t.Errorf("missing month/30-day header in:\n%s", got)
	}
}

func TestBuildPlanPrompt_weeksInHeader(t *testing.T) {
	d := &trainingData{}
	got := buildPlanPrompt(8, d)
	if !strings.Contains(got, "8-week") {
		t.Errorf("missing week count in header:\n%s", got)
	}
}

func TestBuildPlanPrompt_historyDaysConsistent(t *testing.T) {
	d := &trainingData{}
	got := buildPlanPrompt(4, d)
	// planHistoryDays const controls both gatherData call and the prompt string.
	want := "28 days"
	if !strings.Contains(got, want) {
		t.Errorf("prompt header should say %q:\n%s", want, got)
	}
}

func TestBuildPlanPrompt_avgSleepScore(t *testing.T) {
	d := &trainingData{
		sleep: []garmin.Sleep{
			{SleepScore: 80},
			{SleepScore: 60},
		},
	}
	got := buildPlanPrompt(4, d)
	if !strings.Contains(got, "70") {
		t.Errorf("expected avg sleep score 70 in:\n%s", got)
	}
}

func TestBuildPlanPrompt_closingInstruction(t *testing.T) {
	d := &trainingData{}
	got := buildPlanPrompt(12, d)
	if !strings.Contains(got, "12-week plan") {
		t.Errorf("closing instruction missing week count:\n%s", got)
	}
}

func TestBuildPlanPrompt_includesHRV(t *testing.T) {
	balanced := 2.0
	d := &trainingData{
		hrv: []garmin.HRV{
			{Date: testTime.AddDate(0, 0, -6).Format("2006-01-02"), WeeklyAvgMS: 50, Status: nil},
			{Date: testTime.Format("2006-01-02"), WeeklyAvgMS: 60, Status: &balanced},
		},
	}
	got := buildPlanPrompt(4, d)
	for _, want := range []string{"HRV", "55", "balanced"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in plan prompt:\n%s", want, got)
		}
	}
}

func TestBuildPlanPrompt_includesBodyBattery(t *testing.T) {
	d := &trainingData{
		dailyStats: []garmin.DailyStats{
			{Date: testTime.AddDate(0, 0, -1).Format("2006-01-02"), BodyBatteryMax: 80},
			{Date: testTime.Format("2006-01-02"), BodyBatteryMax: 60},
		},
	}
	got := buildPlanPrompt(4, d)
	if !strings.Contains(got, "body battery") {
		t.Errorf("missing body battery in plan prompt:\n%s", got)
	}
	if !strings.Contains(got, "70") {
		t.Errorf("expected avg body battery 70 in plan prompt:\n%s", got)
	}
}
