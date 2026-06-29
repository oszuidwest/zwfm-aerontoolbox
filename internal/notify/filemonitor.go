package notify

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FileAlertResult is the notification payload for one stale or recovered file.
type FileAlertResult struct {
	Name          string
	Path          string
	MaxAgeMinutes int
	ActualAge     time.Duration // Zero if file does not exist.
	Exists        bool
	Error         string // Non-empty when stat failed for a reason other than "not found".
	CheckedAt     time.Time
}

// SendFileAlerts sends one email listing newly stale, missing, or errored files.
func (s *NotificationService) SendFileAlerts(alerts []FileAlertResult) {
	if !IsConfigured(&s.config.Notifications.Email) || len(alerts) == 0 {
		return
	}

	subject, body := formatFileAlerts(alerts)
	s.sendAsync(subject, body)
}

// SendFileRecoveries sends one email listing files that recovered.
func (s *NotificationService) SendFileRecoveries(recoveries []FileAlertResult) {
	if !IsConfigured(&s.config.Notifications.Email) || len(recoveries) == 0 {
		return
	}

	subject, body := formatFileRecoveries(recoveries)
	s.sendAsync(subject, body)
}

func formatFileAlerts(alerts []FileAlertResult) (subject, body string) {
	if len(alerts) == 1 {
		subject = errorSubject("File monitor: " + fileLabel(&alerts[0]) + " stale")
	} else {
		subject = errorSubject(fmt.Sprintf("File monitor: %d files stale", len(alerts)))
	}

	var b strings.Builder
	b.WriteString("File monitor failed\n\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", alerts[0].CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Count:     %d\n\n", len(alerts))

	for i := range alerts {
		a := &alerts[i]
		fmt.Fprintf(&b, "  %s\n", fileLabel(a))
		fmt.Fprintf(&b, "    Path:             %s\n", a.Path)
		fmt.Fprintf(&b, "    Max age:          %d minutes\n", a.MaxAgeMinutes)
		switch {
		case a.Error != "":
			fmt.Fprintf(&b, "    Status:           Error: %s\n", a.Error)
		case !a.Exists:
			b.WriteString("    Status:           File not found\n")
		default:
			fmt.Fprintf(&b, "    Current age:      %.0f minutes\n", math.Round(a.ActualAge.Minutes()))
		}
		b.WriteString("\n")
	}

	return subject, b.String()
}

func formatFileRecoveries(recoveries []FileAlertResult) (subject, body string) {
	if len(recoveries) == 1 {
		subject = okSubject("File monitor: " + fileLabel(&recoveries[0]) + " recovered")
	} else {
		subject = okSubject(fmt.Sprintf("File monitor: %d files recovered", len(recoveries)))
	}

	var b strings.Builder
	b.WriteString("File monitor recovered\n\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", recoveries[0].CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Count:     %d\n\n", len(recoveries))

	for i := range recoveries {
		r := &recoveries[i]
		fmt.Fprintf(&b, "  %s\n", fileLabel(r))
		fmt.Fprintf(&b, "    Path:             %s\n", r.Path)
		b.WriteString("    Status:           Current again\n\n")
	}

	return subject, b.String()
}

// fileLabel returns the display name for a file alert, preferring the name over the path.
func fileLabel(a *FileAlertResult) string {
	if a.Name != "" {
		return a.Name
	}
	return a.Path
}
