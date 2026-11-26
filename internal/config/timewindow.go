package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// TimeWindow is a set of local-time activity windows, optionally per weekday.
//
// The syntax is a comma-separated list of "[days ]HH:MM-HH:MM" entries, for
// example "mon-fri 06:25-21:00, sat-sun 07:25-18:00". Days are mon..sun, a
// single day or a range; an entry without days applies to every day. A window
// whose end is earlier than its start wraps around midnight and belongs to the
// day it starts on (e.g. "fri 22:00-02:00" is active until Saturday 02:00).
//
// An unconfigured window (zero value) is always active. Equal start and end
// would be ambiguous, so they are rejected at parse time.
type TimeWindow struct {
	spans []timeSpan
}

// timeSpan is one window on the weekdays set in days (bit n = time.Weekday(n)).
// endMin runs past 24*60 when the window crosses midnight.
type timeSpan struct {
	days             uint8
	startMin, endMin int
}

const allDays uint8 = 1<<7 - 1

// weekdayNames is indexed by time.Weekday.
var weekdayNames = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// ParseTimeWindow parses the syntax described on TimeWindow. An empty string
// returns an unconfigured (always-active) window with no error.
func ParseTimeWindow(s string) (TimeWindow, error) {
	if s == "" {
		return TimeWindow{}, nil
	}
	var w TimeWindow
	for entry := range strings.SplitSeq(s, ",") {
		span, err := parseSpan(strings.TrimSpace(entry))
		if err != nil {
			return TimeWindow{}, err
		}
		w.spans = append(w.spans, span)
	}
	return w, nil
}

func parseSpan(s string) (timeSpan, error) {
	span := timeSpan{days: allDays}
	if dayPart, rest, ok := strings.Cut(s, " "); ok && !strings.Contains(dayPart, ":") {
		days, err := parseDays(dayPart)
		if err != nil {
			return timeSpan{}, err
		}
		span.days = days
		s = strings.TrimSpace(rest)
	}
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return timeSpan{}, fmt.Errorf("invalid window %q, expected [days ]HH:MM-HH:MM", s)
	}
	var err error
	if span.startMin, err = parseHHMM(strings.TrimSpace(parts[0])); err != nil {
		return timeSpan{}, err
	}
	if span.endMin, err = parseHHMM(strings.TrimSpace(parts[1])); err != nil {
		return timeSpan{}, err
	}
	if span.startMin == span.endMin {
		return timeSpan{}, fmt.Errorf("invalid window %q: start and end are equal (an empty active_window means all day, every day)", s)
	}
	if span.endMin < span.startMin {
		span.endMin += 24 * 60
	}
	return span, nil
}

// parseDays parses "mon" or a range such as "mon-fri" or "sat-sun" (ranges
// wrap past Saturday) into a time.Weekday bit mask.
func parseDays(s string) (uint8, error) {
	from, to, isRange := strings.Cut(strings.ToLower(s), "-")
	if !isRange {
		to = from
	}
	start, end := slices.Index(weekdayNames, from), slices.Index(weekdayNames, to)
	if start < 0 || end < 0 {
		return 0, fmt.Errorf("invalid days %q, expected mon..sun or a range like mon-fri", s)
	}
	var mask uint8
	for d := start; ; d = (d + 1) % 7 {
		mask |= 1 << d
		if d == end {
			return mask, nil
		}
	}
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
	if !w.IsConfigured() {
		return true
	}
	cur := t.Hour()*60 + t.Minute()
	today, yesterday := uint8(1)<<t.Weekday(), uint8(1)<<((t.Weekday()+6)%7)
	for _, s := range w.spans {
		if (s.days&today != 0 && cur >= s.startMin && cur < s.endMin) || (s.days&yesterday != 0 && cur+24*60 < s.endMin) {
			return true
		}
	}
	return false
}

// IsConfigured reports whether this time window was explicitly parsed from a
// non-empty string. An unconfigured window is treated as always-active.
func (w TimeWindow) IsConfigured() bool {
	return len(w.spans) > 0
}
