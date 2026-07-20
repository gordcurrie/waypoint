package garmin

import (
	"testing"
	"time"
)

func TestFloatFrom_Present(t *testing.T) {
	row := map[string]any{"v": float64(3.14)}
	if got := floatFrom(row, "v"); got != 3.14 {
		t.Errorf("got %v, want 3.14", got)
	}
}

func TestFloatFrom_Int64(t *testing.T) {
	row := map[string]any{"v": int64(7)}
	if got := floatFrom(row, "v"); got != 7.0 {
		t.Errorf("got %v, want 7.0", got)
	}
}

func TestFloatFrom_Missing(t *testing.T) {
	if got := floatFrom(map[string]any{}, "v"); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestFloatFrom_Nil(t *testing.T) {
	row := map[string]any{"v": nil}
	if got := floatFrom(row, "v"); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestInt64From_Float64(t *testing.T) {
	// JSON decodes all numbers as float64; int64From must handle that.
	row := map[string]any{"id": float64(1234567890123456)}
	if got := int64From(row, "id"); got != 1234567890123456 {
		t.Errorf("got %v, want 1234567890123456", got)
	}
}

func TestInt64From_NativeInt64(t *testing.T) {
	row := map[string]any{"id": int64(42)}
	if got := int64From(row, "id"); got != 42 {
		t.Errorf("got %v, want 42", got)
	}
}

func TestInt64From_Missing(t *testing.T) {
	if got := int64From(map[string]any{}, "id"); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestStringFrom_Present(t *testing.T) {
	row := map[string]any{"sport": "running"}
	if got := stringFrom(row, "sport"); got != "running" {
		t.Errorf("got %q, want %q", got, "running")
	}
}

func TestStringFrom_Missing(t *testing.T) {
	if got := stringFrom(map[string]any{}, "sport"); got != "" {
		t.Errorf("got %q, want %q", got, "")
	}
}

func TestStringFrom_WrongType(t *testing.T) {
	// Non-string value must return "" rather than panicking or returning garbage.
	row := map[string]any{"sport": float64(42)}
	if got := stringFrom(row, "sport"); got != "" {
		t.Errorf("got %q, want %q for non-string value", got, "")
	}
}

func TestFloatPtrFrom_Present(t *testing.T) {
	row := map[string]any{"status": float64(2)}
	got := floatPtrFrom(row, "status")
	if got == nil || *got != 2 {
		t.Errorf("got %v, want 2.0", got)
	}
}

func TestFloatPtrFrom_Zero(t *testing.T) {
	// 0.0 is a valid sentinel (e.g. POOR HRV) — must return non-nil pointer to 0.
	row := map[string]any{"status": float64(0)}
	got := floatPtrFrom(row, "status")
	if got == nil || *got != 0 {
		t.Errorf("got %v, want pointer to 0.0", got)
	}
}

func TestFloatPtrFrom_Missing(t *testing.T) {
	got := floatPtrFrom(map[string]any{}, "status")
	if got != nil {
		t.Errorf("got %v, want nil for absent field", got)
	}
}

func TestFloatPtrFrom_Nil(t *testing.T) {
	row := map[string]any{"status": nil}
	got := floatPtrFrom(row, "status")
	if got != nil {
		t.Errorf("got %v, want nil for nil value", got)
	}
}

func TestTimeFrom_RFC3339Nano(t *testing.T) {
	row := map[string]any{"time": "2026-07-06T10:30:00.000000000Z"}
	want := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)
	if got := timeFrom(row, "time"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFrom_RFC3339(t *testing.T) {
	row := map[string]any{"time": "2026-07-06T10:30:00Z"}
	want := time.Date(2026, 7, 6, 10, 30, 0, 0, time.UTC)
	if got := timeFrom(row, "time"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFrom_NanosEpoch(t *testing.T) {
	ns := int64(1_700_000_000_000_000_000)
	row := map[string]any{"time": float64(ns)}
	want := time.Unix(0, ns).UTC()
	if got := timeFrom(row, "time"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFrom_NoTimezone(t *testing.T) {
	// InfluxDB 3 Core returns timestamps without a timezone suffix.
	// Must parse as UTC, not return zero time.
	row := map[string]any{"time": "2026-07-20T00:00:00"}
	want := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if got := timeFrom(row, "time"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFrom_NoTimezoneWithSubseconds(t *testing.T) {
	row := map[string]any{"time": "2026-07-20T14:52:18.123456789"}
	want := time.Date(2026, 7, 20, 14, 52, 18, 123456789, time.UTC)
	if got := timeFrom(row, "time"); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestTimeFrom_Missing(t *testing.T) {
	if got := timeFrom(map[string]any{}, "time"); !got.IsZero() {
		t.Errorf("got %v, want zero time", got)
	}
}
