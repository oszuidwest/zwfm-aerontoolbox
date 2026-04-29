package notify

import (
	"fmt"
	"regexp"
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
	fmt.Fprintf(&b, "Fout:      %s\n", formatEmailError(err.Error()))

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
	//nolint:misspell // Dutch word
	fmt.Fprintf(&b, "Connecties:   %d / %d (%.0f%%)\n\n",
		r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct)

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
			fmt.Fprintf(&b, "  - %s\n", formatHealthRecommendation(rec))
		}
	}

	return subject, b.String()
}

var healthRecommendationPatterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{
		re:   regexp.MustCompile(`^Connection usage is high: ([0-9]+)/([0-9]+) \(([0-9]+)%\) - check for connection leaks$`),
		repl: `Connectiegebruik is hoog: $1/$2 ($3%) - controleer op connectielekken`,
	},
	{
		re:   regexp.MustCompile(`^Long-running query detected \(PID ([0-9]+), running for (.+)\)$`),
		repl: `Langlopende query gedetecteerd (PID $1, actief gedurende $2)`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has ([0-9.]+)% dead tuples - VACUUM recommended$`),
		repl: `Tabel '$1' heeft $2% dode tuples - VACUUM aanbevolen`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has ([0-9]+) dead tuples - VACUUM recommended$`),
		repl: `Tabel '$1' heeft $2 dode tuples - VACUUM aanbevolen`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has never been vacuumed$`),
		repl: `Tabel '$1' is nog nooit gevacuumd`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has not been vacuumed in over ([0-9]+) days$`),
		repl: `Tabel '$1' is al meer dan $2 dagen niet gevacuumd`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has never been analyzed - ANALYZE recommended$`),
		repl: `Tabel '$1' is nog nooit geanalyseerd - ANALYZE aanbevolen`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has ([0-9]+) modifications since last ANALYZE - statistics stale$`),
		repl: `Tabel '$1' heeft $2 wijzigingen sinds de laatste ANALYZE - statistieken zijn verouderd`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has high sequential scans \(([0-9]+)\) vs index scans \(([0-9]+)\) - possible missing index$`),
		repl: `Tabel '$1' heeft veel sequential scans ($2) ten opzichte van index scans ($3) - mogelijk ontbreekt een index`,
	},
	{
		re:   regexp.MustCompile(`^Table '([^']+)' has (.+) of TOAST data \(images\)$`),
		repl: `Tabel '$1' heeft $2 aan TOAST-data (afbeeldingen)`,
	},
}

func formatHealthRecommendation(rec string) string {
	for _, pattern := range healthRecommendationPatterns {
		if pattern.re.MatchString(rec) {
			return pattern.re.ReplaceAllString(rec, pattern.repl)
		}
	}
	return rec
}

func formatHealthRecovery(r *HealthAlertResult) (subject, body string) {
	subject = "[OK] Database health check hersteld - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database health check hersteld\n\n")
	b.WriteString("Alle eerder gemelde problemen zijn opgelost.\n\n")
	fmt.Fprintf(&b, "Tijdstip:     %s\n", r.CheckedAt.Format(timeFormat))
	//nolint:misspell // Dutch word
	fmt.Fprintf(&b, "Connecties:   %d / %d (%.0f%%)\n",
		r.ActiveConnections, r.MaxConnections, r.ConnectionUsagePct)

	return subject, b.String()
}
