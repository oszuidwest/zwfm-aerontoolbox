package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

// Input types for notification methods (avoids circular dependency with service package).

// BackupResult contains the information needed to send a backup notification.
type BackupResult struct {
	Success   bool
	StartedAt *time.Time
	EndedAt   *time.Time
	Filename  string
	Error     string
}

// S3SyncResult contains the information needed to send an S3 sync notification.
type S3SyncResult struct {
	Synced bool
	Error  string
}

// MaintenanceResult contains the information needed to send a maintenance notification.
type MaintenanceResult struct {
	Success      bool
	Operation    string
	StartedAt    *time.Time
	Error        string
	TablesTotal  int
	TablesOK     int
	TablesFailed int
	Tables       []MaintenanceTableResult
}

// MaintenanceTableResult contains per-table maintenance results for email formatting.
type MaintenanceTableResult struct {
	Table          string
	Success        bool
	Message        string
	Duration       string
	DeadTuples     int64
	DeadTupleRatio float64
	Skipped        bool
	SkippedReason  string
}

// NotificationService handles email notifications for backup, maintenance, and S3 sync events.
type NotificationService struct {
	config *config.Config
	runner *async.Runner

	// Lazy Graph client (cached by config key)
	client    *GraphClient
	clientKey string
	clientMu  sync.Mutex

	// Recovery state (in-memory, lost on restart)
	prevBackupFailed      bool
	prevMaintenanceFailed bool
	prevS3Failed          bool
	stateMu               sync.Mutex

	// Last notification send error (for health endpoint)
	lastError   string
	lastErrorAt *time.Time
	errorMu     sync.RWMutex

	// Secret expiry checker
	expiryChecker *SecretExpiryChecker
}

// New creates a new NotificationService.
func New(cfg *config.Config) *NotificationService {
	svc := &NotificationService{
		config: cfg,
		runner: async.New(),
	}

	emailCfg := &cfg.Notifications.Email
	if IsConfigured(emailCfg) {
		svc.expiryChecker = NewSecretExpiryChecker(emailCfg)
		slog.Info("E-mailnotificaties geconfigureerd")
	} else {
		slog.Warn("E-mailnotificaties niet geconfigureerd - failures worden niet gemeld per e-mail")
	}

	return svc
}

// NotifyBackupResult sends a failure or recovery email based on the backup result.
func (s *NotificationService) NotifyBackupResult(r *BackupResult) {
	if !IsConfigured(&s.config.Notifications.Email) || r == nil {
		return
	}

	s.stateMu.Lock()
	prevFailed := s.prevBackupFailed

	if !r.Success {
		s.prevBackupFailed = true
		s.stateMu.Unlock()

		subject, body := s.formatBackupFailure(r)
		s.sendAsync(subject, body)
		return
	}

	if prevFailed {
		s.prevBackupFailed = false
		s.stateMu.Unlock()

		subject, body := s.formatBackupRecovery(r)
		s.sendAsync(subject, body)
		return
	}

	s.stateMu.Unlock()
}

// NotifyS3SyncResult sends a failure or recovery email based on the S3 sync result.
func (s *NotificationService) NotifyS3SyncResult(filename string, r *S3SyncResult) {
	if !IsConfigured(&s.config.Notifications.Email) || r == nil {
		return
	}

	s.stateMu.Lock()
	prevFailed := s.prevS3Failed

	if !r.Synced {
		s.prevS3Failed = true
		s.stateMu.Unlock()

		subject, body := s.formatS3Failure(filename, r)
		s.sendAsync(subject, body)
		return
	}

	if prevFailed {
		s.prevS3Failed = false
		s.stateMu.Unlock()

		subject, body := s.formatS3Recovery(filename)
		s.sendAsync(subject, body)
		return
	}

	s.stateMu.Unlock()
}

// NotifyMaintenanceResult sends a failure or recovery email based on the maintenance result.
func (s *NotificationService) NotifyMaintenanceResult(r *MaintenanceResult) {
	if !IsConfigured(&s.config.Notifications.Email) || r == nil {
		return
	}

	hasFailed := !r.Success || r.TablesFailed > 0

	s.stateMu.Lock()
	prevFailed := s.prevMaintenanceFailed

	if hasFailed {
		s.prevMaintenanceFailed = true
		s.stateMu.Unlock()

		subject, body := s.formatMaintenanceFailure(r)
		s.sendAsync(subject, body)
		return
	}

	if prevFailed {
		s.prevMaintenanceFailed = false
		s.stateMu.Unlock()

		subject, body := s.formatMaintenanceRecovery(r)
		s.sendAsync(subject, body)
		return
	}

	s.stateMu.Unlock()
}

