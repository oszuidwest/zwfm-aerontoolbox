package service

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
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
		statTimeout:     config.DefaultMediaFileCheckStatTimeoutSeconds * time.Second,
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

func waitForTestSignal(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", label)
	}
}

func receiveMatchOutcome(t *testing.T, ch <-chan matchOutcome) matchOutcome {
	t.Helper()
	var out matchOutcome
	select {
	case out = <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for match outcome")
	}
	return out
}

func receiveFileIndex(t *testing.T, ch <-chan *fileIndex) *fileIndex {
	t.Helper()
	var idx *fileIndex
	select {
	case idx = <-ch:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for file index")
	}
	return idx
}

func waitForStatInflightEmpty(t *testing.T, svc *MediaFileCheckService) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		svc.statInflightMu.Lock()
		n := len(svc.statInflight)
		svc.statInflightMu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for stat in-flight cleanup")
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

func TestMatch_StatTimeoutUsesSingleFlight(t *testing.T) {
	prev := mediaRootStat
	block := make(chan struct{})
	t.Cleanup(func() { mediaRootStat = prev })
	t.Cleanup(func() { close(block) })

	var starts atomic.Int32
	mediaRootStat = func(_ *os.Root, _ string) (os.FileInfo, error) {
		starts.Add(1)
		<-block
		return nil, os.ErrNotExist
	}

	svc := &MediaFileCheckService{
		statInflight: make(map[string]*statInFlight),
	}
	m := buildTestMatcher(t, map[string]string{"O:": t.TempDir()}, nil, true)
	m.statTimeout = 10 * time.Millisecond
	m.startStatFlight = svc.startOrJoinMediaStatFlight

	out := m.match(&matchInput{FilePath: `O:\Audio\frozen.wav`})
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, "stat timeout") {
		t.Fatalf("first status=%q error=%q, want stat timeout", out.Status, out.Error)
	}

	start := time.Now()
	out = m.match(&matchInput{FilePath: `O:\Audio\frozen.wav`})
	elapsed := time.Since(start)
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, "stat timeout") {
		t.Fatalf("second status=%q error=%q, want stat timeout", out.Status, out.Error)
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("second match took %v, want near-immediate return on already-budgeted stat flight", elapsed)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaRootStat starts = %d, want 1 shared in-flight stat", got)
	}
}

func TestMatch_StatSingleFlightSuccessPath(t *testing.T) {
	prev := mediaRootStat
	t.Cleanup(func() { mediaRootStat = prev })

	dir := t.TempDir()
	full := filepath.Join(dir, "Audio", "hit.wav")
	writeFile(t, full)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	closeRelease := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(closeRelease)

	var starts atomic.Int32
	mediaRootStat = func(_ *os.Root, _ string) (os.FileInfo, error) {
		if starts.Add(1) == 1 {
			close(started)
		}
		<-release
		return info, nil
	}

	svc := &MediaFileCheckService{
		statInflight: make(map[string]*statInFlight),
	}
	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true)
	m.ctx = context.Background()
	m.statTimeout = time.Second
	m.startStatFlight = svc.startOrJoinMediaStatFlight

	input := &matchInput{FilePath: `O:\Audio\hit.wav`}
	first := make(chan matchOutcome, 1)
	go func() { first <- m.match(input) }()
	waitForTestSignal(t, started, "first stat start")

	go func() {
		time.Sleep(10 * time.Millisecond)
		closeRelease()
	}()
	second := m.match(input)
	firstOut := receiveMatchOutcome(t, first)

	for _, out := range []matchOutcome{firstOut, second} {
		if out.Status != MediaStatusPresent || out.MatchType != matchTypeExactPath {
			t.Fatalf("status=%q matchType=%q, want present/exact_path", out.Status, out.MatchType)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaRootStat starts = %d, want 1 shared successful stat", got)
	}
	waitForStatInflightEmpty(t, svc)
}

