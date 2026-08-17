package service

import "testing"

func TestDefaultPlaylistOptionsSortsByStartTime(t *testing.T) {
	opts := DefaultPlaylistOptions()
	if opts.SortBy != "start_time" {
		t.Fatalf("SortBy = %q, want start_time", opts.SortBy)
	}
}
