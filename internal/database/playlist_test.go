package database

import (
	"strings"
	"testing"
)

func TestBuildPlaylistQueryRequiresBlockID(t *testing.T) {
	query, params, err := BuildPlaylistQuery("aeron", &PlaylistOptions{})
	if err != nil {
		t.Fatalf("BuildPlaylistQuery: %v", err)
	}
	if query != "" {
		t.Fatalf("query = %q, want empty query without block id", query)
	}
	if len(params) != 0 {
		t.Fatalf("params = %v, want none", params)
	}
}

func TestBuildPlaylistQueryFiltersSortAndPagination(t *testing.T) {
	trackImage := false
	artistImage := true
	opts := &PlaylistOptions{
		BlockID:     "block-1",
		ExportTypes: []int{1, 2},
		TrackImage:  &trackImage,
		ArtistImage: &artistImage,
		SortBy:      "artist",
		SortDesc:    true,
		Limit:       25,
		Offset:      50,
	}

	query, params, err := BuildPlaylistQuery("aeron", opts)
	if err != nil {
		t.Fatalf("BuildPlaylistQuery: %v", err)
	}

	wants := []string{
		"FROM aeron.playlistitem pi",
		"pi.blockid = $1",
		"COALESCE(t.exporttype, 1) NOT IN ($2,$3)",
		"t.picture IS NULL",
		"a.picture IS NOT NULL",
		"ORDER BY t.artist DESC",
		"LIMIT $4 OFFSET $5",
	}
	for _, want := range wants {
		if !strings.Contains(query, want) {
			t.Fatalf("query missing %q:\n%s", want, query)
		}
	}

	wantParams := []any{"block-1", 1, 2, 25, 50}
	if len(params) != len(wantParams) {
		t.Fatalf("params = %v, want %v", params, wantParams)
	}
	for i := range wantParams {
		if params[i] != wantParams[i] {
			t.Fatalf("params[%d] = %v, want %v", i, params[i], wantParams[i])
		}
	}
}

func TestBuildPlaylistQueryRejectsInvalidSchema(t *testing.T) {
	_, _, err := BuildPlaylistQuery("bad schema", &PlaylistOptions{BlockID: "block-1"})
	if err == nil {
		t.Fatal("expected invalid schema error")
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
