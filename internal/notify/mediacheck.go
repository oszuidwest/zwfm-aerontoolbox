package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MediaCheckProblem describes a single problematic playlist item (a file that is
// missing, ambiguous, or could not be checked).
type MediaCheckProblem struct {
	Artist      string
	TrackTitle  string
	StartTime   string
	Block       string
	Status      string // "missing", "ambiguous", "stat_error", or "error"
	DBReference string
}

// MediaCheckResult contains the information needed to send a media file check
// notification.
type MediaCheckResult struct {
	CheckedAt time.Time
	Scope     string
	Total     int
	Problems  []MediaCheckProblem
}

// NotifyMediaCheckResult sends a failure email when a scheduled check finds
// problems and a recovery email on the first clean run afterwards.
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
		subject = fmt.Sprintf("[ERROR] Media file check: %s - Aeron Toolbox", problemLabel(&r.Problems[0]))
	} else {
		subject = fmt.Sprintf("[ERROR] Media file check: %d problems - Aeron Toolbox", len(r.Problems))
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
	subject = "[OK] Media file check: recovered - Aeron Toolbox"

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
