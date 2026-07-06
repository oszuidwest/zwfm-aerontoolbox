package api

import (
	"net/url"
	"testing"
	"time"
)

func mustValues(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseMediaCheckOptions_Defaults(t *testing.T) {
	opts, err := parseMediaCheckOptions(url.Values{}, 31)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Date != "" || opts.From != "" || opts.To != "" || opts.BlockID != "" {
		t.Errorf("expected empty scope, got %+v", opts)
	}
	if opts.IncludeVoicetracks {
		t.Error("voicetracks should be excluded by default")
	}
}

func TestParseMediaCheckOptions_ValidDate(t *testing.T) {
	opts, err := parseMediaCheckOptions(mustValues(t, "date=2026-06-29"), 31)
	if err != nil {
		t.Fatal(err)
	}
	if opts.Date != "2026-06-29" {
		t.Errorf("date = %q", opts.Date)
	}
}

func TestParseMediaCheckOptions_InvalidDate(t *testing.T) {
	if _, err := parseMediaCheckOptions(mustValues(t, "date=29-06-2026"), 31); err == nil {
		t.Fatal("expected error for malformed date")
	}
}

func TestParseMediaCheckOptions_InvalidBlockID(t *testing.T) {
	if _, err := parseMediaCheckOptions(mustValues(t, "block_id=not-a-uuid"), 31); err == nil {
		t.Fatal("expected error for malformed block_id")
	}
}

func TestParseMediaCheckOptions_ValidBlockID(t *testing.T) {
	id := "add55a6e-2068-4114-b82a-e0729881f0be"
	opts, err := parseMediaCheckOptions(mustValues(t, "block_id="+id), 31)
	if err != nil {
		t.Fatal(err)
	}
	if opts.BlockID != id {
		t.Errorf("block_id = %q", opts.BlockID)
	}
}

func TestParseMediaCheckOptions_IncludeVoicetracks(t *testing.T) {
	opts, err := parseMediaCheckOptions(mustValues(t, "include_voicetracks=true"), 31)
	if err != nil {
		t.Fatal(err)
	}
	if !opts.IncludeVoicetracks {
		t.Error("expected voicetracks included")
	}
}

func TestParseMediaCheckOptions_NegativeLimit(t *testing.T) {
	if _, err := parseMediaCheckOptions(mustValues(t, "limit=-5"), 31); err == nil {
		t.Fatal("expected error for negative limit")
	}
}

func TestValidateDateRange(t *testing.T) {
	date := func(value string) time.Time {
		t.Helper()
		d, err := time.Parse(dateParam, value)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if err := validateDateRange(date("2026-06-01"), date("2026-06-07"), 31); err != nil {
		t.Errorf("valid range rejected: %v", err)
	}
	if err := validateDateRange(date("2026-06-07"), date("2026-06-01"), 31); err == nil {
		t.Error("expected error when to < from")
	}
	if err := validateDateRange(date("2026-01-01"), date("2026-12-31"), 31); err == nil {
		t.Error("expected error when range exceeds cap")
	}
	// Inclusive span: exactly maxRangeDays is allowed.
	if err := validateDateRange(date("2026-06-01"), date("2026-06-30"), 30); err != nil {
		t.Errorf("30-day inclusive range with cap 30 rejected: %v", err)
	}
	// Single bound is allowed (open-ended).
	if err := validateDateRange(date("2026-06-01"), time.Time{}, 31); err != nil {
		t.Errorf("single bound rejected: %v", err)
	}
}
