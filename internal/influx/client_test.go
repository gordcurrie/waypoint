package influx

import (
	"strings"
	"testing"
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

func TestNew_EmptyDatabase(t *testing.T) {
	_, err := New("http://localhost:8181", "token", "")
	if err == nil {
		t.Fatal("expected error for empty database")
	}
}

func TestNew_HostWithPath(t *testing.T) {
	_, err := New("http://localhost:8181/influxdb", "token", "garmin")
	if err == nil {
		t.Fatal("expected error for host with path component")
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
