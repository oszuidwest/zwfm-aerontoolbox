package config

import (
	"strings"
	"testing"
	"time"
)

// boundary is a spot-checked Active() probe: the window's state at hh:mm.
type boundary struct {
	hh, mm int
	want   bool
}

func TestParseTimeWindow_Cases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		checks  []boundary
	}{
		{name: "empty is unconfigured", input: "", checks: []boundary{
			// Unconfigured windows are always active.
			{0, 0, true}, {6, 0, true}, {12, 0, true}, {22, 0, true}, {23, 0, true},
		}},
		{name: "normal day window", input: "06:00-22:00", checks: []boundary{
			{0, 0, false},
			{5, 59, false},
			{6, 0, true}, // boundary inclusive on start
			{12, 0, true},
			{21, 59, true},
			{22, 0, false}, // boundary exclusive on end
			{23, 0, false},
		}},
		{name: "wraparound night window", input: "22:00-06:00", checks: []boundary{
			{21, 59, false}, // just before window opens
			{22, 0, true},   // window opens
			{23, 30, true},  // late night
			{0, 0, true},    // midnight
			{5, 59, true},   // last minute inside
			{6, 0, false},   // window closes
			{12, 0, false},  // midday outside
		}},
		{name: "minute precision", input: "06:30-22:45", checks: []boundary{
			{6, 29, false}, {6, 30, true}, {22, 44, true}, {22, 45, false},
		}},
		{name: "leading whitespace tolerated", input: " 06:00 - 22:00 ", checks: []boundary{
			{5, 59, false}, {6, 0, true}, {22, 0, false},
		}},
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
		{name: "unknown day", input: "funday 06:00-07:00", wantErr: true},
		{name: "unknown range end", input: "mon-xyz 06:00-07:00", wantErr: true},
		{name: "days without time", input: "mon-fri", wantErr: true},
		{name: "trailing comma", input: "06:00-07:00,", wantErr: true},
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
			if got, want := w.IsConfigured(), tc.input != ""; got != want {
				t.Errorf("IsConfigured() = %v, want %v", got, want)
			}
			for _, c := range tc.checks {
				ts := time.Date(2026, 1, 1, c.hh, c.mm, 0, 0, time.UTC)
				if got := w.Active(ts); got != c.want {
					t.Errorf("Active(%02d:%02d) = %v, want %v", c.hh, c.mm, got, c.want)
				}
			}
		})
	}
}

func TestParseTimeWindow_Weekdays(t *testing.T) {
	// 2026-01-02 is a Friday.
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 1, day, hh, mm, 0, 0, time.UTC) }
	tests := []struct {
		input string
		at    time.Time
		want  bool
	}{
		{"mon-fri 06:25-21:00, sat-sun 07:25-18:00", at(2, 20, 59), true},  // Fri evening
		{"mon-fri 06:25-21:00, sat-sun 07:25-18:00", at(3, 20, 59), false}, // Sat evening
		{"mon-fri 06:25-21:00, sat-sun 07:25-18:00", at(3, 7, 24), false},  // Sat, one minute early
		{"mon-fri 06:25-21:00, sat-sun 07:25-18:00", at(4, 7, 25), true},   // Sun
		{"mon-fri 06:25-21:00, sat-sun 07:25-18:00", at(5, 6, 25), true},   // Mon
		{"fri 22:00-02:00", at(3, 1, 59), true},                            // wraps into Saturday
		{"fri 22:00-02:00", at(2, 1, 0), false},                            // Fri 01:00 is Thursday's night
		{"fri 22:00-02:00", at(3, 22, 0), false},                           // no window starts on Saturday
		{"sun 22:00-02:00", at(5, 1, 59), true},                            // Sunday wraps into Monday
		{"sat-sun 00:00-00:00", at(3, 23, 59), true},                       // whole day
		{"sat-sun 00:00-00:00", at(5, 0, 0), false},
		{"sat 12:00-12:00", at(4, 11, 59), true},      // 24 hours from Saturday noon
		{"FRI-mon 10:00-11:00", at(4, 10, 30), true},  // case and wrap
		{"fri-mon 10:00-11:00", at(6, 10, 30), false}, // Tuesday
	}
	for _, tc := range tests {
		w, err := ParseTimeWindow(tc.input)
		if err != nil {
			t.Fatalf("ParseTimeWindow(%q): %v", tc.input, err)
		}
		if got := w.Active(tc.at); got != tc.want {
			t.Errorf("%q at %s = %v, want %v", tc.input, tc.at.Format("Mon 15:04"), got, tc.want)
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
		{"garbage", "expected [days ]HH:MM-HH:MM"},
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
	// Multiple bad windows must produce distinct, indexed error labels -
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
