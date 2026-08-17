package service

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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
	if !slices.Equal(got, want) {
		t.Fatalf("components = %v, want %v", got, want)
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

type matcherOption func(*mediaMatcher)

// withMatcherContext overrides the matcher context (e.g. with a cancellable one).
func withMatcherContext(ctx context.Context) matcherOption {
	return func(m *mediaMatcher) { m.ctx = ctx }
}

// withStatTimeout overrides the per-stat timeout.
func withStatTimeout(d time.Duration) matcherOption {
	return func(m *mediaMatcher) { m.statTimeout = d }
}

// withIndexTimeout overrides the index-build timeout.
func withIndexTimeout(d time.Duration) matcherOption {
	return func(m *mediaMatcher) { m.indexTimeout = d }
}

// withStatFlights routes stats through the given flight group so tests can
// observe its in-flight map.
func withStatFlights(g *statFlightGroup) matcherOption {
	return func(m *mediaMatcher) { m.startStatFlight = g.startOrJoin }
}

// buildTestMatcher constructs a matcher for tests, opening an os.Root per drive
// mount (missing directories are stored as unopened roots, root == nil). It
// mirrors buildMatcher's defaults: a background context, the production stat
// and index timeouts, and a fresh stat-flight group.
func buildTestMatcher(t *testing.T, driveDirs map[string]string, searchDirs []string, caseInsensitive bool, opts ...matcherOption) *mediaMatcher {
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
	m := &mediaMatcher{
		driveRoots:      driveRoots,
		searchDirs:      searchDirs,
		caseInsensitive: caseInsensitive,
		statTimeout:     config.DefaultMediaFileCheckStatTimeoutSeconds * time.Second,
		indexTimeout:    mediaCheckRunTimeout,
		ctx:             context.Background(),
		startStatFlight: new(statFlightGroup).startOrJoin,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// waitForStatInflightEmpty waits for the flight group's in-flight map to drain.
func waitForStatInflightEmpty(t *testing.T, g *statFlightGroup) {
	t.Helper()
	waitFor(t, time.Second, "timed out waiting for stat in-flight cleanup", func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return len(g.flights) == 0
	})
}

// stubMediaRootStat replaces mediaRootStat for the test with a stub that counts
// invocations, signals started on the first call, and blocks every call until
// release is invoked. release is idempotent and also registered as a cleanup so
// blocked goroutines always unwind. Released calls return (info, err).
func stubMediaRootStat(t *testing.T, info os.FileInfo, err error) (started <-chan struct{}, release func(), starts *atomic.Int32) {
	t.Helper()
	prev := mediaRootStat
	t.Cleanup(func() { mediaRootStat = prev })

	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	var once sync.Once
	releaseFn := func() { once.Do(func() { close(releaseCh) }) }
	t.Cleanup(releaseFn)

	counter := new(atomic.Int32)
	mediaRootStat = func(*os.Root, string) (os.FileInfo, error) {
		if counter.Add(1) == 1 {
			close(startedCh)
		}
		<-releaseCh
		return info, err
	}
	return startedCh, releaseFn, counter
}

// stubMediaWalkDir replaces mediaWalkDir for the test with a stub that counts
// invocations, signals started on the first call, and blocks every call until
// the test ends (a cleanup releases blocked walkers so they always unwind).
func stubMediaWalkDir(t *testing.T) (started <-chan struct{}, starts *atomic.Int32) {
	t.Helper()
	prev := mediaWalkDir
	t.Cleanup(func() { mediaWalkDir = prev })

	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	t.Cleanup(func() { close(releaseCh) })

	counter := new(atomic.Int32)
	mediaWalkDir = func(string, fs.WalkDirFunc) error {
		if counter.Add(1) == 1 {
			close(startedCh)
		}
		<-releaseCh
		return nil
	}
	return startedCh, counter
}

func TestMatch_Outcomes(t *testing.T) {
	tests := []struct {
		name          string
		files         []string          // files created under the case's base dir (slash-separated)
		driveDirs     map[string]string // drive -> subdir of base, created unless listed in missingDirs
		searchDirs    []string          // subdirs of base used as index roots
		missingDirs   []string          // referenced subdirs deliberately left uncreated
		caseSensitive bool
		input         matchInput
		wantStatus    MediaFileStatus
		wantMatchType string
		wantMatches   int
		wantError     bool // non-empty out.Error
	}{
		{
			name:       "drive mapping missing file",
			driveDirs:  map[string]string{"O:": "drive"},
			input:      matchInput{FilePath: `O:\Audio\nope.wav`},
			wantStatus: MediaStatusMissing,
		},
		{
			name:       "index by filename",
			files:      []string{"idx/sub/unique.wav"},
			searchDirs: []string{"idx"},
			// No drive mapping for O: -> falls through to the index.
			input:         matchInput{FilePath: `O:\elsewhere\unique.wav`},
			wantStatus:    MediaStatusPresent,
			wantMatchType: matchTypeFilename,
			wantMatches:   1,
		},
		{
			name:          "index ambiguous",
			files:         []string{"idx/a/dup.wav", "idx/b/dup.wav"},
			searchDirs:    []string{"idx"},
			input:         matchInput{FileName: `O:\x\dup.wav`},
			wantStatus:    MediaStatusAmbiguous,
			wantMatchType: matchTypeFilename,
			wantMatches:   2,
		},
		{
			name:          "extension independent",
			files:         []string{"idx/song.flac"}, // DB says .wav, disk has .flac
			searchDirs:    []string{"idx"},
			input:         matchInput{FilePath: `O:\Audio\song.wav`},
			wantStatus:    MediaStatusPresent,
			wantMatchType: matchTypeFilenameNoExt,
			wantMatches:   1,
		},
		{
			name:       "metadata only is not a file reference",
			files:      []string{"idx/Artist - Title.mp3"},
			searchDirs: []string{"idx"},
			input:      matchInput{Artist: "Artist", TrackTitle: "Title"},
			wantStatus: MediaStatusNoReference,
		},
		{
			name:       "no reference",
			input:      matchInput{},
			wantStatus: MediaStatusNoReference,
		},
		{
			name:       "concrete path ignores bare title index hit",
			files:      []string{"idx/jingles/Liefdedealer.mp3"},
			driveDirs:  map[string]string{"O:": "drive"},
			searchDirs: []string{"idx"},
			input: matchInput{
				FilePath:   `O:\Audio\85\Blof - Liefdedealer.wav`,
				Artist:     "Blof",
				TrackTitle: "Liefdedealer",
			},
			wantStatus: MediaStatusMissing,
		},
		{
			name:          "case insensitive index hit",
			files:         []string{"idx/MixedCase.WAV"},
			searchDirs:    []string{"idx"},
			input:         matchInput{FileName: `O:\x\mixedcase.wav`},
			wantStatus:    MediaStatusPresent,
			wantMatchType: matchTypeFilename,
			wantMatches:   1,
		},
		{
			name:          "case sensitive index miss",
			files:         []string{"idx/MixedCase.WAV"},
			searchDirs:    []string{"idx"},
			caseSensitive: true,
			input:         matchInput{FileName: `O:\x\mixedcase.wav`},
			wantStatus:    MediaStatusMissing,
		},
		{
			// Drive mapped to a directory that cannot be opened -> stat error,
			// not missing, even when an index root has the basename.
			name:        "stat error wins over index fallback",
			files:       []string{"idx/x.wav"},
			driveDirs:   map[string]string{"O:": "gone"},
			missingDirs: []string{"gone"},
			searchDirs:  []string{"idx"},
			input:       matchInput{FilePath: `O:\Audio\x.wav`},
			wantStatus:  MediaStatusStatError,
			wantError:   true,
		},
		{
			// A correct exact path must win even when an index also has the basename.
			name:          "drive mapping prefers exact path over index",
			files:         []string{"drive/Audio/hit.wav", "idx/other/hit.wav"},
			driveDirs:     map[string]string{"O:": "drive"},
			searchDirs:    []string{"idx"},
			input:         matchInput{FilePath: `O:\Audio\hit.wav`},
			wantStatus:    MediaStatusPresent,
			wantMatchType: matchTypeExactPath,
			wantMatches:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			mkdir := func(sub string) string {
				dir := filepath.Join(base, sub)
				if !slices.Contains(tt.missingDirs, sub) {
					if err := os.MkdirAll(dir, 0o750); err != nil {
						t.Fatal(err)
					}
				}
				return dir
			}

			driveDirs := make(map[string]string, len(tt.driveDirs))
			for drive, sub := range tt.driveDirs {
				driveDirs[drive] = mkdir(sub)
			}
			var searchDirs []string
			for _, sub := range tt.searchDirs {
				searchDirs = append(searchDirs, mkdir(sub))
			}
			for _, f := range tt.files {
				writeFileAt(t, filepath.Join(base, filepath.FromSlash(f)), time.Now())
			}

			m := buildTestMatcher(t, driveDirs, searchDirs, !tt.caseSensitive)
			out := m.match(&tt.input)

			if out.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", out.Status, tt.wantStatus)
			}
			if out.MatchType != tt.wantMatchType {
				t.Errorf("matchType = %q, want %q", out.MatchType, tt.wantMatchType)
			}
			if len(out.Matches) != tt.wantMatches {
				t.Errorf("matches = %v, want %d entries", out.Matches, tt.wantMatches)
			}
			if tt.wantError && out.Error == "" {
				t.Error("expected non-empty error message")
			}
		})
	}
}

