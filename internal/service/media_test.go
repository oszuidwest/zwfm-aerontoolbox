package service

import (
	"strings"
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
)

func TestDefaultPlaylistOptionsSortsByStartTime(t *testing.T) {
	opts := DefaultPlaylistOptions()
	if opts.SortBy != "start_time" {
		t.Fatalf("SortBy = %q, want start_time", opts.SortBy)
	}

	opts.BlockID = "block-1"
	query, _, err := database.BuildPlaylistQuery("aeron", &opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "ORDER BY pi.startdatetime") {
		t.Fatalf("query = %q, want explicit start time ordering", query)
	}
}
