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
	tests := []struct {
		name        string
		trackImage  bool
		artistImage bool
		limit       int
		offset      int
	}{
		{
			name:        "track image false artist image false",
			trackImage:  false,
			artistImage: false,
			limit:       25,
			offset:      50,
		},
		{
			name:        "track image false artist image true",
			trackImage:  false,
			artistImage: true,
			limit:       25,
			offset:      50,
		},
		{
			name:        "track image true artist image false",
			trackImage:  true,
			artistImage: false,
			limit:       25,
			offset:      50,
		},
		{
			name:        "track image true artist image true",
			trackImage:  true,
			artistImage: true,
			limit:       25,
			offset:      50,
		},
		{
			name:        "limit without offset",
			trackImage:  false,
			artistImage: true,
			limit:       25,
			offset:      0,
		},
	}

	commonWants := []string{
		"FROM aeron.playlistitem pi",
		"WHERE pi.blockid = $1",
		"ORDER BY t.artist DESC",
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trackImage := tt.trackImage
			artistImage := tt.artistImage
			opts := &PlaylistOptions{
				BlockID:     "block-1",
				TrackImage:  &trackImage,
				ArtistImage: &artistImage,
				SortBy:      "artist",
				SortDesc:    true,
				Limit:       tt.limit,
				Offset:      tt.offset,
			}

			query, params, err := BuildPlaylistQuery("aeron", opts)
			if err != nil {
				t.Fatalf("BuildPlaylistQuery: %v", err)
			}

			wantTrackPredicate := "AND t.picture IS NULL"
			notWantTrackPredicate := "AND t.picture IS NOT NULL"
			if tt.trackImage {
				wantTrackPredicate, notWantTrackPredicate = notWantTrackPredicate, wantTrackPredicate
			}

			wantArtistPredicate := "AND a.picture IS NULL"
			notWantArtistPredicate := "AND a.picture IS NOT NULL"
			if tt.artistImage {
				wantArtistPredicate, notWantArtistPredicate = notWantArtistPredicate, wantArtistPredicate
			}

			for _, want := range commonWants {
				if !strings.Contains(query, want) {
					t.Fatalf("query missing %q:\n%s", want, query)
				}
			}
			for _, want := range []string{wantTrackPredicate, wantArtistPredicate, "LIMIT $2"} {
				if !strings.Contains(query, want) {
					t.Fatalf("query missing %q:\n%s", want, query)
				}
			}
			for _, notWant := range []string{notWantTrackPredicate, notWantArtistPredicate} {
				if strings.Contains(query, notWant) {
					t.Fatalf("query contains %q:\n%s", notWant, query)
				}
			}
			if tt.offset > 0 {
				if !strings.Contains(query, "LIMIT $2 OFFSET $3") {
					t.Fatalf("query missing LIMIT/OFFSET clause:\n%s", query)
				}
			} else if strings.Contains(query, " OFFSET ") {
				t.Fatalf("query contains unexpected OFFSET:\n%s", query)
			}

			wantParams := []any{"block-1", tt.limit}
			if tt.offset > 0 {
				wantParams = append(wantParams, tt.offset)
			}
			if !reflect.DeepEqual(params, wantParams) {
				t.Fatalf("params = %v, want %v", params, wantParams)
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
