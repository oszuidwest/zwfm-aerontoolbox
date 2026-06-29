package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// mediaCheckParallelism caps how many items are stat'd concurrently per run.
const mediaCheckParallelism = 8

// mediaCheckRunTimeout bounds a single check run end-to-end. It is generous
// because indexing a large network share can be slow; per-stat timeouts guard
// against individually frozen files.
const mediaCheckRunTimeout = 30 * time.Minute

// mediaCheckNotifier is the notification subset required by the media file check.
type mediaCheckNotifier interface {
	NotifyMediaCheckResult(*notify.MediaCheckResult)
}

// MediaCheckScope echoes the effective scope of a check run.
type MediaCheckScope struct {
	Date               string `json:"date,omitempty"`
	From               string `json:"from,omitempty"`
	To                 string `json:"to,omitempty"`
	BlockID            string `json:"block_id,omitempty"`
	LookaheadDays      int    `json:"lookahead_days,omitempty"`
	Limit              int    `json:"limit,omitempty"`
	ExcludeVoicetracks bool   `json:"exclude_voicetracks"`
}

// MediaCheckSummary aggregates item outcomes for a run.
type MediaCheckSummary struct {
	Total       int `json:"total"`
	Present     int `json:"present"`
	Missing     int `json:"missing"`
	Ambiguous   int `json:"ambiguous"`
	NoReference int `json:"no_reference"`
	Errors      int `json:"errors"`
}

// MediaCheckItemResult is one operator-facing item verdict.
type MediaCheckItemResult struct {
	TrackID      string          `json:"trackid"`
	Artist       string          `json:"artist"`
	TrackTitle   string          `json:"tracktitle"`
	StartTime    string          `json:"start_time"`
	BlockID      string          `json:"block_id,omitempty"`
	Block        string          `json:"block"`
	Status       MediaFileStatus `json:"status"`
	DBReference  string          `json:"db_reference"`
	CheckedPaths []string        `json:"checked_paths"`
	Matches      []string        `json:"matches"`
	MatchType    string          `json:"match_type,omitempty"`
	Error        string          `json:"error,omitempty"`
}

// MediaCheckResult is the published result of one check run.
type MediaCheckResult struct {
	CheckedAt time.Time              `json:"checked_at"`
	Scope     MediaCheckScope        `json:"scope"`
	Summary   MediaCheckSummary      `json:"summary"`
	Items     []MediaCheckItemResult `json:"items"`
	Error     string                 `json:"error,omitempty"`
}

