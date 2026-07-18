package analysis

import (
	"testing"
	"time"
)

// baseDate is a fixed Monday used across tests.
var baseDate = time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC)

func TestCompute_NoLoad(t *testing.T) {
	start := baseDate.AddDate(0, 0, -9)
	results := compute(nil, start, baseDate, 7)

	if len(results) != 7 {
		t.Fatalf("want 7 results, got %d", len(results))
	}
	for _, r := range results {
		if r.ATL != 0 || r.CTL != 0 || r.TSB != 0 || r.Load != 0 {
			t.Errorf("zero load day %s: want all zeros, got ATL=%v CTL=%v TSB=%v Load=%v",
				r.Date.Format("2006-01-02"), r.ATL, r.CTL, r.TSB, r.Load)
		}
	}
}

func TestCompute_WindowSize(t *testing.T) {
	start := baseDate.AddDate(0, 0, -30)
	for _, w := range []int{1, 7, 14, 30} {
		results := compute(nil, start, baseDate, w)
		if len(results) != w {
			t.Errorf("windowDays=%d: want %d results, got %d", w, w, len(results))
		}
	}
}

func TestCompute_WindowDates(t *testing.T) {
	start := baseDate.AddDate(0, 0, -10)
	results := compute(nil, start, baseDate, 3)

	// Results should be baseDate-2, baseDate-1, baseDate
	want := []time.Time{
		baseDate.AddDate(0, 0, -2),
		baseDate.AddDate(0, 0, -1),
		baseDate,
	}
	for i, r := range results {
		if !r.Date.Equal(want[i]) {
			t.Errorf("result[%d]: want date %s, got %s", i, want[i].Format("2006-01-02"), r.Date.Format("2006-01-02"))
		}
	}
}

func TestCompute_EMASteps(t *testing.T) {
	// start=day-1, today=day0, windowDays=1 → windowStart=today → only today returned.
	//
	// day -1: load=70 → ATL = 0*(6/7) + 70*(1/7) = 10   (warmup, not in results)
	// day  0: load=0  → ATL = 10*(6/7) + 0 = 60/7       (in results)
	today := baseDate
	start := today.AddDate(0, 0, -1)

	dayLoads := map[string]float64{
		start.Format("2006-01-02"): 70,
	}
	results := compute(dayLoads, start, today, 1)

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if !r.Date.Equal(today) {
		t.Errorf("result date: want %s, got %s", today.Format("2006-01-02"), r.Date.Format("2006-01-02"))
	}

	wantATL := (70.0 / atlDays) * (1 - 1.0/atlDays) // 10 * (6/7) = 60/7
	if abs(r.ATL-wantATL) > 1e-9 {
		t.Errorf("ATL: want %.9f, got %.9f", wantATL, r.ATL)
	}

	wantCTL := (70.0 / ctlDays) * (1 - 1.0/ctlDays) // (70/42)*(41/42)
	if abs(r.CTL-wantCTL) > 1e-9 {
		t.Errorf("CTL: want %.9f, got %.9f", wantCTL, r.CTL)
	}

	if abs(r.TSB-(r.CTL-r.ATL)) > 1e-9 {
		t.Errorf("TSB invariant: CTL-ATL=%.9f, TSB=%.9f", r.CTL-r.ATL, r.TSB)
	}
}

func TestCompute_LoadRawPreserved(t *testing.T) {
	today := baseDate
	start := today

	dayLoads := map[string]float64{
		today.Format("2006-01-02"): 55.5,
	}
	results := compute(dayLoads, start, today, 1)

	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Load != 55.5 {
		t.Errorf("Load: want 55.5, got %v", results[0].Load)
	}
}

func TestCompute_MultipleDaysSameDay(t *testing.T) {
	// Two activities on the same day: their loads should sum in the caller (Compute),
	// but compute() itself just reads what dayLoads provides.
	today := baseDate
	start := today

	dayLoads := map[string]float64{
		today.Format("2006-01-02"): 100, // pre-summed
	}
	results := compute(dayLoads, start, today, 1)
	if results[0].Load != 100 {
		t.Errorf("want Load=100, got %v", results[0].Load)
	}
}

func TestCompute_ATLRisesBeforeCTL(t *testing.T) {
	// With constant high load, ATL (k=1/7) rises faster than CTL (k=1/42).
	// After 14 days of load=100, ATL > CTL → TSB < 0.
	start := baseDate.AddDate(0, 0, -20)
	today := baseDate

	dayLoads := make(map[string]float64)
	for i := 0; i < 14; i++ {
		d := today.AddDate(0, 0, -13+i)
		dayLoads[d.Format("2006-01-02")] = 100
	}

	results := compute(dayLoads, start, today, 1)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.ATL <= r.CTL {
		t.Errorf("after sustained high load, ATL=%.2f should exceed CTL=%.2f", r.ATL, r.CTL)
	}
	if r.TSB >= 0 {
		t.Errorf("after sustained high load, TSB=%.2f should be negative", r.TSB)
	}
}

func TestCompute_TSBInvariant(t *testing.T) {
	// TSB must always equal CTL - ATL.
	start := baseDate.AddDate(0, 0, -60)
	today := baseDate

	dayLoads := make(map[string]float64)
	for i := 0; i < 45; i++ {
		d := today.AddDate(0, 0, -44+i)
		dayLoads[d.Format("2006-01-02")] = float64(30 + i%40) // varying load
	}

	results := compute(dayLoads, start, today, 30)
	for _, r := range results {
		if abs(r.TSB-(r.CTL-r.ATL)) > 1e-9 {
			t.Errorf("date %s: TSB=%.9f but CTL-ATL=%.9f",
				r.Date.Format("2006-01-02"), r.TSB, r.CTL-r.ATL)
		}
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
