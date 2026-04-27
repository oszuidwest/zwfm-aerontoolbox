package config

import (
	"strings"
	"testing"
	"time"
)

func TestParseTimeWindow_Cases(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantStart int
		wantEnd   int
		wantConf  bool
	}{
		{name: "empty is unconfigured", input: "", wantConf: false},
		{name: "normal day window", input: "06:00-22:00", wantStart: 6 * 60, wantEnd: 22 * 60, wantConf: true},
		{name: "wraparound night window", input: "22:00-06:00", wantStart: 22 * 60, wantEnd: 6 * 60, wantConf: true},
		{name: "minute precision", input: "06:30-22:45", wantStart: 6*60 + 30, wantEnd: 22*60 + 45, wantConf: true},
		{name: "leading whitespace tolerated", input: " 06:00 - 22:00 ", wantStart: 6 * 60, wantEnd: 22 * 60, wantConf: true},
		{name: "missing dash", input: "06:00", wantErr: true},
		{name: "extra dash", input: "06:00-12:00-18:00", wantErr: true},
		{name: "non-numeric hour", input: "ab:00-22:00", wantErr: true},
		{name: "missing minutes", input: "0600-2200", wantErr: true},
		{name: "hour out of range", input: "24:00-06:00", wantErr: true},
		{name: "minute out of range", input: "06:60-22:00", wantErr: true},
		{name: "negative hour", input: "-1:00-22:00", wantErr: true},
		{name: "equal start and end", input: "12:00-12:00", wantErr: true},
		{name: "midnight equal", input: "00:00-00:00", wantErr: true},
		{name: "trailing junk in time", input: "06:00:00-22:00", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, err := ParseTimeWindow(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTimeWindow(%q) = %+v, want error", tc.input, w)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTimeWindow(%q) unexpected error: %v", tc.input, err)
			}
			if w.Configured != tc.wantConf {
				t.Errorf("Configured = %v, want %v", w.Configured, tc.wantConf)
			}
			if w.Configured && (w.StartMin != tc.wantStart || w.EndMin != tc.wantEnd) {
				t.Errorf("Start/End = %d/%d, want %d/%d", w.StartMin, w.EndMin, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

func TestTimeWindow_Active_Wraparound(t *testing.T) {
	// Night window: active 22:00 → next-day 06:00.
	w, err := ParseTimeWindow("22:00-06:00")
	if err != nil {
		t.Fatalf("ParseTimeWindow: %v", err)
	}

	cases := []struct {
		hh, mm int
		want   bool
	}{
		{21, 59, false}, // just before window opens
		{22, 0, true},   // window opens
		{23, 30, true},  // late night
		{0, 0, true},    // midnight
		{5, 59, true},   // last minute inside
		{6, 0, false},   // window closes
		{12, 0, false},  // midday outside
	}
	for _, tc := range cases {
		ts := time.Date(2026, 1, 1, tc.hh, tc.mm, 0, 0, time.UTC)
		if got := w.Active(ts); got != tc.want {
			t.Errorf("Active(%02d:%02d) = %v, want %v", tc.hh, tc.mm, got, tc.want)
		}
	}
}

func TestTimeWindow_UnconfiguredIsAlwaysActive(t *testing.T) {
	var w TimeWindow // zero value: Configured=false
	for _, hh := range []int{0, 6, 12, 22, 23} {
		ts := time.Date(2026, 1, 1, hh, 0, 0, 0, time.UTC)
		if !w.Active(ts) {
			t.Errorf("zero-value TimeWindow should be active at %02d:00", hh)
		}
	}
}

func TestTimeWindow_Active_NormalDay(t *testing.T) {
	// Day window: active 06:00 → 22:00.
	w, err := ParseTimeWindow("06:00-22:00")
	if err != nil {
		t.Fatalf("ParseTimeWindow: %v", err)
	}

	cases := []struct {
		hh, mm int
		want   bool
	}{
		{0, 0, false},
		{5, 59, false},
		{6, 0, true}, // boundary inclusive on start
		{12, 0, true},
		{21, 59, true},
		{22, 0, false}, // boundary exclusive on end
		{23, 0, false},
	}
	for _, tc := range cases {
		ts := time.Date(2026, 1, 1, tc.hh, tc.mm, 0, 0, time.UTC)
		if got := w.Active(ts); got != tc.want {
			t.Errorf("Active(%02d:%02d) = %v, want %v", tc.hh, tc.mm, got, tc.want)
		}
	}
}

func TestFileMonitorValidation_ActiveWindow(t *testing.T) {
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 30, ActiveWindow: "06:00-22:00"},
	}
	if err := validate(cfg); err != nil {
		t.Errorf("valid window rejected: %v", err)
	}

	// Specific reasons must survive into the user-facing message rather than
	// being collapsed to a generic "must be HH:MM-HH:MM". Without the verbatim
	// parser error, an operator hitting the equal-start/end case has no way
	// to tell what changed about the format and what to do about it.
	cases := []struct {
		input   string
		wantMsg string
	}{
		{"12:00-12:00", "start and end are equal"},
		{"24:00-06:00", "out of range"},
		{"garbage", "expected HH:MM"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			cfg.FileMonitor.Checks[0].ActiveWindow = tc.input
			err := validate(cfg)
			if err == nil {
				t.Fatalf("expected validation error for %q", tc.input)
			}
			if !strings.Contains(err.Error(), tc.wantMsg) {
				t.Errorf("error %q missing %q (parser reason was discarded)", err.Error(), tc.wantMsg)
			}
		})
	}

	cfg.FileMonitor.Checks[0].ActiveWindow = ""
	if err := validate(cfg); err != nil {
		t.Errorf("empty (always-active) window should pass validation, got: %v", err)
	}
}

func TestFileMonitorValidation_ActiveWindow_IncludesIndex(t *testing.T) {
	// Multiple bad windows must produce distinct, indexed error labels —
	// otherwise an operator with two typos sees the same "active_window"
	// key twice and cannot tell which checks[] entry needs editing. The
	// label is the only way to locate the bad row when the parser message
	// alone (e.g. "out of range") is shared between checks.
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = true
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 30, ActiveWindow: "12:00-12:00"},
		{Path: "/data/weather.mp3", MaxAgeMinutes: 60, ActiveWindow: "24:00-06:00"},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error for two bad windows")
	}
	msg := err.Error()
	if !strings.Contains(msg, "checks[0].active_window") {
		t.Errorf("error %q missing checks[0].active_window label", msg)
	}
	if !strings.Contains(msg, "checks[1].active_window") {
		t.Errorf("error %q missing checks[1].active_window label", msg)
	}
}

func TestFileMonitorValidation_ActiveWindow_EvenWhenDisabled(t *testing.T) {
	// Operators must see window-format errors during config load even with
	// the monitor turned off, otherwise enabling it later would fail with
	// confusing context.
	cfg := minimalConfig()
	cfg.FileMonitor.Enabled = false
	cfg.FileMonitor.Checks = []FileMonitorCheckConfig{
		{Path: "/data/news.mp3", MaxAgeMinutes: 30, ActiveWindow: "12:00-12:00"},
	}
	err := validate(cfg)
	if err == nil {
		t.Fatal("expected validation error even when file_monitor is disabled")
	}
	if !strings.Contains(err.Error(), "start and end are equal") {
		t.Errorf("error %q missing parser reason", err.Error())
	}
}