// LastError returns the last notification send error and when it occurred.
func (s *NotificationService) LastError() (string, *time.Time) {
	s.errorMu.RLock()
	defer s.errorMu.RUnlock()
	return s.lastError, s.lastErrorAt
}

// SecretExpiry returns the cached secret expiry information, or nil if not configured.
// This method never blocks on external API calls - it returns cached data and triggers
// an async refresh if the cache is stale.
func (s *NotificationService) SecretExpiry() *SecretExpiryInfo {
	if s.expiryChecker == nil {
		return nil
	}
	info := s.expiryChecker.Info()
	return &info
}

// StartExpiryChecker triggers an initial background refresh of the secret expiry info.
// Call this at startup to populate the cache before the first health check.
func (s *NotificationService) StartExpiryChecker() {
	if s.expiryChecker != nil {
		go s.expiryChecker.refresh()
	}
}

// SendTestEmail sends a synchronous test email to validate the notification setup.
// The context controls cancellation of the request.
func (s *NotificationService) SendTestEmail(ctx context.Context) error {
	client, err := s.getOrCreateClient()
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}

	recipients := ParseRecipients(s.config.Notifications.Email.Recipients)
	subject := "[TEST] Aeron Toolbox - E-mailnotificatie test"
	body := fmt.Sprintf("Dit is een test-e-mail van Aeron Toolbox.\n\nTijdstip: %s\n\nAls u deze e-mail ontvangt, zijn de notificatie-instellingen correct geconfigureerd.",
		time.Now().Format("2006-01-02 15:04:05"))

	return client.SendMail(ctx, recipients, subject, body)
}

// Close stops the notification service runner.
func (s *NotificationService) Close() {
	s.runner.Close()
}

// Internal methods.

// getOrCreateClient returns a cached or newly created GraphClient.
func (s *NotificationService) getOrCreateClient() (*GraphClient, error) {
	s.clientMu.Lock()
	defer s.clientMu.Unlock()

	cfg := &s.config.Notifications.Email
	key := cfg.TenantID + "|" + cfg.ClientID + "|" + cfg.ClientSecret + "|" + cfg.FromAddress

	if s.client != nil && s.clientKey == key {
		return s.client, nil
	}

	client, err := NewGraphClient(cfg)
	if err != nil {
		return nil, err
	}

	s.client = client
	s.clientKey = key
	return client, nil
}

// sendAsync sends an email asynchronously via the runner.
func (s *NotificationService) sendAsync(subject, body string) {
	s.runner.GoBackground(func() {
		s.send(subject, body)
	})
}

// sendTimeout is the maximum time allowed for sending a notification email.
const sendTimeout = 2 * time.Minute

// send sends an email and tracks any errors.
func (s *NotificationService) send(subject, body string) {
	client, err := s.getOrCreateClient()
	if err != nil {
		slog.Error("Notificatie client aanmaken mislukt", "error", err)
		s.trackError(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	recipients := ParseRecipients(s.config.Notifications.Email.Recipients)
	if err := client.SendMail(ctx, recipients, subject, body); err != nil {
		slog.Error("Notificatie e-mail verzenden mislukt", "error", err, "subject", subject)
		s.trackError(err)
		return
	}

	slog.Info("Notificatie e-mail verzonden", "subject", subject)
}

// trackError records the last notification error for the health endpoint.
func (s *NotificationService) trackError(err error) {
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	s.lastError = err.Error()
	now := time.Now()
	s.lastErrorAt = &now
}

// Email formatting.

func (s *NotificationService) formatBackupFailure(r *BackupResult) (subject, body string) {
	subject = "[FOUT] Backup mislukt - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Backup mislukt\n\n")
	if r.StartedAt != nil {
		b.WriteString(fmt.Sprintf("Tijdstip:       %s\n", r.StartedAt.Format("2006-01-02 15:04:05")))
	}
	if r.StartedAt != nil && r.EndedAt != nil {
		b.WriteString(fmt.Sprintf("Duur:           %s\n", r.EndedAt.Sub(*r.StartedAt).Round(time.Second)))
	}
	if r.Filename != "" {
		b.WriteString(fmt.Sprintf("Bestandsnaam:   %s\n", r.Filename))
	}
	if r.Error != "" {
		b.WriteString(fmt.Sprintf("Fout:           %s\n", r.Error))
	}

	return subject, b.String()
}

func (s *NotificationService) formatBackupRecovery(r *BackupResult) (subject, body string) {
	subject = "[OK] Backup hersteld - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Backup hersteld\n\n")
	b.WriteString("De backup is weer succesvol na een eerdere fout.\n\n")
	if r.StartedAt != nil {
		b.WriteString(fmt.Sprintf("Tijdstip:       %s\n", r.StartedAt.Format("2006-01-02 15:04:05")))
	}
	if r.StartedAt != nil && r.EndedAt != nil {
		b.WriteString(fmt.Sprintf("Duur:           %s\n", r.EndedAt.Sub(*r.StartedAt).Round(time.Second)))
	}
	if r.Filename != "" {
		b.WriteString(fmt.Sprintf("Bestandsnaam:   %s\n", r.Filename))
	}

	return subject, b.String()
}

