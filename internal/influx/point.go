package influx

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Point is a minimal line-protocol builder for InfluxDB writes.
// Only used by Go code (internal/analysis writes training_load); all other
// measurements are written by the Python sync sidecar.
type Point struct {
	measurement string
	tags        map[string]string
	fields      map[string]float64
	ts          time.Time
}

// NewPoint creates a Point for the given measurement.
func NewPoint(measurement string) *Point {
	return &Point{
		measurement: measurement,
		tags:        make(map[string]string),
		fields:      make(map[string]float64),
		ts:          time.Now(),
	}
}

// SetTag adds a tag key/value pair.
func (p *Point) SetTag(k, v string) *Point {
	p.tags[k] = v
	return p
}

// SetField adds a float64 field.
func (p *Point) SetField(k string, v float64) *Point {
	p.fields[k] = v
	return p
}

// SetTimestamp sets the point timestamp.
func (p *Point) SetTimestamp(t time.Time) *Point {
	p.ts = t
	return p
}

// LineProtocol returns the InfluxDB line-protocol representation.
// Returns empty string if the measurement name is empty or no fields are set.
func (p *Point) LineProtocol() string {
	if p.measurement == "" || len(p.fields) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(escapeMeasurement(p.measurement))

	if len(p.tags) > 0 {
		keys := make([]string, 0, len(p.tags))
		for k := range p.tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sb.WriteByte(',')
			sb.WriteString(escapeTagKey(k))
			sb.WriteByte('=')
			sb.WriteString(escapeTagValue(p.tags[k]))
		}
	}

	sb.WriteByte(' ')

	keys := make([]string, 0, len(p.fields))
	for k := range p.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(escapeFieldKey(k))
		sb.WriteByte('=')
		_, _ = fmt.Fprintf(&sb, "%g", p.fields[k])
	}

	sb.WriteByte(' ')
	_, _ = fmt.Fprintf(&sb, "%d", p.ts.UnixNano())
	return sb.String()
}

// Line protocol escaping: commas, spaces, and equals signs are special in
// measurement names, tag keys, and tag values.
func escapeMeasurement(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ",", `\,`)
	return strings.ReplaceAll(s, " ", `\ `)
}

func escapeTagKey(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ",", `\,`)
	s = strings.ReplaceAll(s, "=", `\=`)
	return strings.ReplaceAll(s, " ", `\ `)
}

func escapeTagValue(s string) string { return escapeTagKey(s) }

func escapeFieldKey(s string) string { return escapeTagKey(s) }
