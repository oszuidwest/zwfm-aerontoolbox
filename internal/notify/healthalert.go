package notify

import (
	"fmt"
	"strings"
	"time"
)

// HealthAlertResult contains the information needed to send a health check notification.
type HealthAlertResult struct {
	Recommendations    []string
	ActiveConnections  int
	MaxConnections     int
	ConnectionUsagePct float64
	LongRunningQueries []LongRunningQueryAlert
	CheckedAt          time.Time
}

// LongRunningQueryAlert contains information about a single long-running query.
type LongRunningQueryAlert struct {
	PID      int
	Duration string
	Query    string
	State    string
}

// HasIssues returns true if the health check found any problems.
func (r *HealthAlertResult) HasIssues() bool {
	return len(r.Recommendations) > 0 || len(r.LongRunningQueries) > 0
}

// NotifyHealthAlert sends a failure or recovery email based on the health check result.
func (s *NotificationService) NotifyHealthAlert(r *HealthAlertResult) {
	if !IsConfigured(&s.config.Notifications.Email) || r == nil {
		return
	}

	hasIssues := r.HasIssues()

	s.stateMu.Lock()
	prevActive := s.prevHealthAlertActive

	if hasIssues {
		s.prevHealthAlertActive = true
		s.stateMu.Unlock()

		subject, body := formatHealthAlert(r)
		s.sendAsync(subject, body)
		return
	}

	if prevActive {
		s.prevHealthAlertActive = false
		s.stateMu.Unlock()

		subject, body := formatHealthRecovery(r)
		s.sendAsync(subject, body)
		return
	}

	s.stateMu.Unlock()
}

func formatHealthAlert(r *HealthAlertResult) (subject, body string) {
	subject = "[FOUT] Database health check - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check meldt problemen\n\n")
	fmt.Fprintf(&b, "Tijdstip:     %s\n", r.CheckedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Connecties:   %d / %d (%.0f%%)\n\n", r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct) //nolint:misspell // Dutch word

	if len(r.LongRunningQueries) > 0 {
		fmt.Fprintf(&b, "Langlopende queries: %d\n", len(r.LongRunningQueries))
		for _, q := range r.LongRunningQueries {
			fmt.Fprintf(&b, "  PID %-6d  %s  %s\n", q.PID, q.Duration, q.State)
			if q.Query != "" {
				fmt.Fprintf(&b, "    Query: %s\n", q.Query)
			}
		}
		b.WriteString("\n")
	}

	if len(r.Recommendations) > 0 {
		fmt.Fprintf(&b, "Aanbevelingen: %d\n", len(r.Recommendations))
		for _, rec := range r.Recommendations {
			fmt.Fprintf(&b, "  - %s\n", rec)
		}
	}

	return subject, b.String()
}

func formatHealthRecovery(r *HealthAlertResult) (subject, body string) {
	subject = "[OK] Database health check hersteld - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check hersteld\n\n")
	b.WriteString("Alle eerder gemelde problemen zijn opgelost.\n\n")
	fmt.Fprintf(&b, "Tijdstip:     %s\n", r.CheckedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "Connecties:   %d / %d (%.0f%%)\n", r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct) //nolint:misspell // Dutch word

	return subject, b.String()
}