// MediaCheckStatus is the run-state snapshot returned by the status endpoint.
//
// Polling recipe after POST /check: read run_id from the response, then poll
// /status until completed_run_id >= run_id && running == false. The Result then
// reflects your run (or a later one that overtook it).
type MediaCheckStatus struct {
	Running        bool              `json:"running"`
	RunID          uint64            `json:"run_id"`
	CompletedRunID uint64            `json:"completed_run_id"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	Result         *MediaCheckResult `json:"result"`
}

// MediaFileCheckService verifies that playlist audio references exist on disk.
// Runs are single-flighted so manual and scheduled checks cannot overlap.
type MediaFileCheckService struct {
	repo   *database.Repository
	config *config.Config
	notify mediaCheckNotifier

	runner *async.Runner

	statInflightMu sync.Mutex
	statInflight   map[string]*statInFlight

	indexInflightMu sync.Mutex
	indexInflight   map[string]*indexInFlight

	publishedMu    sync.RWMutex
	lastResult     *MediaCheckResult
	lastScheduled  *MediaCheckResult
	startedAt      *time.Time
	runID          uint64
	completedRunID uint64
}

// newMediaFileCheckService returns a media file check service.
func newMediaFileCheckService(
	repo *database.Repository, cfg *config.Config, notifySvc *notify.NotificationService,
) *MediaFileCheckService {
	return &MediaFileCheckService{
		repo:          repo,
		config:        cfg,
		notify:        notifySvc,
		runner:        async.New(),
		statInflight:  make(map[string]*statInFlight),
		indexInflight: make(map[string]*indexInFlight),
	}
}

// Close waits for any running check to finish.
func (s *MediaFileCheckService) Close() {
	s.runner.Close()
}

// TriggerCheck starts an API-initiated check for the given scope in the
// background. The result is published for /status but no email is sent, because
// ad-hoc scopes would corrupt the scheduled-run alert transition state.
func (s *MediaFileCheckService) TriggerCheck(opts *database.MediaCheckOptions) (uint64, error) {
	return s.trigger(opts, false)
}

// TriggerScheduled starts the scheduled check (default scope: today) and emails
// alerts on transitions. Used by the cron scheduler.
func (s *MediaFileCheckService) TriggerScheduled() (uint64, error) {
	opts := s.scheduledOptions()
	return s.trigger(&opts, true)
}

// scheduledOptions builds the default scheduled scope and voicetrack policy.
func (s *MediaFileCheckService) scheduledOptions() database.MediaCheckOptions {
	return database.MediaCheckOptions{
		LookaheadDays:      s.config.MediaFileCheck.LookaheadDays,
		IncludeVoicetracks: s.config.MediaFileCheck.IncludeVoicetracks,
	}
}

// trigger acquires the single-flight lock and runs the check in the background.
func (s *MediaFileCheckService) trigger(opts *database.MediaCheckOptions, notifyResult bool) (uint64, error) {
	if !s.runner.TryStart() {
		return 0, types.NewConflictError("media_file_check", "media file check already running")
	}
	runID := s.startNewRun()
	s.runner.Go(func() {
		ctx, cancel := s.runner.Context(mediaCheckRunTimeout)
		defer cancel()

		result := s.executeRun(ctx, opts)
		s.publishCompleted(result, runID, notifyResult)
		if notifyResult {
			s.notify.NotifyMediaCheckResult(buildNotifyResult(result))
		}
	})
	return runID, nil
}

// executeRun fetches the in-scope items and classifies each against the disk.
func (s *MediaFileCheckService) executeRun(ctx context.Context, opts *database.MediaCheckOptions) *MediaCheckResult {
	result := &MediaCheckResult{
		CheckedAt: time.Now(),
		Scope:     scopeFromOptions(opts),
		Items:     []MediaCheckItemResult{},
	}

	items, err := s.repo.GetMediaCheckItems(ctx, opts)
	if err != nil {
		slog.Error("Media file check: failed to fetch items", "error", err)
		result.Error = err.Error()
		return result
	}

	matcher, closeMatcher := s.buildMatcher(ctx)
	defer closeMatcher()

	results := make([]MediaCheckItemResult, len(items))
	var g errgroup.Group
	g.SetLimit(mediaCheckParallelism)
	for i := range items {
		item := &items[i]
		g.Go(func() error {
			outcome := matcher.match(toMatchInput(item))
			results[i] = toItemResult(item, &outcome)
			return nil
		})
	}
	// Workers embed their verdict in results and never return an error, so Wait
	// only blocks for completion. A run-level failure surfaces as a context error
	// (timeout/cancel) or an index-build error, in that precedence.
	_ = g.Wait()
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
	}
	if err := matcher.indexErr(); err != nil && result.Error == "" {
		result.Error = err.Error()
	}

	result.Items = results
	result.Summary = summarize(results)

	slog.Info("Media file check completed",
		"total", result.Summary.Total,
		"present", result.Summary.Present,
		"missing", result.Summary.Missing,
		"ambiguous", result.Summary.Ambiguous,
		"no_reference", result.Summary.NoReference,
		"errors", result.Summary.Errors)

	return result
}

// buildMatcher opens the drive-mount targets and returns a matcher plus a
// cleanup function that closes them.
func (s *MediaFileCheckService) buildMatcher(ctx context.Context) (matcher *mediaMatcher, cleanup func()) {
	mfc := &s.config.MediaFileCheck

	driveRoots := make(map[string]*rootDir, len(mfc.DriveMounts))
	for drive, dir := range mfc.DriveMounts {
		key := normalizeDriveKey(drive)
		if key == "" {
			continue
		}
		rd := &rootDir{dir: dir}
		root, err := os.OpenRoot(dir)
		if err != nil {
			rd.openErr = err
			slog.Warn("Media file check: drive mount target unavailable", "drive", key, "dir", dir, "error", err)
		} else {
			rd.root = root
		}
		driveRoots[key] = rd
	}

	matcher = &mediaMatcher{
		driveRoots:       driveRoots,
		searchDirs:       mfc.SearchDirs,
		caseInsensitive:  mfc.IsCaseInsensitive(),
		statTimeout:      mfc.StatTimeout(),
		indexTimeout:     mediaCheckRunTimeout,
		ctx:              ctx,
		startStatFlight:  s.startOrJoinMediaStatFlight,
		startIndexFlight: s.startOrJoinMediaIndexFlight,
	}

	cleanup = func() {
		for _, rd := range driveRoots {
			if rd.root != nil {
				if err := rd.root.Close(); err != nil {
					slog.Debug("Media file check: failed to close drive root", "dir", rd.dir, "error", err)
				}
			}
		}
	}
	return matcher, cleanup
}

func (s *MediaFileCheckService) startOrJoinMediaStatFlight(
	key string,
	now time.Time,
	statFn func() (os.FileInfo, error),
) (*statInFlight, bool) {
	s.statInflightMu.Lock()
	defer s.statInflightMu.Unlock()

	if s.statInflight == nil {
		s.statInflight = make(map[string]*statInFlight)
	}
	if existing, ok := s.statInflight[key]; ok {
		return existing, false
	}

	flight := &statInFlight{done: make(chan struct{})}
	flight.startedNano.Store(now.UnixNano())
	s.statInflight[key] = flight

	go func() {
		flight.startedNano.Store(time.Now().UnixNano())
		info, err := statFn()
		flight.result = statResult{info: info, err: err}
		close(flight.done)

		s.statInflightMu.Lock()
		if s.statInflight[key] == flight {
			delete(s.statInflight, key)
		}
		s.statInflightMu.Unlock()
	}()

	return flight, true
}

func (s *MediaFileCheckService) startOrJoinMediaIndexFlight(
	key string,
	now time.Time,
	buildFn func() *fileIndex,
) (*indexInFlight, bool) {
	s.indexInflightMu.Lock()
	defer s.indexInflightMu.Unlock()

	if s.indexInflight == nil {
		s.indexInflight = make(map[string]*indexInFlight)
	}
	if existing, ok := s.indexInflight[key]; ok {
		return existing, false
	}

	flight := &indexInFlight{done: make(chan struct{})}
	flight.startedNano.Store(now.UnixNano())
	s.indexInflight[key] = flight

	go func() {
		flight.startedNano.Store(time.Now().UnixNano())
		flight.index = buildFn()
		close(flight.done)

		s.indexInflightMu.Lock()
		if s.indexInflight[key] == flight {
			delete(s.indexInflight, key)
		}
		s.indexInflightMu.Unlock()
	}()

	return flight, true
}

// Status returns a consistent snapshot of run-state plus the most recent result.
func (s *MediaFileCheckService) Status() *MediaCheckStatus {
	s.publishedMu.RLock()
	defer s.publishedMu.RUnlock()

	snap := &MediaCheckStatus{
		Running:        s.runner.IsRunning(),
		RunID:          s.runID,
		CompletedRunID: s.completedRunID,
		Result:         s.lastResult,
	}
	if s.startedAt != nil {
		t := *s.startedAt
		snap.StartedAt = &t
	}
	return snap
}

// ProblemCount returns the number of missing, ambiguous and errored items in the
// most recent scheduled run. Used by the health endpoint; ad-hoc API scopes are
// intentionally excluded from the health signal.
func (s *MediaFileCheckService) ProblemCount() int {
	s.publishedMu.RLock()
	defer s.publishedMu.RUnlock()
	if s.lastScheduled == nil {
		return 0
	}
	return problemCount(s.lastScheduled)
}

func (s *MediaFileCheckService) startNewRun() uint64 {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	s.runID++
	now := time.Now()
	s.startedAt = &now
	return s.runID
}

func (s *MediaFileCheckService) publishCompleted(result *MediaCheckResult, runID uint64, scheduled bool) {
	s.publishedMu.Lock()
	defer s.publishedMu.Unlock()
	s.lastResult = result
	if scheduled {
		s.lastScheduled = result
	}
	s.completedRunID = runID
}

func toMatchInput(item *database.MediaCheckItem) *matchInput {
	return &matchInput{
		FilePath:   item.FilePath,
		FileName:   item.FileName,
		AudioName:  item.AudioName,
		Artist:     item.Artist,
		TrackTitle: item.TrackTitle,
	}
}

func toItemResult(item *database.MediaCheckItem, out *matchOutcome) MediaCheckItemResult {
	checked := out.CheckedPaths
	if checked == nil {
		checked = []string{}
	}
	matches := out.Matches
	if matches == nil {
		matches = []string{}
	}
	return MediaCheckItemResult{
		TrackID:      item.TrackID,
		Artist:       item.Artist,
		TrackTitle:   item.TrackTitle,
		StartTime:    item.StartTime,
		BlockID:      item.BlockID,
		Block:        item.BlockName,
		Status:       out.Status,
		DBReference:  out.DBReference,
		CheckedPaths: checked,
		Matches:      matches,
		MatchType:    out.MatchType,
		Error:        out.Error,
	}
}

func summarize(items []MediaCheckItemResult) MediaCheckSummary {
	sum := MediaCheckSummary{Total: len(items)}
	for i := range items {
		switch items[i].Status {
		case MediaStatusPresent:
			sum.Present++
		case MediaStatusMissing:
			sum.Missing++
		case MediaStatusAmbiguous:
			sum.Ambiguous++
		case MediaStatusNoReference:
			sum.NoReference++
		case MediaStatusStatError:
			sum.Errors++
		}
	}
	return sum
}

func problemCount(result *MediaCheckResult) int {
	if result == nil {
		return 0
	}
	if result.Error != "" {
		return 1
	}
	sum := result.Summary
	return sum.Missing + sum.Ambiguous + sum.Errors
}

func scopeFromOptions(opts *database.MediaCheckOptions) MediaCheckScope {
	return MediaCheckScope{
		Date:               opts.Date,
		From:               opts.From,
		To:                 opts.To,
		BlockID:            opts.BlockID,
		LookaheadDays:      opts.LookaheadDays,
		Limit:              opts.Limit,
		ExcludeVoicetracks: !opts.IncludeVoicetracks,
	}
}

// buildNotifyResult converts a run result into the notification input, listing
// only the problem items (missing, ambiguous, stat_error). A run-level error is
// surfaced as a single problem so a failed scheduled run still alerts.
func buildNotifyResult(result *MediaCheckResult) *notify.MediaCheckResult {
	nr := &notify.MediaCheckResult{
		CheckedAt: result.CheckedAt,
		Scope:     scopeDescription(&result.Scope),
		Total:     result.Summary.Total,
	}
	if result.Error != "" {
		nr.Problems = append(nr.Problems, notify.MediaCheckProblem{
			Status:      "error",
			DBReference: result.Error,
		})
		return nr
	}
	for i := range result.Items {
		it := &result.Items[i]
		switch it.Status {
		case MediaStatusMissing, MediaStatusAmbiguous, MediaStatusStatError:
			nr.Problems = append(nr.Problems, notify.MediaCheckProblem{
				Artist:      it.Artist,
				TrackTitle:  it.TrackTitle,
				StartTime:   it.StartTime,
				Block:       it.Block,
				Status:      string(it.Status),
				DBReference: it.DBReference,
			})
		}
	}
	return nr
}

// scopeDescription renders a short human-readable scope for emails/logs.
func scopeDescription(scope *MediaCheckScope) string {
	var parts []string
	switch {
	case scope.BlockID != "":
		parts = append(parts, "block "+scope.BlockID)
	case scope.Date != "":
		parts = append(parts, "date "+scope.Date)
	case scope.From != "" || scope.To != "":
		parts = append(parts, fmt.Sprintf("range %s..%s", scope.From, scope.To))
	case scope.LookaheadDays > 0:
		parts = append(parts, fmt.Sprintf("today +%d days", scope.LookaheadDays))
	default:
		parts = append(parts, "today")
	}
	if scope.ExcludeVoicetracks {
		parts = append(parts, "excl. voicetracks")
	}
	return strings.Join(parts, ", ")
}
