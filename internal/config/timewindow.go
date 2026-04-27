package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// TimeWindow represents a HH:MM-HH:MM window in local time.
//
// A window whose end is earlier than its start wraps around midnight (e.g.
// 22:00-06:00 is active from 22:00 until 06:00 the next morning). An
// unconfigured window (zero value) is always active; this is distinct from a
// parsed window — equal start and end would otherwise be ambiguous, so it is
// rejected at parse time and operators must omit the field for always-active
// behaviour.
type TimeWindow struct {
	startMin, endMin int
	configured       bool
}

// ParseTimeWindow parses "HH:MM-HH:MM". An empty string returns an
// unconfigured (always-active) window with no error. Equal start and end
// minutes are rejected — operators should omit the field instead, since
// there is no sensible interpretation of a zero-length window.
func ParseTimeWindow(s string) (TimeWindow, error) {
	if s == "" {
		return TimeWindow{}, nil
	}
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return TimeWindow{}, fmt.Errorf("invalid window %q, expected HH:MM-HH:MM", s)
	}
	start, err := parseHHMM(strings.TrimSpace(parts[0]))
	if err != nil {
		return TimeWindow{}, err
	}
	end, err := parseHHMM(strings.TrimSpace(parts[1]))
	if err != nil {
		return TimeWindow{}, err
	}
	if start == end {
		return TimeWindow{}, fmt.Errorf("invalid window %q: start and end are equal; omit the field for always-active", s)
	}
	return TimeWindow{startMin: start, endMin: end, configured: true}, nil
}

func parseHHMM(s string) (int, error) {
	hh, mm, ok := strings.Cut(s, ":")
	if !ok {
		return 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	h, err := strconv.Atoi(hh)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	m, err := strconv.Atoi(mm)
	if err != nil {
		return 0, fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("time %q out of range (HH 0-23, MM 0-59)", s)
	}
	return h*60 + m, nil
}

// Active reports whether t (in its own location) falls inside the window.
// An unconfigured window is treated as always-active.
func (w TimeWindow) Active(t time.Time) bool {
	if !w.configured {
		return true
	}
	cur := t.Hour()*60 + t.Minute()
	if w.startMin < w.endMin {
		return cur >= w.startMin && cur < w.endMin
	}
	return cur >= w.startMin || cur < w.endMin
}

// IsConfigured reports whether this time window was explicitly parsed from a
// non-empty string. An unconfigured window is treated as always-active.
func (w TimeWindow) IsConfigured() bool {
	return w.configured
}
