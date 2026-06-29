package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNormalizeDriveKey(t *testing.T) {
	cases := map[string]string{
		"O:":    "O:",
		"o:":    "O:",
		`O:\`:   "O:",
		"O":     "O:",
		"  o  ": "O:",
		"OO:":   "",
		"1:":    "",
		"":      "",
	}
	for in, want := range cases {
		if got := normalizeDriveKey(in); got != want {
			t.Errorf("normalizeDriveKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDriveOf(t *testing.T) {
	cases := map[string]string{
		`O:\Audio\x.wav`: "O:",
		`y:\a\b.mp3`:     "Y:",
		`\\server\share`: "",
		`/mnt/audio/x`:   "",
		`relative\x`:     "",
	}
	for in, want := range cases {
		if got := driveOf(in); got != want {
			t.Errorf("driveOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWinPathComponents(t *testing.T) {
	got := winPathComponents(`O:\Audio\85\Artist - Title.wav`)
	want := []string{"Audio", "85", "Artist - Title.wav"}
	if len(got) != len(want) {
		t.Fatalf("components = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("components = %v, want %v", got, want)
		}
	}

	if winPathComponents(`O:\Audio\..\..\etc\passwd`) != nil {
		t.Error("expected nil components for path containing '..'")
	}
}

func TestBaseNameAndStem(t *testing.T) {
	if got := baseName(`O:\Audio\85\Artist - Title.wav`); got != "Artist - Title.wav" {
		t.Errorf("baseName = %q", got)
	}
	if got := baseName("/mnt/x/song.flac"); got != "song.flac" {
		t.Errorf("baseName = %q", got)
	}
	if got := stem("Artist - Title.wav"); got != "Artist - Title" {
		t.Errorf("stem = %q", got)
	}
	if got := stem("no_extension"); got != "no_extension" {
		t.Errorf("stem = %q", got)
	}
}

// buildTestMatcher constructs a matcher for tests, opening an os.Root per drive
// mount. Missing directories are stored as unopened roots (root == nil).
func buildTestMatcher(t *testing.T, driveDirs map[string]string, searchDirs []string, caseInsensitive bool) *mediaMatcher {
	t.Helper()
	driveRoots := make(map[string]*rootDir)
	for drive, dir := range driveDirs {
		rd := &rootDir{dir: dir}
		if root, err := os.OpenRoot(dir); err == nil {
			rd.root = root
			t.Cleanup(func() { _ = root.Close() })
		} else {
			rd.openErr = err
		}
		driveRoots[normalizeDriveKey(drive)] = rd
	}
	return &mediaMatcher{
		driveRoots:      driveRoots,
		searchDirs:      searchDirs,
		caseInsensitive: caseInsensitive,
		statTimeout:     5 * time.Second,
	}
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMatch_DriveMappingExactPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Audio", "85", "Artist - Title.wav"))

	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\85\Artist - Title.wav`})

	if out.Status != MediaStatusPresent {
		t.Fatalf("status = %q, want present", out.Status)
	}
	if out.MatchType != matchTypeExactPath {
		t.Errorf("matchType = %q, want %q", out.MatchType, matchTypeExactPath)
	}
	if len(out.Matches) != 1 {
		t.Errorf("matches = %v, want one entry", out.Matches)
	}
}

func TestMatch_DriveMappingMissing(t *testing.T) {
	dir := t.TempDir()
	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\nope.wav`})

	if out.Status != MediaStatusMissing {
		t.Fatalf("status = %q, want missing", out.Status)
	}
}

func TestMatch_IndexByFilename(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "unique.wav"))

	m := buildTestMatcher(t, nil, []string{root}, true)
	// No drive mapping for O: -> falls through to the index.
	out := m.match(&matchInput{FilePath: `O:\elsewhere\unique.wav`})

	if out.Status != MediaStatusPresent {
		t.Fatalf("status = %q, want present", out.Status)
	}
	if out.MatchType != matchTypeFilename {
		t.Errorf("matchType = %q, want %q", out.MatchType, matchTypeFilename)
	}
}

func TestMatch_IndexAmbiguous(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a", "dup.wav"))
	writeFile(t, filepath.Join(root, "b", "dup.wav"))

	m := buildTestMatcher(t, nil, []string{root}, true)
	out := m.match(&matchInput{FileName: `O:\x\dup.wav`})

	if out.Status != MediaStatusAmbiguous {
		t.Fatalf("status = %q, want ambiguous", out.Status)
	}
	if len(out.Matches) != 2 {
		t.Errorf("matches = %v, want two entries", out.Matches)
	}
}

func TestMatch_ExtensionIndependent(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "song.flac")) // DB says .wav, disk has .flac

	m := buildTestMatcher(t, nil, []string{root}, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\song.wav`})

	if out.Status != MediaStatusPresent {
		t.Fatalf("status = %q, want present", out.Status)
	}
	if out.MatchType != matchTypeFilenameNoExt {
		t.Errorf("matchType = %q, want %q", out.MatchType, matchTypeFilenameNoExt)
	}
}

