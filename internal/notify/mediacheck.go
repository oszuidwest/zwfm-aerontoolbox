package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MediaCheckProblem is a missing, ambiguous, or unchecked playlist item.
type MediaCheckProblem struct {
	Artist      string
	TrackTitle  string
	StartTime   string
	Block       string
	Status      string // "missing", "ambiguous", "stat_error", or "error"
	DBReference string
}

// MediaCheckResult is the notification payload for scheduled media file checks.
type MediaCheckResult struct {
	CheckedAt time.Time
	Scope     string
	Total     int
	Problems  []MediaCheckProblem
}

// NotifyMediaCheckResult updates the media-file alert/recovery state.
func (s *NotificationService) NotifyMediaCheckResult(r *MediaCheckResult) {
	if r == nil {
		return
	}
	s.notifyOnTransition(&s.prevMediaCheckFailed, len(r.Problems) > 0,
		func() (string, string) { return formatMediaCheckFailure(r) },
		func() (string, string) { return formatMediaCheckRecovery(r) },
	)
}

func formatMediaCheckFailure(r *MediaCheckResult) (subject, body string) {
	if len(r.Problems) == 1 {
		subject = errorSubject("Media file check: " + problemLabel(&r.Problems[0]))
	} else {
		subject = errorSubject(fmt.Sprintf("Media file check: %d problems", len(r.Problems)))
	}

	var b strings.Builder
	b.WriteString("Media file check found problems\n\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", r.CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Scope:     %s\n", r.Scope)
	fmt.Fprintf(&b, "Checked:   %d items\n", r.Total)
	fmt.Fprintf(&b, "Problems:  %d\n\n", len(r.Problems))

	for i := range r.Problems {
		p := &r.Problems[i]
		fmt.Fprintf(&b, "  [%s] %s\n", strings.ToUpper(p.Status), problemLabel(p))
		if p.StartTime != "" {
			fmt.Fprintf(&b, "    Start:     %s\n", p.StartTime)
		}
		if p.Block != "" {
			fmt.Fprintf(&b, "    Block:     %s\n", p.Block)
		}
		if p.DBReference != "" {
			fmt.Fprintf(&b, "    Reference: %s\n", p.DBReference)
		}
		b.WriteString("\n")
	}

	return subject, b.String()
}

func formatMediaCheckRecovery(r *MediaCheckResult) (subject, body string) {
	subject = okSubject("Media file check: recovered")

	var b strings.Builder
	b.WriteString("Media file check recovered\n\n")
	b.WriteString("All referenced files were found again after a previous failure.\n\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", r.CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Scope:     %s\n", r.Scope)
	fmt.Fprintf(&b, "Checked:   %d items\n", r.Total)

	return subject, b.String()
}

// problemLabel renders a short "artist - title" label, falling back to the
// reference when metadata is absent.
func problemLabel(p *MediaCheckProblem) string {
	if label := util.FormatArtistTitle(p.Artist, p.TrackTitle); label != "" {
		return label
	}
	if p.DBReference != "" {
		return p.DBReference
	}
	return "unknown item"
}
