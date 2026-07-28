package tools

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClampDays(t *testing.T) {
	tests := []struct {
		name           string
		days, def, max int
		want           int
	}{
		{"zero uses default", 0, 7, 365, 7},
		{"negative uses default", -5, 7, 365, 7},
		{"within range unchanged", 30, 7, 365, 30},
		{"exceeds max clamps to max", 400, 7, 365, 365},
		{"exactly max unchanged", 365, 7, 365, 365},
		{"exactly one unchanged", 1, 7, 365, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clampInt(tt.days, tt.def, tt.max)
			if got != tt.want {
				t.Errorf("clampInt(%d, %d, %d) = %d, want %d", tt.days, tt.def, tt.max, got, tt.want)
			}
		})
	}
}

func TestTimeRangeQuery(t *testing.T) {
	tests := []struct {
		name        string
		measurement string
		days        int
		order       string
	}{
		{"ascending order", "hrv", 14, "ASC"},
		{"descending order", "daily_stats", 7, "DESC"},
		{"zero day window", "performance", 0, "ASC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -tt.days)
			want := fmt.Sprintf(
				"SELECT * FROM %s WHERE time >= '%s' ORDER BY time %s",
				tt.measurement, start.Format(time.RFC3339), tt.order,
			)
			got := timeRangeQuery(tt.measurement, tt.days, tt.order)
			if got != want {
				t.Errorf("timeRangeQuery(%q, %d, %q) = %q, want %q", tt.measurement, tt.days, tt.order, got, want)
			}
		})
	}
}

func TestTimeRangeQuery_InvalidOrderPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("want panic for invalid order")
		}
	}()
	timeRangeQuery("hrv", 7, "SIDEWAYS")
}

func TestTextResult(t *testing.T) {
	result, _, err := textResult("hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("textResult must not be an error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(result.Content))
	}
}

func TestErrorResult(t *testing.T) {
	result, _, err := errorResult(errors.New("boom"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("errorResult must set IsError")
	}
}

func TestJSONResult(t *testing.T) {
	result, _, err := jsonResult(map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Error("jsonResult must not be an error")
	}
	if len(result.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(result.Content))
	}
}

func TestJSONResult_UnmarshalableInput(t *testing.T) {
	result, _, err := jsonResult(make(chan int))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Error("jsonResult with unmarshalable input must return IsError:true")
	}
}