func TestMatch_MetadataOnlyIsNotAFileReference(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "Artist - Title.mp3"))

	m := buildTestMatcher(t, nil, []string{root}, true)
	out := m.match(&matchInput{Artist: "Artist", TrackTitle: "Title"})

	if out.Status != MediaStatusNoReference {
		t.Fatalf("status = %q, want no_reference", out.Status)
	}
}

func TestMatch_NoReference(t *testing.T) {
	m := buildTestMatcher(t, nil, nil, true)
	out := m.match(&matchInput{})

	if out.Status != MediaStatusNoReference {
		t.Fatalf("status = %q, want no_reference", out.Status)
	}
}

func TestMatch_ConcretePathIgnoresBareTitleIndexHit(t *testing.T) {
	driveDir := t.TempDir()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "jingles", "Liefdedealer.mp3"))

	m := buildTestMatcher(t, map[string]string{"O:": driveDir}, []string{root}, true)
	out := m.match(&matchInput{
		FilePath:   `O:\Audio\85\Blof - Liefdedealer.wav`,
		Artist:     "Blof",
		TrackTitle: "Liefdedealer",
	})

	if out.Status != MediaStatusMissing {
		t.Fatalf("status = %q, want missing", out.Status)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("matches = %v, want none", out.Matches)
	}
}

func TestMatch_CaseSensitivity(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "MixedCase.WAV"))

	ci := buildTestMatcher(t, nil, []string{root}, true)
	if out := ci.match(&matchInput{FileName: `O:\x\mixedcase.wav`}); out.Status != MediaStatusPresent {
		t.Errorf("case-insensitive: status = %q, want present", out.Status)
	}

	cs := buildTestMatcher(t, nil, []string{root}, false)
	if out := cs.match(&matchInput{FileName: `O:\x\mixedcase.wav`}); out.Status != MediaStatusMissing {
		t.Errorf("case-sensitive: status = %q, want missing", out.Status)
	}
}

func TestMatch_StatErrorWhenRootUnavailable(t *testing.T) {
	// Drive mapped to a directory that cannot be opened -> stat error, not missing.
	m := buildTestMatcher(t, map[string]string{"O:": filepath.Join(t.TempDir(), "does-not-exist")}, nil, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\x.wav`})

	if out.Status != MediaStatusStatError {
		t.Fatalf("status = %q, want stat_error", out.Status)
	}
	if out.Error == "" {
		t.Error("expected non-empty error message")
	}
}

func TestMatch_StatErrorWinsOverIndexFallback(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "x.wav"))

	m := buildTestMatcher(t, map[string]string{"O:": filepath.Join(t.TempDir(), "does-not-exist")}, []string{root}, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\x.wav`})

	if out.Status != MediaStatusStatError {
		t.Fatalf("status = %q, want stat_error", out.Status)
	}
	if len(out.Matches) != 0 {
		t.Fatalf("matches = %v, want none", out.Matches)
	}
}

func TestMatch_DriveMappingPrefersExactOverIndex(t *testing.T) {
	// A correct exact path must win even when an index also has the basename.
	driveDir := t.TempDir()
	writeFile(t, filepath.Join(driveDir, "Audio", "hit.wav"))
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "other", "hit.wav"))

	m := buildTestMatcher(t, map[string]string{"O:": driveDir}, []string{root}, true)
	out := m.match(&matchInput{FilePath: `O:\Audio\hit.wav`})

	if out.Status != MediaStatusPresent || out.MatchType != matchTypeExactPath {
		t.Fatalf("status=%q matchType=%q, want present/exact_path", out.Status, out.MatchType)
	}
}

func TestBuildFileIndexRecordsWalkError(t *testing.T) {
	idx := buildFileIndex(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}, true)

	if err := idx.err(); err == nil {
		t.Fatal("expected index error for missing root, got nil")
	}
}

func TestSummarizeCountsByStatus(t *testing.T) {
	items := []MediaCheckItemResult{
		{Status: MediaStatusPresent},
		{Status: MediaStatusPresent},
		{Status: MediaStatusMissing},
		{Status: MediaStatusAmbiguous},
		{Status: MediaStatusNoReference},
		{Status: MediaStatusStatError},
	}
	sum := summarize(items)
	if sum.Total != 6 || sum.Present != 2 || sum.Missing != 1 || sum.Ambiguous != 1 || sum.NoReference != 1 || sum.Errors != 1 {
		t.Errorf("summary = %+v", sum)
	}
}
