package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MediaFileStatus classifies the on-disk verdict for a playlist item.
type MediaFileStatus string

const (
	// MediaStatusPresent means a file matching the reference was found on disk.
	MediaStatusPresent MediaFileStatus = "present"
	// MediaStatusMissing means no matching file was found.
	MediaStatusMissing MediaFileStatus = "missing"
	// MediaStatusAmbiguous means more than one file matched the reference.
	MediaStatusAmbiguous MediaFileStatus = "ambiguous"
	// MediaStatusNoReference means the item carries no usable file reference.
	MediaStatusNoReference MediaFileStatus = "no_reference"
	// MediaStatusStatError means a file could not be checked (timeout, permission, etc.).
	MediaStatusStatError MediaFileStatus = "stat_error"
)

// Match strategy labels stored in MediaCheckItemResult.MatchType.
const (
	matchTypeExactPath     = "exact_path"
	matchTypeFilename      = "filename"
	matchTypeFilenameNoExt = "filename_noext"
)

const maxFileIndexErrors = 5

// normalizeDriveKey converts a Windows drive prefix to canonical "O:" form.
// It returns "" when s is not a single-letter drive.
func normalizeDriveKey(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, `\`)
	s = strings.TrimSuffix(s, ":")
	if len(s) != 1 {
		return ""
	}
	c := s[0]
	if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
		return ""
	}
	return strings.ToUpper(s) + ":"
}

// driveOf returns the canonical drive prefix of a Windows path.
func driveOf(winPath string) string {
	if len(winPath) < 2 || winPath[1] != ':' {
		return ""
	}
	return normalizeDriveKey(winPath[:2])
}

// winPathComponents splits a Windows path into safe relative components.
// It returns nil when the path escapes its root via "..".
func winPathComponents(winPath string) []string {
	rest := winPath
	if d := driveOf(winPath); d != "" {
		rest = winPath[2:]
	}
	rest = strings.ReplaceAll(rest, `\`, "/")
	var out []string
	for _, part := range strings.Split(rest, "/") {
		switch part {
		case "", ".":
			continue
		case "..":
			return nil // refuse traversal
		default:
			out = append(out, part)
		}
	}
	return out
}

// baseName returns the final path component of a Windows or Unix path.
func baseName(p string) string {
	p = strings.ReplaceAll(p, `\`, "/")
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// stem returns a filename with its extension removed. Names without an
// extension are returned unchanged.
func stem(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// foldKey lowercases s when lookups should ignore host filesystem casing.
func foldKey(s string, caseInsensitive bool) string {
	if caseInsensitive {
		return strings.ToLower(s)
	}
	return s
}

// fileIndex maps filename keys to the absolute on-disk paths that carry them.
// byName is keyed by the full filename (with extension); byStem by the filename
// without extension. Keys are folded according to the matcher's case setting.
type fileIndex struct {
	byName map[string][]string
	byStem map[string][]string
	count  int
	errors []string
}

// buildFileIndex indexes regular files by full filename and stem.
// Per-file walk errors are logged and skipped; ctx cancellation aborts the walk.
func buildFileIndex(ctx context.Context, dirs []string, caseInsensitive bool) *fileIndex {
	idx := &fileIndex{
		byName: make(map[string][]string),
		byStem: make(map[string][]string),
	}
	add := func(m map[string][]string, key, full string) {
		key = foldKey(key, caseInsensitive)
		for _, existing := range m[key] {
			if existing == full {
				return
			}
		}
		m[key] = append(m[key], full)
	}

	for _, dir := range dirs {
		if ctx != nil && ctx.Err() != nil {
			idx.addError(fmt.Sprintf("index build canceled before %s: %v", dir, ctx.Err()))
			break
		}
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				idx.addError(fmt.Sprintf("%s: %v", path, err))
				slog.Warn("Media file check: walk error", "path", path, "error", err)
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() || !d.Type().IsRegular() {
				return nil
			}
			name := d.Name()
			add(idx.byName, name, path)
			add(idx.byStem, stem(name), path)
			idx.count++
			return nil
		})
		if err != nil {
			idx.addError(fmt.Sprintf("%s: %v", dir, err))
			slog.Warn("Media file check: index walk stopped", "dir", dir, "error", err)
			if ctx != nil && ctx.Err() != nil {
				break
			}
		}
	}
	return idx
}

func (idx *fileIndex) addError(msg string) {
	if len(idx.errors) < maxFileIndexErrors {
		idx.errors = append(idx.errors, msg)
		return
	}
	if len(idx.errors) == maxFileIndexErrors {
		idx.errors = append(idx.errors, "additional index errors omitted")
	}
}

func (idx *fileIndex) err() error {
	if idx == nil || len(idx.errors) == 0 {
		return nil
	}
	return fmt.Errorf("media file index incomplete: %s", strings.Join(idx.errors, "; "))
}

// rootDir is an opened drive-mapping target. root is nil when the directory
// could not be opened (e.g. the mount is down); openErr then explains why.
type rootDir struct {
	dir     string
	root    *os.Root
	openErr error
}

// mediaMatcher resolves database references to files on disk using two
// strategies: exact path translation via drive mounts, then a filename index
// over the configured search dirs (built lazily on first fallback).
type mediaMatcher struct {
	driveRoots      map[string]*rootDir
	searchDirs      []string
	caseInsensitive bool
	statTimeout     time.Duration

	ctx       context.Context
	index     *fileIndex
	indexOnce sync.Once
}

// matchInput is the database-free item data classified by mediaMatcher.
type matchInput struct {
	FilePath   string // audio.filepath (full Windows path)
	FileName   string // audio.filename (full Windows path)
	AudioName  string // audio.name (basename without extension)
	Artist     string
	TrackTitle string
}

// matchOutcome is the matcher verdict for one item.
type matchOutcome struct {
	Status       MediaFileStatus
	DBReference  string
	CheckedPaths []string
	Matches      []string
	MatchType    string
	Error        string
}

// dbReference picks the most meaningful reference string for display.
func (in *matchInput) dbReference() string {
	switch {
	case in.FilePath != "":
		return in.FilePath
	case in.FileName != "":
		return in.FileName
	case in.AudioName != "":
		return "name: " + in.AudioName
	case in.Artist != "" || in.TrackTitle != "":
		return "metadata: " + util.FormatArtistTitle(in.Artist, in.TrackTitle)
	default:
		return ""
	}
}

// winRefs returns the distinct full Windows-path references for an item.
func (in *matchInput) winRefs() []string {
	var refs []string
	for _, r := range []string{in.FilePath, in.FileName} {
		if r == "" {
			continue
		}
		if !slices.Contains(refs, r) {
			refs = append(refs, r)
		}
	}
	return refs
}

// match classifies a single item across both resolution strategies.
func (m *mediaMatcher) match(in *matchInput) matchOutcome {
	out := matchOutcome{DBReference: in.dbReference()}

	winRefs := in.winRefs()
	hasRef := len(winRefs) > 0 || in.AudioName != ""
	if !hasRef {
		out.Status = MediaStatusNoReference
		return out
	}

	// Strategy 1: exact path via drive mapping.
	var statErr string
	for _, ref := range winRefs {
		candidate, exists, err := m.statDriveMapped(ref)
		if candidate == "" {
			continue // no mapping for this drive
		}
		out.CheckedPaths = append(out.CheckedPaths, candidate)
		switch {
		case err != nil:
			statErr = err.Error()
		case exists:
			out.Status = MediaStatusPresent
			out.MatchType = matchTypeExactPath
			out.Matches = []string{candidate}
			return out
		}
	}
	if statErr != "" {
		out.Status = MediaStatusStatError
		out.Error = statErr
		return out
	}

	// Strategy 2: filename index over search dirs (exact filename, then ext-independent).
	if idx := m.getIndex(); idx != nil {
		// Exact filename (with extension).
		var names []string
		for _, ref := range winRefs {
			names = appendUnique(names, baseName(ref))
		}
		if matches := idx.lookup(idx.byName, names, m.caseInsensitive); len(matches) > 0 {
			out.CheckedPaths = append(out.CheckedPaths, describeSearch("by name", names))
			finishIndexMatch(&out, matches, matchTypeFilename)
			return out
		}

		// Extension-independent: stems from filenames and audio.name.
		var stems []string
		for _, ref := range winRefs {
			stems = appendUnique(stems, stem(baseName(ref)))
		}
		if in.AudioName != "" {
			stems = appendUnique(stems, in.AudioName)
		}
		if matches := idx.lookup(idx.byStem, stems, m.caseInsensitive); len(matches) > 0 {
			out.CheckedPaths = append(out.CheckedPaths, describeSearch("by name (any extension)", stems))
			finishIndexMatch(&out, matches, matchTypeFilenameNoExt)
			return out
		}
		out.CheckedPaths = append(out.CheckedPaths, describeSearch("by name", names))
	}
	out.Status = MediaStatusMissing
	return out
}

// finishIndexMatch sets present/ambiguous based on how many files matched.
func finishIndexMatch(out *matchOutcome, matches []string, matchType string) {
	out.Matches = matches
	out.MatchType = matchType
	if len(matches) == 1 {
		out.Status = MediaStatusPresent
	} else {
		out.Status = MediaStatusAmbiguous
	}
}

// statDriveMapped maps a Windows reference to a host path and stats it.
// It returns ("", false, nil) when no drive mapping applies.
func (m *mediaMatcher) statDriveMapped(ref string) (candidate string, exists bool, err error) {
	drive := driveOf(ref)
	if drive == "" {
		return "", false, nil
	}
	rd, ok := m.driveRoots[drive]
	if !ok {
		return "", false, nil
	}

	components := winPathComponents(ref)
	if len(components) == 0 {
		return "", false, nil
	}
	rel := filepath.Join(components...)
	candidate = filepath.Join(rd.dir, rel)

	if rd.root == nil {
		return candidate, false, fmt.Errorf("drive %s root unavailable: %w", drive, rd.openErr)
	}

	exists, err = statRootWithTimeout(rd.root, rel, m.statTimeout)
	return candidate, exists, err
}

// getIndex returns the lazily-built filename index, or nil when no search dirs
// are configured. The index is built at most once per matcher; concurrent
// callers that fall through to the index strategy block on the same build via Once.
func (m *mediaMatcher) getIndex() *fileIndex {
	m.indexOnce.Do(func() {
		if len(m.searchDirs) == 0 {
			return
		}
		start := time.Now()
		m.index = buildFileIndex(m.ctx, m.searchDirs, m.caseInsensitive)
		if err := m.index.err(); err != nil {
			slog.Warn("Media file check: index built with errors",
				"files", m.index.count, "search_dirs", len(m.searchDirs),
				"duration", time.Since(start).Round(time.Millisecond).String(),
				"error", err)
			return
		}
		slog.Info("Media file check: index built",
			"files", m.index.count, "search_dirs", len(m.searchDirs),
			"duration", time.Since(start).Round(time.Millisecond).String())
	})
	return m.index
}

func (m *mediaMatcher) indexErr() error {
	if m == nil || m.index == nil {
		return nil
	}
	return m.index.err()
}

// lookup collects the distinct on-disk paths matching any of the given keys.
func (idx *fileIndex) lookup(table map[string][]string, keys []string, caseInsensitive bool) []string {
	var matches []string
	for _, key := range keys {
		if key == "" {
			continue
		}
		for _, full := range table[foldKey(key, caseInsensitive)] {
			matches = appendUnique(matches, full)
		}
	}
	return matches
}

// statRootWithTimeout stats name within root, bounding the call with timeout so
// a frozen mount cannot stall the worker. A timeout is reported as an error
// (the file's existence is unknown), a genuine absence as (false, nil).
func statRootWithTimeout(root *os.Root, name string, timeout time.Duration) (bool, error) {
	type result struct{ err error }
	ch := make(chan result, 1)
	go func() {
		// A frozen mount can leave this goroutine stuck in Stat after the caller
		// times out; the buffered channel prevents an additional blocked send.
		_, err := root.Stat(name)
		ch <- result{err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case r := <-ch:
		if r.err == nil {
			return true, nil
		}
		if errors.Is(r.err, fs.ErrNotExist) {
			// A stale mount that appears as an empty directory is indistinguishable
			// from a genuine absence at this layer.
			return false, nil
		}
		return false, r.err
	case <-timer.C:
		return false, fmt.Errorf("stat timeout after %s", timeout)
	}
}

// describeSearch renders an operator-facing note about an index lookup.
func describeSearch(how string, keys []string) string {
	return fmt.Sprintf("search_dirs (%s): %s", how, strings.Join(keys, ", "))
}

func appendUnique(s []string, v string) []string {
	if v == "" || slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}
