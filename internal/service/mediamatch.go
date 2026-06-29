package service

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MediaFileStatus classifies the on-disk outcome for a single playlist item.
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

// Match strategy labels reported in MediaCheckItemResult.MatchType.
const (
	matchTypeExactPath     = "exact_path"
	matchTypeFilename      = "filename"
	matchTypeFilenameNoExt = "filename_noext"
	matchTypeMetadata      = "metadata"
)

// normalizeDriveKey turns a Windows drive prefix ("O:", "o:\", "O") into the
// canonical "O:" form. It returns "" when s is not a single-letter drive.
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

// driveOf returns the canonical drive prefix of a Windows path, or "" if the
// path has no drive letter.
func driveOf(winPath string) string {
	if len(winPath) < 2 || winPath[1] != ':' {
		return ""
	}
	return normalizeDriveKey(winPath[:2])
}

// winPathComponents strips the drive prefix from a Windows path and splits the
// remainder into path components on either separator. Empty components and "."
// are dropped. It returns nil when the path escapes its root via "..".
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
	ext := filepath.Ext(name)
	return name[:len(name)-len(ext)]
}

// foldKey lowercases s when matching is case-insensitive, so lookups ignore the
// casing differences between Windows references and a case-sensitive host FS.
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
}

// buildFileIndex walks each root recursively and indexes every regular file by
// filename and by stem. Per-file walk errors are logged and skipped so one
// unreadable subtree does not abort the whole index. The walk aborts early if
// ctx is cancelled.
func buildFileIndex(ctx contextLike, roots []string, caseInsensitive bool) *fileIndex {
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

	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
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
			slog.Warn("Media file check: index walk stopped", "root", root, "error", err)
		}
	}
	return idx
}

// contextLike is the minimal context surface buildFileIndex needs. It avoids a
// hard context import here while letting callers pass a real context.Context.
type contextLike interface {
	Err() error
}

// rootDir is an opened drive-mapping target. root is nil when the directory
// could not be opened (e.g. the mount is down); openErr then explains why.
type rootDir struct {
	dir     string
	root    *os.Root
	openErr error
}

// mediaMatcher resolves database references to files on disk using two
// strategies: exact path translation via drive mappings, then a filename index
// over the configured roots (built lazily on first fallback).
type mediaMatcher struct {
	driveRoots      map[string]*rootDir
	roots           []string
	caseInsensitive bool
	statTimeout     time.Duration

	ctx       contextLike
	index     *fileIndex
	indexOnce sync.Once
}

// matchInput is the per-item data the matcher classifies. It is decoupled from
// the database row so the matcher can be unit-tested without a database.
type matchInput struct {
	FilePath   string // audio.filepath (full Windows path)
	FileName   string // audio.filename (full Windows path)
	AudioName  string // audio.name (basename without extension)
	Artist     string
	TrackTitle string
	TrackFound bool
}

// matchOutcome is the matcher's verdict for one item.
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
		return "metadata: " + metadataLabel(in.Artist, in.TrackTitle)
	default:
		return ""
	}
}

// metadataLabel renders the "artist - title" (or bare title) label.
func metadataLabel(artist, title string) string {
	if artist != "" && title != "" {
		return artist + " - " + title
	}
	if title != "" {
		return title
	}
	return artist
}

// winRefs returns the distinct full Windows-path references for an item.
func (in *matchInput) winRefs() []string {
	var refs []string
	for _, r := range []string{in.FilePath, in.FileName} {
		if r == "" {
			continue
		}
		if !sliceContains(refs, r) {
			refs = append(refs, r)
		}
	}
	return refs
}

// match classifies a single item across both resolution strategies.
func (m *mediaMatcher) match(in *matchInput) matchOutcome {
	out := matchOutcome{DBReference: in.dbReference()}

	winRefs := in.winRefs()
	hasRef := len(winRefs) > 0 || in.AudioName != "" || in.Artist != "" || in.TrackTitle != ""
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

	// Strategy 2: filename index over roots (exact filename, then ext-independent).
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

		// Extension-independent: stems from filenames, audio.name and metadata.
		var stems []string
		for _, ref := range winRefs {
			stems = appendUnique(stems, stem(baseName(ref)))
		}
		fileStemCount := len(stems)
		if in.AudioName != "" {
			stems = appendUnique(stems, in.AudioName)
		}
		if label := metadataLabel(in.Artist, in.TrackTitle); label != "" {
			stems = appendUnique(stems, label)
		}
		if in.TrackTitle != "" {
			stems = appendUnique(stems, in.TrackTitle)
		}
		if matches := idx.lookup(idx.byStem, stems, m.caseInsensitive); len(matches) > 0 {
			out.CheckedPaths = append(out.CheckedPaths, describeSearch("by name (any extension)", stems))
			mt := matchTypeFilenameNoExt
			if fileStemCount == 0 {
				mt = matchTypeMetadata
			}
			finishIndexMatch(&out, matches, mt)
			return out
		}
		out.CheckedPaths = append(out.CheckedPaths, describeSearch("by name", names))
	}

	// No match: a stat error outranks a clean "missing" because the file may in
	// fact exist but could not be verified.
	if statErr != "" {
		out.Status = MediaStatusStatError
		out.Error = statErr
		return out
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

// statDriveMapped translates a Windows reference through the drive mappings and
// stats the resulting host path. It returns ("", false, nil) when no mapping
// applies to the reference's drive.
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

// getIndex returns the lazily-built filename index, or nil when no roots are
// configured. The index is built at most once per matcher; concurrent callers
// that fall through to the index strategy block on the same build via Once.
func (m *mediaMatcher) getIndex() *fileIndex {
	m.indexOnce.Do(func() {
		if len(m.roots) == 0 {
			return
		}
		start := time.Now()
		m.index = buildFileIndex(m.ctx, m.roots, m.caseInsensitive)
		slog.Info("Media file check: index built",
			"files", m.index.count, "roots", len(m.roots),
			"duration", time.Since(start).Round(time.Millisecond).String())
	})
	return m.index
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
			return false, nil
		}
		return false, r.err
	case <-timer.C:
		return false, fmt.Errorf("stat timeout after %s", timeout)
	}
}

// describeSearch renders an operator-facing note about an index lookup.
func describeSearch(how string, keys []string) string {
	return fmt.Sprintf("roots (%s): %s", how, strings.Join(keys, ", "))
}

func appendUnique(s []string, v string) []string {
	if v == "" || sliceContains(s, v) {
		return s
	}
	return append(s, v)
}

func sliceContains(s []string, v string) bool {
	for _, e := range s {
		if e == v {
			return true
		}
	}
	return false
}
