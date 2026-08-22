package database

import (
	"reflect"
	"strings"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// mediaCheckSelectAliases are the column aliases MediaCheckItem's db tags scan
// (see GetMediaCheckItems). A SELECT-list change that breaks scanning must
// fail here.
var mediaCheckSelectAliases = []string{
	"as trackid",
	"as tracktitle",
	"as artist",
	"as start_time",
	"as blockid",
	"as block",
	"as filepath",
	"as filename",
	"as audioname",
}

func TestBuildMediaCheckQuery(t *testing.T) {
	tests := []struct {
		name            string
		schema          string // Defaults to "aeron".
		opts            MediaCheckOptions
		wantErr         bool
		wantContains    []string
		wantNotContains []string
		wantParams      []any
	}{
		{
			name:    "invalid schema",
			schema:  "bad schema!",
			wantErr: true,
		},
		{
			name:         "defaults to today with voicetrack exclusion",
			opts:         MediaCheckOptions{},
			wantContains: []string{"CURRENT_DATE", "t.userid IS NULL OR t.userid <>"},
			wantParams:   []any{types.VoicetrackUserID},
		},
		{
			name:         "explicit date is bound",
			opts:         MediaCheckOptions{Date: "2026-06-29"},
			wantContains: []string{"$1::date"},
			wantParams:   []any{"2026-06-29", types.VoicetrackUserID},
		},
		{
			name:            "block takes precedence over date",
			opts:            MediaCheckOptions{BlockID: "abc", Date: "2026-06-29"},
			wantContains:    []string{"pi.blockid = $1"},
			wantNotContains: []string{"CURRENT_DATE", "::date"},
			wantParams:      []any{"abc", types.VoicetrackUserID},
		},
		{
			name:         "from/to range",
			opts:         MediaCheckOptions{From: "2026-06-01", To: "2026-06-07"},
			wantContains: []string{">= $1::date", "< $2::date + INTERVAL '1 day'"},
			wantParams:   []any{"2026-06-01", "2026-06-07", types.VoicetrackUserID},
		},
		{
			// LookaheadDays=2 → today through today+2 inclusive → upper bound
			// CURRENT_DATE + 3.
			name:         "lookahead widens today scope",
			opts:         MediaCheckOptions{LookaheadDays: 2},
			wantContains: []string{"CURRENT_DATE + $1::int"},
			wantParams:   []any{3, types.VoicetrackUserID},
		},
		{
			name:            "explicit date ignores lookahead",
			opts:            MediaCheckOptions{Date: "2026-06-29", LookaheadDays: 5},
			wantNotContains: []string{"::int"},
			wantParams:      []any{"2026-06-29", types.VoicetrackUserID},
		},
		{
			name:            "include voicetracks drops exclusion",
			opts:            MediaCheckOptions{IncludeVoicetracks: true},
			wantNotContains: []string{"t.userid <>"},
			wantParams:      nil,
		},
		{
			name:         "limit is bound last",
			opts:         MediaCheckOptions{Limit: 50},
			wantContains: []string{"LIMIT $2"},
			wantParams:   []any{types.VoicetrackUserID, 50},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := tt.schema
			if schema == "" {
				schema = "aeron"
			}

			query, params, err := BuildMediaCheckQuery(schema, &tt.opts)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildMediaCheckQuery: %v", err)
			}

			lowerQuery := strings.ToLower(query)
			for _, alias := range mediaCheckSelectAliases {
				if !strings.Contains(lowerQuery, alias) {
					t.Errorf("query missing scanned column %q:\n%s", alias, query)
				}
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(query, want) {
					t.Errorf("query missing %q:\n%s", want, query)
				}
			}
			for _, notWant := range tt.wantNotContains {
				if strings.Contains(query, notWant) {
					t.Errorf("query contains %q:\n%s", notWant, query)
				}
			}
			if !reflect.DeepEqual(params, tt.wantParams) {
				t.Errorf("params = %v, want %v", params, tt.wantParams)
			}
		})
	}
}
