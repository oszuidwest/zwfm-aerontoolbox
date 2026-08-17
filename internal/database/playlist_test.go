package database

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildPlaylistQueryRequiresBlockID(t *testing.T) {
	_, _, err := BuildPlaylistQuery("aeron", &PlaylistOptions{})
	if err == nil {
		t.Fatal("expected validation error without block id")
	}
}

func TestBuildPlaylistQueryFiltersSortAndPagination(t *testing.T) {
	boolPtr := func(b bool) *bool { return &b }

	// The image predicates carry the "AND " prefix on purpose: the SELECT list
	// always contains "t.picture IS NOT NULL" / "a.picture IS NOT NULL" inside
	// the has_*_image CASE expressions, so only the WHERE occurrence counts.
	tests := []struct {
		name            string
		opts            *PlaylistOptions
		wantContains    []string
		wantNotContains []string
		wantParams      []any
	}{
		{
			name: "no image filters and no pagination",
			opts: &PlaylistOptions{BlockID: "block-1"},
			wantContains: []string{
				"FROM aeron.playlistitem pi",
				"WHERE pi.blockid = $1",
				"ORDER BY pi.startdatetime",
			},
			wantNotContains: []string{"AND t.picture", "AND a.picture", "LIMIT"},
			wantParams:      []any{"block-1"},
		},
		{
			name: "track image absent artist image present with limit and offset",
			opts: &PlaylistOptions{
				BlockID:     "block-1",
				TrackImage:  boolPtr(false),
				ArtistImage: boolPtr(true),
				SortBy:      "artist",
				SortDesc:    true,
				Limit:       25,
				Offset:      50,
			},
			wantContains: []string{
				"AND t.picture IS NULL",
				"AND a.picture IS NOT NULL",
				"ORDER BY t.artist DESC",
				"LIMIT $2 OFFSET $3",
			},
			wantNotContains: []string{"AND t.picture IS NOT NULL", "AND a.picture IS NULL"},
			wantParams:      []any{"block-1", 25, 50},
		},
		{
			name: "track image present artist image absent with limit only",
			opts: &PlaylistOptions{
				BlockID:     "block-1",
				TrackImage:  boolPtr(true),
				ArtistImage: boolPtr(false),
				Limit:       25,
			},
			wantContains: []string{
				"AND t.picture IS NOT NULL",
				"AND a.picture IS NULL",
				"LIMIT $2",
			},
			wantNotContains: []string{"AND t.picture IS NULL", "AND a.picture IS NOT NULL", " OFFSET "},
			wantParams:      []any{"block-1", 25},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, params, err := BuildPlaylistQuery("aeron", tt.opts)
			if err != nil {
				t.Fatalf("BuildPlaylistQuery: %v", err)
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

func TestBuildPlaylistQueryRejectsInvalidSchema(t *testing.T) {
	_, _, err := BuildPlaylistQuery("bad schema", &PlaylistOptions{BlockID: "block-1"})
	if err == nil {
		t.Fatal("expected invalid schema error")
	}
}

func TestBuildPlaylistQuerySortByDuration(t *testing.T) {
	query, _, err := BuildPlaylistQuery("aeron", &PlaylistOptions{
		BlockID: "block-1",
		SortBy:  "duration",
	})
	if err != nil {
		t.Fatalf("BuildPlaylistQuery: %v", err)
	}
	if !strings.Contains(query, "ORDER BY COALESCE(t.knownlength, 0)") {
		t.Fatalf("query = %s, want duration sort", query)
	}
}

func TestBuildPlaylistQuerySortByIsAllowListed(t *testing.T) {
	query, _, err := BuildPlaylistQuery("aeron", &PlaylistOptions{
		BlockID:  "block-1",
		SortBy:   "artist; DROP TABLE track",
		SortDesc: true,
	})
	if err != nil {
		t.Fatalf("BuildPlaylistQuery: %v", err)
	}
	if strings.Contains(query, "DROP") {
		t.Fatalf("query contains untrusted sort input: %s", query)
	}
	if !strings.Contains(query, "ORDER BY pi.startdatetime DESC") {
		t.Fatalf("query = %s, want start time fallback sort", query)
	}
}