func TestMatch_StatFlightContextCancel(t *testing.T) {
	prev := mediaRootStat
	block := make(chan struct{})
	t.Cleanup(func() { mediaRootStat = prev })
	t.Cleanup(func() { close(block) })

	started := make(chan struct{})
	var starts atomic.Int32
	mediaRootStat = func(_ *os.Root, _ string) (os.FileInfo, error) {
		if starts.Add(1) == 1 {
			close(started)
		}
		<-block
		return nil, os.ErrNotExist
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc := &MediaFileCheckService{
		statInflight: make(map[string]*statInFlight),
	}
	m := buildTestMatcher(t, map[string]string{"O:": t.TempDir()}, nil, true)
	m.ctx = ctx
	m.statTimeout = time.Second
	m.startStatFlight = svc.startOrJoinMediaStatFlight

	outCh := make(chan matchOutcome, 1)
	go func() { outCh <- m.match(&matchInput{FilePath: `O:\Audio\frozen.wav`}) }()
	waitForTestSignal(t, started, "stat start")
	cancel()

	out := receiveMatchOutcome(t, outCh)
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, context.Canceled.Error()) {
		t.Fatalf("status=%q error=%q, want context-canceled stat_error", out.Status, out.Error)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaRootStat starts = %d, want 1", got)
	}
}

func TestMatch_CompletedStatFlightIsRemoved(t *testing.T) {
	prev := mediaRootStat
	t.Cleanup(func() { mediaRootStat = prev })

	dir := t.TempDir()
	full := filepath.Join(dir, "Audio", "hit.wav")
	writeFile(t, full)
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	var starts atomic.Int32
	mediaRootStat = func(_ *os.Root, _ string) (os.FileInfo, error) {
		starts.Add(1)
		return info, nil
	}

	svc := &MediaFileCheckService{
		statInflight: make(map[string]*statInFlight),
	}
	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true)
	m.ctx = context.Background()
	m.statTimeout = time.Second
	m.startStatFlight = svc.startOrJoinMediaStatFlight

	for range 2 {
		out := m.match(&matchInput{FilePath: `O:\Audio\hit.wav`})
		if out.Status != MediaStatusPresent {
			t.Fatalf("status = %q, want present", out.Status)
		}
		waitForStatInflightEmpty(t, svc)
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("mediaRootStat starts = %d, want a fresh stat after completed flight cleanup", got)
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
	idx := buildFileIndexWithWalkDir(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}, true, mediaWalkDir)

	if err := idx.err(); err == nil {
		t.Fatal("expected index error for missing root, got nil")
	}
}

func TestMatch_IndexTimeoutReportsStatError(t *testing.T) {
	prev := mediaWalkDir
	block := make(chan struct{})
	t.Cleanup(func() { mediaWalkDir = prev })
	t.Cleanup(func() { close(block) })

	var starts atomic.Int32
	mediaWalkDir = func(_ string, _ fs.WalkDirFunc) error {
		starts.Add(1)
		<-block
		return nil
	}

	m := buildTestMatcher(t, nil, []string{"/frozen-share"}, true)
	m.ctx = context.Background()
	m.indexTimeout = 10 * time.Millisecond

	out := m.match(&matchInput{FileName: `O:\Audio\frozen.wav`})
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, "media file index timeout") {
		t.Fatalf("status=%q error=%q, want index timeout stat_error", out.Status, out.Error)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaWalkDir starts = %d, want 1 bounded index build", got)
	}
}

func TestGetIndexContextCancel(t *testing.T) {
	prev := mediaWalkDir
	block := make(chan struct{})
	t.Cleanup(func() { mediaWalkDir = prev })
	t.Cleanup(func() { close(block) })

	started := make(chan struct{})
	var starts atomic.Int32
	mediaWalkDir = func(_ string, _ fs.WalkDirFunc) error {
		if starts.Add(1) == 1 {
			close(started)
		}
		<-block
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	m := buildTestMatcher(t, nil, []string{"/frozen-share"}, true)
	m.ctx = ctx
	m.indexTimeout = time.Hour

	idxCh := make(chan *fileIndex, 1)
	go func() { idxCh <- m.getIndex() }()
	waitForTestSignal(t, started, "index walk start")
	cancel()

	idx := receiveFileIndex(t, idxCh)
	if err := idx.err(); err == nil || !strings.Contains(err.Error(), "media file index canceled: context canceled") {
		t.Fatalf("index error = %v, want context-canceled index error", err)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaWalkDir starts = %d, want 1", got)
	}
}

func TestIndexErrReportsBuiltIndexError(t *testing.T) {
	m := buildTestMatcher(t, nil, nil, true)
	idx := emptyFileIndex()
	idx.addError("boom")
	m.index = idx

	if err := m.indexErr(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("indexErr = %v, want stored index error", err)
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