func TestMatch_StatTimeoutUsesSingleFlight(t *testing.T) {
	_, _, starts := stubMediaRootStat(t, nil, os.ErrNotExist)

	m := buildTestMatcher(t, map[string]string{"O:": t.TempDir()}, nil, true,
		withStatTimeout(10*time.Millisecond))

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
	dir := t.TempDir()
	full := filepath.Join(dir, "Audio", "hit.wav")
	writeFileAt(t, full, time.Now())
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	started, release, starts := stubMediaRootStat(t, info, nil)

	flights := new(statFlightGroup)
	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true, withStatFlights(flights))

	input := &matchInput{FilePath: `O:\Audio\hit.wav`}
	first := make(chan matchOutcome, 1)
	go func() { first <- m.match(input) }()
	mustReceive(t, started, "first stat start")

	go func() {
		time.Sleep(10 * time.Millisecond)
		release()
	}()
	second := m.match(input)
	firstOut := mustReceive(t, first, "first match outcome")

	for _, out := range []matchOutcome{firstOut, second} {
		if out.Status != MediaStatusPresent || out.MatchType != matchTypeExactPath {
			t.Fatalf("status=%q matchType=%q, want present/exact_path", out.Status, out.MatchType)
		}
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaRootStat starts = %d, want 1 shared successful stat", got)
	}
	waitForStatInflightEmpty(t, flights)
}

