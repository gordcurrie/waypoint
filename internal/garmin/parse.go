package garmin

import (
	"math"
	"strconv"
	"time"
)

// roundF rounds f to 1 decimal place for compact JSON output.
func roundF(f float64) float64 {
	return math.Round(f*10) / 10
}

// roundFPtr rounds a *float64 to 1 decimal place, returning nil unchanged.
func roundFPtr(p *float64) *float64 {
	if p == nil {
		return nil
	}
	v := roundF(*p)
	return &v
}

// floatFrom extracts a float64 from a query row, returning 0 if absent or non-numeric.
func floatFrom(row map[string]any, key string) float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	case int:
		return float64(n)
	}
	return 0
}

// int64From extracts an int64, handling JSON's default float64 representation for numbers.
func int64From(row map[string]any, key string) int64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}

// floatPtrFrom extracts a *float64 from a query row, returning nil if absent or non-numeric.
// Use instead of floatFrom when 0.0 is a meaningful sentinel (e.g. HRV status, training status).
func floatPtrFrom(row map[string]any, key string) *float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return nil
	}
	switch n := v.(type) {
	case float64:
		return &n
	case int64:
		f := float64(n)
		return &f
	case int:
		f := float64(n)
		return &f
	}
	return nil
}

// int64FromString extracts an int64 from a query row where the value may be a tag
// (InfluxDB tags are always returned as strings) or a numeric field.
func int64FromString(row map[string]any, key string) int64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	case string:
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i
		}
	}
	return 0
}

// stringFrom extracts a string from a query row, returning "" if absent or wrong type.
func stringFrom(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// dateFrom extracts a YYYY-MM-DD date string from a query row.
// Returns "" when the key is absent or the timestamp cannot be parsed,
// so callers can detect missing data instead of seeing "0001-01-01".
func dateFrom(row map[string]any, key string) string {
	t := timeFrom(row, key)
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// timeFrom parses a timestamp from a query row.
// Handles RFC3339Nano strings (superset of RFC3339) and nanosecond-epoch integers.
func timeFrom(row map[string]any, key string) time.Time {
	v, ok := row[key]
	if !ok || v == nil {
		return time.Time{}
	}
	switch t := v.(type) {
	case string:
		// InfluxDB 3 Core returns timestamps without a timezone suffix (e.g. "2026-07-20T15:04:05").
		// Try RFC3339Nano first (has Z/offset), then the no-TZ form (treat as UTC).
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05"} {
			if ts, err := time.Parse(layout, t); err == nil {
				return ts.UTC()
			}
		}
	case float64:
		return time.Unix(0, int64(t)).UTC()
	case int64:
		return time.Unix(0, t).UTC()
	}
	return time.Time{}
}