func (s *NotificationService) formatS3Failure(filename string, r *S3SyncResult) (subject, body string) {
	subject = "[FOUT] S3-synchronisatie mislukt - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("S3-synchronisatie mislukt\n\n")
	b.WriteString(fmt.Sprintf("Tijdstip:       %s\n", time.Now().Format("2006-01-02 15:04:05")))
	if filename != "" {
		b.WriteString(fmt.Sprintf("Bestandsnaam:   %s\n", filename))
	}
	if r.Error != "" {
		b.WriteString(fmt.Sprintf("Fout:           %s\n", r.Error))
	}

	return subject, b.String()
}

func (s *NotificationService) formatS3Recovery(filename string) (subject, body string) {
	subject = "[OK] S3-synchronisatie hersteld - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("S3-synchronisatie hersteld\n\n")
	b.WriteString("De S3-synchronisatie is weer succesvol na een eerdere fout.\n\n")
	b.WriteString(fmt.Sprintf("Tijdstip:       %s\n", time.Now().Format("2006-01-02 15:04:05")))
	if filename != "" {
		b.WriteString(fmt.Sprintf("Bestandsnaam:   %s\n", filename))
	}

	return subject, b.String()
}

func (s *NotificationService) formatMaintenanceFailure(r *MaintenanceResult) (subject, body string) {
	allFailed := r.TablesOK == 0
	if allFailed {
		subject = "[FOUT] Database onderhoud mislukt - Aeron Toolbox"
	} else {
		subject = "[FOUT] Database onderhoud deels mislukt - Aeron Toolbox"
	}

	var b strings.Builder
	if allFailed {
		b.WriteString("Database onderhoud mislukt\n\n")
	} else {
		b.WriteString("Database onderhoud deels mislukt\n\n")
	}

	if r.Operation != "" {
		b.WriteString(fmt.Sprintf("Operatie:         %s\n", strings.ToUpper(strings.ReplaceAll(r.Operation, "_", " ")))) //nolint:misspell // Dutch word
	}
	if r.StartedAt != nil {
		b.WriteString(fmt.Sprintf("Tijdstip:         %s\n", r.StartedAt.Format("2006-01-02 15:04:05")))
	}
	if r.Error != "" {
		b.WriteString(fmt.Sprintf("Fout:             %s\n", r.Error))
	}

	b.WriteString(fmt.Sprintf("Tabellen totaal:  %d\n", r.TablesTotal))
	b.WriteString(fmt.Sprintf("Gelukt:           %d\n", r.TablesOK))
	b.WriteString(fmt.Sprintf("Mislukt:          %d\n", r.TablesFailed))

	if len(r.Tables) > 0 {
		b.WriteString("\nResultaten:\n")
		for _, t := range r.Tables {
			if t.Skipped {
				b.WriteString(fmt.Sprintf("  %-14s OVERGESLAGEN  %s\n", t.Table, t.SkippedReason))
				continue
			}
			statusLabel := "GELUKT"
			if !t.Success {
				statusLabel = "MISLUKT"
			}
			b.WriteString(fmt.Sprintf("  %-14s %-8s %s    dead tuples: %d (%.1f%%)\n",
				t.Table, statusLabel, t.Duration, t.DeadTuples, t.DeadTupleRatio))
			if !t.Success {
				b.WriteString(fmt.Sprintf("    Fout: %s\n", t.Message))
			}
		}
	}

	return subject, b.String()
}

func (s *NotificationService) formatMaintenanceRecovery(r *MaintenanceResult) (subject, body string) {
	subject = "[OK] Database onderhoud hersteld - Aeron Toolbox"

	var b strings.Builder
	b.WriteString("Database onderhoud hersteld\n\n")
	b.WriteString("Het database onderhoud is weer succesvol na een eerdere fout.\n\n")

	if r.Operation != "" {
		b.WriteString(fmt.Sprintf("Operatie:         %s\n", strings.ToUpper(strings.ReplaceAll(r.Operation, "_", " ")))) //nolint:misspell // Dutch word
	}
	if r.StartedAt != nil {
		b.WriteString(fmt.Sprintf("Tijdstip:         %s\n", r.StartedAt.Format("2006-01-02 15:04:05")))
	}

	b.WriteString(fmt.Sprintf("Tabellen totaal:  %d\n", r.TablesTotal))
	b.WriteString(fmt.Sprintf("Gelukt:           %d\n", r.TablesOK))

	return subject, b.String()
}
