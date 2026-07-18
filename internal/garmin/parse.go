package garmin

import "time"

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

// timeFrom parses a timestamp from a query row.
// Handles RFC3339Nano strings (superset of RFC3339) and nanosecond-epoch integers.
func timeFrom(row map[string]any, key string) time.Time {
	v, ok := row[key]
	if !ok || v == nil {
		return time.Time{}
	}
	switch t := v.(type) {
	case string:
		if ts, err := time.Parse(time.RFC3339Nano, t); err == nil {
			return ts
		}
	case float64:
		return time.Unix(0, int64(t)).UTC()
	case int64:
		return time.Unix(0, t).UTC()
	}
	return time.Time{}
}