func TestMatch_StatFlightContextCancel(t *testing.T) {
	started, _, starts := stubMediaRootStat(t, nil, os.ErrNotExist)

	ctx, cancel := context.WithCancel(context.Background())
	m := buildTestMatcher(t, map[string]string{"O:": t.TempDir()}, nil, true,
		withMatcherContext(ctx))

	outCh := make(chan matchOutcome, 1)
	go func() { outCh <- m.match(&matchInput{FilePath: `O:\Audio\frozen.wav`}) }()
	mustReceive(t, started, "stat start")
	cancel()

	out := mustReceive(t, outCh, "match outcome")
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, context.Canceled.Error()) {
		t.Fatalf("status=%q error=%q, want context-canceled stat_error", out.Status, out.Error)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaRootStat starts = %d, want 1", got)
	}
}

func TestMatch_CompletedStatFlightIsRemoved(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "Audio", "hit.wav")
	writeFileAt(t, full, time.Now())
	info, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}

	_, release, starts := stubMediaRootStat(t, info, nil)
	release() // every stat completes immediately

	flights := new(statFlightGroup)
	m := buildTestMatcher(t, map[string]string{"O:": dir}, nil, true, withStatFlights(flights))

	input := &matchInput{FilePath: `O:\Audio\hit.wav`}
	if out := m.match(input); out.Status != MediaStatusPresent {
		t.Fatalf("first status = %q, want present", out.Status)
	}
	// The completed flight evicts itself asynchronously; wait so the second
	// match cannot join the finished flight. The fresh stat below is the proof
	// of eviction.
	waitForStatInflightEmpty(t, flights)

	if out := m.match(input); out.Status != MediaStatusPresent {
		t.Fatalf("second status = %q, want present", out.Status)
	}
	if got := starts.Load(); got != 2 {
		t.Fatalf("mediaRootStat starts = %d, want a fresh stat after completed flight cleanup", got)
	}
}

func TestBuildFileIndexRecordsWalkError(t *testing.T) {
	idx := buildFileIndexWithWalkDir(context.Background(), []string{filepath.Join(t.TempDir(), "missing")}, true, mediaWalkDir)

	if err := idx.err(); err == nil {
		t.Fatal("expected index error for missing root, got nil")
	}
}

func TestMatch_IndexTimeoutReportsStatError(t *testing.T) {
	_, starts := stubMediaWalkDir(t)

	m := buildTestMatcher(t, nil, []string{"/frozen-share"}, true,
		withIndexTimeout(10*time.Millisecond))

	out := m.match(&matchInput{FileName: `O:\Audio\frozen.wav`})
	if out.Status != MediaStatusStatError || !strings.Contains(out.Error, "media file index timeout") {
		t.Fatalf("status=%q error=%q, want index timeout stat_error", out.Status, out.Error)
	}
	if got := starts.Load(); got != 1 {
		t.Fatalf("mediaWalkDir starts = %d, want 1 bounded index build", got)
	}
}

func TestGetIndexContextCancel(t *testing.T) {
	started, starts := stubMediaWalkDir(t)

	ctx, cancel := context.WithCancel(context.Background())
	m := buildTestMatcher(t, nil, []string{"/frozen-share"}, true,
		withMatcherContext(ctx), withIndexTimeout(time.Hour))

	idxCh := make(chan *fileIndex, 1)
	go func() { idxCh <- m.getIndex() }()
	mustReceive(t, started, "index walk start")
	cancel()

	idx := mustReceive(t, idxCh, "file index")
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
