package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// HealthAlertResult contains the information needed to send a health check notification.
type HealthAlertResult struct {
	Recommendations    []string
	ActiveConnections  int
	MaxConnections     int
	ConnectionUsagePct float64
	LongRunningQueries []types.LongRunningQuery
	CheckedAt          time.Time
}

// HasIssues returns true if the health check found any problems.
func (r *HealthAlertResult) HasIssues() bool {
	return len(r.Recommendations) > 0 || len(r.LongRunningQueries) > 0
}

// NotifyHealthCheckError sends an alert when the health check itself fails (e.g. database unreachable).
// Uses the same state tracking as NotifyHealthAlert so a recovery email is sent when the next check succeeds.
func (s *NotificationService) NotifyHealthCheckError(err error) {
	if !IsConfigured(&s.config.Notifications.Email) || err == nil {
		return
	}

	s.stateMu.Lock()
	s.prevHealthAlertActive = true
	s.stateMu.Unlock()

	subject := "[FOUT] Database health check mislukt - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check mislukt\n\n")
	b.WriteString("De health check kon niet worden uitgevoerd. Controleer de databaseverbinding.\n\n")
	fmt.Fprintf(&b, "Tijdstip:  %s\n", time.Now().Format(timeFormat))
	fmt.Fprintf(&b, "Fout:      %s\n", err.Error())

	s.sendAsync(subject, b.String())
}

// NotifyHealthAlert sends a failure or recovery email based on the health check result.
func (s *NotificationService) NotifyHealthAlert(r *HealthAlertResult) {
	if r == nil {
		return
	}
	s.notifyOnTransition(&s.prevHealthAlertActive, r.HasIssues(),
		func() (string, string) { return formatHealthAlert(r) },
		func() (string, string) { return formatHealthRecovery(r) },
	)
}

func formatHealthAlert(r *HealthAlertResult) (subject, body string) {
	subject = "[FOUT] Database health check - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check meldt problemen\n\n")
	fmt.Fprintf(&b, "Tijdstip:     %s\n", r.CheckedAt.Format(timeFormat))
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
	fmt.Fprintf(&b, "Tijdstip:     %s\n", r.CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Connecties:   %d / %d (%.0f%%)\n", r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct) //nolint:misspell // Dutch word

	return subject, b.String()
}
