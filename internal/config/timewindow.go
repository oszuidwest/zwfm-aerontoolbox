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
// Equal start and end means 24 hours from the start on the given days
// ("sat-sun 00:00-00:00" is all of Saturday and Sunday);
// without days it is rejected, because an empty window already means always.
// An unconfigured window (zero value) is always active.
type TimeWindow struct {
	spans []timeSpan
}

// timeSpan is one window on the weekdays set in days (bit n = time.Weekday(n)).
// endMin runs past 24*60 when the window crosses midnight; equal start and end
// with days given means 24 hours from the start.
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
		entry = strings.TrimSpace(entry)
		span, err := parseSpan(entry)
		if err != nil {
			return TimeWindow{}, fmt.Errorf("invalid window %q: %w", entry, err)
		}
		w.spans = append(w.spans, span)
	}
	return w, nil
}

func parseSpan(s string) (timeSpan, error) {
	span := timeSpan{days: allDays}
	dayPart, rest, hasDays := strings.Cut(s, " ")
	hasDays = hasDays && !strings.Contains(dayPart, ":")
	if hasDays {
		days, err := parseDays(dayPart)
		if err != nil {
			return timeSpan{}, err
		}
		span.days = days
		s = strings.TrimSpace(rest)
	}
	parts := strings.Split(s, "-")
	if len(parts) != 2 {
		return timeSpan{}, fmt.Errorf("expected [days ]HH:MM-HH:MM")
	}
	var err error
	if span.startMin, err = parseHHMM(strings.TrimSpace(parts[0])); err != nil {
		return timeSpan{}, err
	}
	if span.endMin, err = parseHHMM(strings.TrimSpace(parts[1])); err != nil {
		return timeSpan{}, err
	}
	if span.startMin == span.endMin && !hasDays {
		return timeSpan{}, fmt.Errorf("start and end are equal; leave active_window empty to monitor all day, every day")
	}
	if span.endMin <= span.startMin {
		span.endMin += 24 * 60
	}
	return span, nil
}

// parseDays parses a day ("mon") or an inclusive range ("mon-fri"; ranges may
// wrap, e.g. "fri-mon"), ignoring case, into a time.Weekday bit mask.
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
