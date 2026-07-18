package influx

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

var lpTimestamp = time.Unix(0, 1_700_000_000_000_000_000)

func TestPointLineProtocol(t *testing.T) {
	p := NewPoint("training_load").
		SetTag("device", "forerunner").
		SetField("atl_7day", 42.3).
		SetField("ctl_42day", 56.1).
		SetField("tsb", -13.8).
		SetTimestamp(lpTimestamp)

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
	p := NewPoint("training_load").SetField("atl_7day", 42.3).SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	if !strings.HasPrefix(lp, "training_load ") {
		t.Errorf("expected 'training_load <space>' prefix, got: %q", lp)
	}
}

func TestPointLineProtocol_SpecialCharsEscaped(t *testing.T) {
	p := NewPoint("my measurement").
		SetTag("key=eq", "val,comma").
		SetField("f", 1.0).
		SetTimestamp(lpTimestamp)
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

func TestPointLineProtocol_BackslashEscaped(t *testing.T) {
	p := NewPoint(`my\measure`).
		SetTag(`tag\key`, `tag\val`).
		SetField("f", 1.0).
		SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	if !strings.Contains(lp, `my\\measure`) {
		t.Errorf("backslash in measurement not escaped: %q", lp)
	}
	if !strings.Contains(lp, `tag\\key`) {
		t.Errorf("backslash in tag key not escaped: %q", lp)
	}
	if !strings.Contains(lp, `tag\\val`) {
		t.Errorf("backslash in tag value not escaped: %q", lp)
	}
}

func TestPointLineProtocol_EmptyFields(t *testing.T) {
	p := NewPoint("training_load").SetTimestamp(time.Now())
	if got := p.LineProtocol(); got != "" {
		t.Errorf("expected empty string for no-field point, got %q", got)
	}
}

func TestPointLineProtocol_EmptyMeasurement(t *testing.T) {
	p := NewPoint("").SetField("x", 1.0).SetTimestamp(time.Now())
	if got := p.LineProtocol(); got != "" {
		t.Errorf("expected empty string for empty-measurement point, got %q", got)
	}
}

func TestPointLineProtocol_FieldsSortedAlphabetically(t *testing.T) {
	p := NewPoint("m").
		SetField("zzz", 3.0).
		SetField("aaa", 1.0).
		SetField("mmm", 2.0).
		SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	idxA := strings.Index(lp, "aaa=")
	idxM := strings.Index(lp, "mmm=")
	idxZ := strings.Index(lp, "zzz=")
	if idxA >= idxM || idxM >= idxZ {
		t.Errorf("fields not in alphabetical order: %q", lp)
	}
}

func TestPointLineProtocol_NaNFieldDropped(t *testing.T) {
	p := NewPoint("m").SetField("valid", 1.0).SetField("nan_val", math.NaN()).SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	if strings.Contains(lp, "nan_val") {
		t.Errorf("NaN field must be dropped from line protocol: %q", lp)
	}
	if !strings.Contains(lp, "valid=1") {
		t.Errorf("valid field must be present: %q", lp)
	}
}

func TestPointLineProtocol_InfFieldDropped(t *testing.T) {
	p := NewPoint("m").SetField("inf_val", math.Inf(1)).SetField("ok", 2.0).SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	if strings.Contains(lp, "inf_val") {
		t.Errorf("Inf field must be dropped from line protocol: %q", lp)
	}
}

func TestPointSetTimestamp_ZeroIgnored(t *testing.T) {
	before := time.Now()
	p := NewPoint("m").SetField("f", 1.0).SetTimestamp(time.Time{})
	lp := p.LineProtocol()
	after := time.Now()
	// timestamp must be between before and after (i.e. time.Now() from NewPoint)
	var ns int64
	if _, err := fmt.Sscanf(lp[strings.LastIndex(lp, " ")+1:], "%d", &ns); err != nil {
		t.Fatalf("could not parse timestamp from %q: %v", lp, err)
	}
	ts := time.Unix(0, ns)
	if ts.Before(before) || ts.After(after) {
		t.Errorf("zero SetTimestamp should keep NewPoint default; got %v", ts)
	}
}

func TestPointLineProtocol_TagsSortedAlphabetically(t *testing.T) {
	p := NewPoint("m").
		SetTag("zzz", "3").
		SetTag("aaa", "1").
		SetTag("mmm", "2").
		SetField("f", 1.0).
		SetTimestamp(lpTimestamp)
	lp := p.LineProtocol()
	idxA := strings.Index(lp, "aaa=")
	idxM := strings.Index(lp, "mmm=")
	idxZ := strings.Index(lp, "zzz=")
	if idxA >= idxM || idxM >= idxZ {
		t.Errorf("tags not in alphabetical order: %q", lp)
	}
}
