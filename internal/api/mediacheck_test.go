package api

import (
	"net/url"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
)

func mustValues(t *testing.T, raw string) url.Values {
	t.Helper()
	v, err := url.ParseQuery(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseMediaCheckOptions(t *testing.T) {
	const blockID = "add55a6e-2068-4114-b82a-e0729881f0be"

	tests := []struct {
		name    string
		query   string
		wantErr bool
		want    database.MediaCheckOptions
	}{
		{
			name:  "defaults are empty scope without voicetracks",
			query: "",
			want:  database.MediaCheckOptions{},
		},
		{
			name:  "valid date",
			query: "date=2026-06-29",
			want:  database.MediaCheckOptions{Date: "2026-06-29"},
		},
		{
			name:    "invalid date",
			query:   "date=29-06-2026",
			wantErr: true,
		},
		{
			name:    "invalid block_id",
			query:   "block_id=not-a-uuid",
			wantErr: true,
		},
		{
			name:  "valid block_id",
			query: "block_id=" + blockID,
			want:  database.MediaCheckOptions{BlockID: blockID},
		},
		{
			name:  "include voicetracks",
			query: "include_voicetracks=true",
			want:  database.MediaCheckOptions{IncludeVoicetracks: true},
		},
		{
			name:    "negative limit",
			query:   "limit=-5",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, err := parseMediaCheckOptions(mustValues(t, tt.query), 31)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseMediaCheckOptions(%q) error = nil, want error", tt.query)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMediaCheckOptions(%q): %v", tt.query, err)
			}
			if *opts != tt.want {
				t.Errorf("parseMediaCheckOptions(%q) = %+v, want %+v", tt.query, *opts, tt.want)
			}
		})
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
