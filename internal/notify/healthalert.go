package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// HealthAlertResult is the notification payload for database health checks.
type HealthAlertResult struct {
	Recommendations    []string
	ActiveConnections  int
	MaxConnections     int
	ConnectionUsagePct float64
	LongRunningQueries []types.LongRunningQuery
	CheckedAt          time.Time
}

// HasIssues reports whether the check should keep the alert state active.
func (r *HealthAlertResult) HasIssues() bool {
	return len(r.Recommendations) > 0 || len(r.LongRunningQueries) > 0
}

// NotifyHealthCheckError opens the health alert state when the check itself fails.
// The next successful clean check will emit the matching recovery email.
func (s *NotificationService) NotifyHealthCheckError(err error) {
	if !IsConfigured(&s.config.Notifications.Email) || err == nil {
		return
	}

	s.stateMu.Lock()
	s.prevHealthAlertActive = true
	s.stateMu.Unlock()

	subject := "[ERROR] Database health check failed - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check failed\n\n")
	b.WriteString("The health check could not be completed. Check the database connection.\n\n")
	fmt.Fprintf(&b, "Timestamp: %s\n", time.Now().Format(timeFormat))
	fmt.Fprintf(&b, "Error:     %s\n", err.Error())

	s.sendAsync(subject, b.String())
}

// NotifyHealthAlert updates the database-health alert/recovery state.
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
	subject = "[ERROR] Database health check - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check reports problems\n\n")
	fmt.Fprintf(&b, "Timestamp:    %s\n", r.CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Connections:  %d / %d (%.0f%%)\n\n",
		r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct)

	if len(r.LongRunningQueries) > 0 {
		fmt.Fprintf(&b, "Long-running queries: %d\n", len(r.LongRunningQueries))
		for _, q := range r.LongRunningQueries {
			fmt.Fprintf(&b, "  PID %-6d  %s  %s\n", q.PID, q.Duration, q.State)
			if q.Query != "" {
				fmt.Fprintf(&b, "    Query: %s\n", q.Query)
			}
		}
		b.WriteString("\n")
	}

	if len(r.Recommendations) > 0 {
		fmt.Fprintf(&b, "Recommendations: %d\n", len(r.Recommendations))
		for _, rec := range r.Recommendations {
			fmt.Fprintf(&b, "  - %s\n", rec)
		}
	}

	return subject, b.String()
}

func formatHealthRecovery(r *HealthAlertResult) (subject, body string) {
	subject = "[OK] Database health check recovered - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check recovered\n\n")
	b.WriteString("All previously reported problems have been resolved.\n\n")
	fmt.Fprintf(&b, "Timestamp:    %s\n", r.CheckedAt.Format(timeFormat))
	fmt.Fprintf(&b, "Connections:  %d / %d (%.0f%%)\n",
		r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct)

	return subject, b.String()
}
