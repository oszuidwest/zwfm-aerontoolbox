package database

import (
	"strings"
	"testing"
)

func TestBuildMediaCheckQuery_InvalidSchema(t *testing.T) {
	if _, _, err := BuildMediaCheckQuery("bad schema!", &MediaCheckOptions{}); err == nil {
		t.Fatal("expected error for invalid schema, got nil")
	}
}

func TestBuildMediaCheckQuery_DefaultsToToday(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "CURRENT_DATE") {
		t.Errorf("expected CURRENT_DATE default scope, got: %s", query)
	}
	// Only the voicetrack-exclusion parameter should be bound.
	if len(params) != 1 {
		t.Errorf("params = %v, want 1 (voicetrack id)", params)
	}
	if !strings.Contains(query, "t.userid IS NULL OR t.userid <>") {
		t.Errorf("expected voicetrack exclusion, got: %s", query)
	}
	for _, removed := range []string{"as track_found", "as audioid", "as is_voicetrack"} {
		if strings.Contains(query, removed) {
			t.Errorf("query still selects removed column %q: %s", removed, query)
		}
	}
}

func TestBuildMediaCheckQuery_Date(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{Date: "2026-06-29"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "$1::date") {
		t.Errorf("expected bound date param, got: %s", query)
	}
	if params[0] != "2026-06-29" {
		t.Errorf("params[0] = %v, want date", params[0])
	}
}

func TestBuildMediaCheckQuery_BlockTakesPrecedence(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{
		BlockID: "abc", Date: "2026-06-29",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "pi.blockid = $1") {
		t.Errorf("expected block filter first, got: %s", query)
	}
	if params[0] != "abc" {
		t.Errorf("params[0] = %v, want block id", params[0])
	}
	if strings.Contains(query, "CURRENT_DATE") || strings.Contains(query, "::date") {
		t.Errorf("block scope should not include date filters, got: %s", query)
	}
}

func TestBuildMediaCheckQuery_Range(t *testing.T) {
	query, _, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{From: "2026-06-01", To: "2026-06-07"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, ">= $1::date") || !strings.Contains(query, "< $2::date + INTERVAL '1 day'") {
		t.Errorf("expected from/to range, got: %s", query)
	}
}

func TestBuildMediaCheckQuery_Lookahead(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{LookaheadDays: 2})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "CURRENT_DATE + $1::int") {
		t.Errorf("expected lookahead upper bound, got: %s", query)
	}
	// LookaheadDays=2 → today through today+2 inclusive → upper bound CURRENT_DATE + 3.
	if params[0] != 3 {
		t.Errorf("params[0] = %v, want 3 (lookahead+1)", params[0])
	}
}

func TestBuildMediaCheckQuery_DateIgnoresLookahead(t *testing.T) {
	query, _, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{Date: "2026-06-29", LookaheadDays: 5})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, "::int") {
		t.Errorf("explicit date scope must ignore lookahead, got: %s", query)
	}
}

func TestBuildMediaCheckQuery_IncludeVoicetracks(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{IncludeVoicetracks: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, "t.userid <>") {
		t.Errorf("voicetracks should be included, got: %s", query)
	}
	if len(params) != 0 {
		t.Errorf("params = %v, want none", params)
	}
}

func TestBuildMediaCheckQuery_Limit(t *testing.T) {
	query, params, err := BuildMediaCheckQuery("aeron", &MediaCheckOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "LIMIT") {
		t.Errorf("expected LIMIT clause, got: %s", query)
	}
	if params[len(params)-1] != 50 {
		t.Errorf("last param = %v, want 50", params[len(params)-1])
	}
}
