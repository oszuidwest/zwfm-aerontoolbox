package config

import (
	"fmt"
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
// An unconfigured window (zero value) is always active; this is distinct from a
// parsed window - equal start and end would otherwise be ambiguous, so it is
// rejected at parse time and operators must omit the field for always-active
// behavior.
type TimeWindow struct {
	spans []timeSpan
}

// timeSpan is one HH:MM-HH:MM window on the weekdays set in days
// (bit n = time.Weekday(n)).
type timeSpan struct {
	days             uint8
	startMin, endMin int
}

const allDays uint8 = 1<<7 - 1

// weekdayNames lists day names Monday first so "sat-sun" is a plain range.
var weekdayNames = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// ParseTimeWindow parses a comma-separated list of "[days ]HH:MM-HH:MM"
// entries. An empty string returns an unconfigured (always-active) window with
// no error. Equal start and end minutes are rejected - operators should omit
// the field instead, since there is no sensible interpretation of a
// zero-length window.
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
		return timeSpan{}, fmt.Errorf("invalid window %q: start and end are equal; omit the field for always-active", s)
	}
	return span, nil
}

// parseDays parses "mon" or a range such as "mon-fri" or "fri-mon" into a
// time.Weekday bit mask.
func parseDays(s string) (uint8, error) {
	from, to, isRange := strings.Cut(strings.ToLower(s), "-")
	if !isRange {
		to = from
	}
	start, end := indexOfDay(from), indexOfDay(to)
	if start < 0 || end < 0 {
		return 0, fmt.Errorf("invalid days %q, expected mon..sun or a range like mon-fri", s)
	}
	var mask uint8
	for i := start; ; i = (i + 1) % 7 {
		mask |= 1 << ((i + 1) % 7) // Monday-first index to time.Weekday
		if i == end {
			return mask, nil
		}
	}
}

func indexOfDay(name string) int {
	for i, n := range weekdayNames {
		if n == name {
			return i
		}
	}
	return -1
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
	today := uint8(1) << t.Weekday()
	yesterday := uint8(1) << ((t.Weekday() + 6) % 7)
	for _, s := range w.spans {
		if s.startMin < s.endMin {
			if s.days&today != 0 && cur >= s.startMin && cur < s.endMin {
				return true
			}
			continue
		}
		if (s.days&today != 0 && cur >= s.startMin) || (s.days&yesterday != 0 && cur < s.endMin) {
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
