package influx

import (
	"strings"
	"testing"
	"time"
)

func TestNew_MissingHost(t *testing.T) {
	_, err := New("", "token", "garmin")
	if err == nil {
		t.Fatal("expected error for empty host")
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c, err := New("http://localhost:8181/", "token", "garmin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.HasSuffix(c.host, "/") {
		t.Errorf("host should not have trailing slash, got %q", c.host)
	}
}

func TestNew_InvalidScheme(t *testing.T) {
	_, err := New("ftp://localhost:8181", "token", "garmin")
	if err == nil {
		t.Fatal("expected error for non-http(s) scheme")
	}
}

func TestNewFromEnv_MissingURL(t *testing.T) {
	t.Setenv("INFLUXDB_URL", "")
	_, err := NewFromEnv()
	if err == nil {
		t.Fatal("expected error when INFLUXDB_URL is empty")
	}
}

func TestConfigFromEnv_DefaultDatabase(t *testing.T) {
	t.Setenv("INFLUXDB_URL", "http://localhost:8086")
	t.Setenv("INFLUXDB_DATABASE", "")
	_, _, db, err := configFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db != "garmin" {
		t.Errorf("expected default database 'garmin', got %q", db)
	}
}

func TestConfigFromEnv_ExplicitDatabase(t *testing.T) {
	t.Setenv("INFLUXDB_URL", "http://localhost:8086")
	t.Setenv("INFLUXDB_DATABASE", "custom")
	_, _, db, err := configFromEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if db != "custom" {
		t.Errorf("expected database 'custom', got %q", db)
	}
}

func TestMeasurementConstants(t *testing.T) {
	// Regression guard: names must match what sync/sync.py writes.
	cases := map[string]string{
		"activity":           MeasurementActivity,
		"daily_stats":        MeasurementDailyStats,
		"sleep":              MeasurementSleep,
		"hrv":                MeasurementHRV,
		"training_readiness": MeasurementTrainingReadiness,
		"training_status":    MeasurementTrainingStatus,
		"performance":        MeasurementPerformance,
		"lactate_threshold":  MeasurementLactateThreshold,
		"respiration":        MeasurementRespiration,
		"training_load":      MeasurementTrainingLoad,
	}
	for want, got := range cases {
		if got != want {
			t.Errorf("measurement constant mismatch: want %q, got %q", want, got)
		}
	}
}

func TestPointLineProtocol(t *testing.T) {
	ts := time.Unix(0, 1_700_000_000_000_000_000)
	p := NewPoint("training_load").
		SetTag("device", "forerunner").
		SetField("atl_7day", 42.3).
		SetField("ctl_42day", 56.1).
		SetField("tsb", -13.8).
		SetTimestamp(ts)

	lp := p.LineProtocol()
	if !strings.HasPrefix(lp, "training_load,device=forerunner ") {
		t.Errorf("unexpected line protocol prefix: %q", lp)
	}
	if !strings.HasSuffix(lp, " 1700000000000000000") {
		t.Errorf("unexpected line protocol suffix: %q", lp)
	}
	for _, field := range []string{"atl_7day=42.3", "ctl_42day=56.1", "tsb=-13.8"} {
		if !strings.Contains(lp, field) {
			t.Errorf("line protocol missing %q: %s", field, lp)
		}
	}
}

func TestPointLineProtocol_NoTags(t *testing.T) {
	ts := time.Unix(0, 1_700_000_000_000_000_000)
	p := NewPoint("training_load").SetField("atl_7day", 42.3).SetTimestamp(ts)
	lp := p.LineProtocol()
	// No comma after measurement name when there are no tags.
	if !strings.HasPrefix(lp, "training_load ") {
		t.Errorf("expected 'training_load <space>' prefix, got: %q", lp)
	}
}

func TestPointLineProtocol_SpecialCharsEscaped(t *testing.T) {
	ts := time.Unix(0, 1_700_000_000_000_000_000)
	p := NewPoint("my measurement"). // space in measurement name
					SetTag("key=eq", "val,comma").
					SetField("f", 1.0).
					SetTimestamp(ts)
	lp := p.LineProtocol()
	if !strings.Contains(lp, `my\ measurement`) {
		t.Errorf("space in measurement not escaped: %q", lp)
	}
	if !strings.Contains(lp, `key\=eq`) {
		t.Errorf("equals in tag key not escaped: %q", lp)
	}
	if !strings.Contains(lp, `val\,comma`) {
		t.Errorf("comma in tag value not escaped: %q", lp)
	}
}

func TestPointLineProtocol_FieldsSortedAlphabetically(t *testing.T) {
	ts := time.Unix(0, 1_700_000_000_000_000_000)
	p := NewPoint("m").
		SetField("zzz", 3.0).
		SetField("aaa", 1.0).
		SetField("mmm", 2.0).
		SetTimestamp(ts)
	lp := p.LineProtocol()
	// Fields must appear in sorted order for deterministic output.
	idxA := strings.Index(lp, "aaa=")
	idxM := strings.Index(lp, "mmm=")
	idxZ := strings.Index(lp, "zzz=")
	if idxA >= idxM || idxM >= idxZ {
		t.Errorf("fields not in alphabetical order: %q", lp)
	}
}
