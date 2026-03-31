package notify

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// FileAlertResult contains information about a single file check for notification purposes.
type FileAlertResult struct {
	Name          string
	Path          string
	MaxAgeMinutes int
	ActualAge     time.Duration // Zero if file does not exist.
	Exists        bool
	Error         string // Non-empty when stat failed for a reason other than "not found".
	CheckedAt     time.Time
}

// SendFileAlerts sends a single email listing all newly detected stale or missing files.
func (s *NotificationService) SendFileAlerts(alerts []FileAlertResult) {
	if !IsConfigured(&s.config.Notifications.Email) || len(alerts) == 0 {
		return
	}

	subject, body := formatFileAlerts(alerts)
	s.sendAsync(subject, body)
}

// SendFileRecoveries sends a single email listing all files that have recovered.
func (s *NotificationService) SendFileRecoveries(recoveries []FileAlertResult) {
	if !IsConfigured(&s.config.Notifications.Email) || len(recoveries) == 0 {
		return
	}

	subject, body := formatFileRecoveries(recoveries)
	s.sendAsync(subject, body)
}

func formatFileAlerts(alerts []FileAlertResult) (subject, body string) {
	if len(alerts) == 1 {
		subject = fmt.Sprintf("[FOUT] Bestandscontrole: %s verouderd - Aeron Toolbox", fileLabel(&alerts[0]))
	} else {
		subject = fmt.Sprintf("[FOUT] Bestandscontrole: %d bestanden verouderd - Aeron Toolbox", len(alerts))
	}

	var b strings.Builder
	b.WriteString("Bestandscontrole mislukt\n\n")
	fmt.Fprintf(&b, "Tijdstip:  %s\n", alerts[0].CheckedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Aantal:    %d\n\n", len(alerts))

	for i := range alerts {
		a := &alerts[i]
		fmt.Fprintf(&b, "  %s\n", fileLabel(a))
		fmt.Fprintf(&b, "    Pad:              %s\n", a.Path)
		fmt.Fprintf(&b, "    Max. leeftijd:    %d minuten\n", a.MaxAgeMinutes)
		switch {
		case a.Error != "":
			fmt.Fprintf(&b, "    Status:           Fout: %s\n", a.Error)
		case !a.Exists:
			b.WriteString("    Status:           Bestand niet gevonden\n")
		default:
			fmt.Fprintf(&b, "    Huidige leeftijd: %.0f minuten\n", math.Round(a.ActualAge.Minutes()))
		}
		b.WriteString("\n")
	}

	return subject, b.String()
}

func formatFileRecoveries(recoveries []FileAlertResult) (subject, body string) {
	if len(recoveries) == 1 {
		subject = fmt.Sprintf("[OK] Bestandscontrole: %s hersteld - Aeron Toolbox", fileLabel(&recoveries[0]))
	} else {
		subject = fmt.Sprintf("[OK] Bestandscontrole: %d bestanden hersteld - Aeron Toolbox", len(recoveries))
	}

	var b strings.Builder
	b.WriteString("Bestandscontrole hersteld\n\n")
	fmt.Fprintf(&b, "Tijdstip:  %s\n", recoveries[0].CheckedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Aantal:    %d\n\n", len(recoveries))

	for i := range recoveries {
		r := &recoveries[i]
		fmt.Fprintf(&b, "  %s\n", fileLabel(r))
		fmt.Fprintf(&b, "    Pad:              %s\n", r.Path)
		b.WriteString("    Status:           Weer actueel\n\n")
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
