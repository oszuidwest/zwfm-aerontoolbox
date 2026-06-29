package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

// timeFormat is the timestamp layout used in notification emails.
const timeFormat = "2006-01-02 15:04:05"

// errorSubject and okSubject format a notification subject line, wrapping a
// summary with the severity tag and the shared product suffix, e.g.
// okSubject("Backup recovered") -> "[OK] Backup recovered - Aeron Toolbox".
func errorSubject(summary string) string { return "[ERROR] " + summary + " - Aeron Toolbox" }
func okSubject(summary string) string    { return "[OK] " + summary + " - Aeron Toolbox" }

// pluralize returns one when n == 1, otherwise many. It keeps each formatter's
// singular/plural subject choice to a single expression.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// BackupResult is the notification payload for a backup run.
type BackupResult struct {
	Success   bool
	StartedAt *time.Time
	EndedAt   *time.Time
	Filename  string
	Error     string
}

// S3SyncResult is the notification payload for S3 backup synchronization.
type S3SyncResult struct {
	Synced bool
	Error  string
}

// NotificationService sends transition-based operational email alerts.
type NotificationService struct {
	config     *config.Config
	runner     *async.Runner
	recipients []string // parsed once at construction

	// Lazy Graph client (cached by config key)
	client    *GraphClient
	clientKey string
	clientMu  sync.Mutex

	// Recovery state (in-memory, lost on restart)
	prevBackupFailed      bool
	prevHealthAlertActive bool
	prevS3Failed          bool
	prevMediaCheckFailed  bool
	stateMu               sync.Mutex

	// Last notification send error (for health endpoint)
	lastError   string
	lastErrorAt *time.Time
	errorMu     sync.RWMutex

	// Secret expiry checker
	expiryChecker *SecretExpiryChecker
}

// New returns a notification service for cfg.
func New(cfg *config.Config) *NotificationService {
	emailCfg := &cfg.Notifications.Email
	svc := &NotificationService{
		config:     cfg,
		runner:     async.New(),
		recipients: ParseRecipients(emailCfg.Recipients),
	}

	if IsConfigured(emailCfg) {
		svc.expiryChecker = NewSecretExpiryChecker(emailCfg)
		slog.Info("Email notifications configured")
	} else {
		slog.Warn("Email notifications not configured - failures will not be reported by email")
	}

	return svc
}

// notifyOnTransition sends first-failure and first-recovery emails for one signal.
func (s *NotificationService) notifyOnTransition(
	prevFailed *bool,
	isFailing bool,
	formatFail func() (string, string),
	formatRecover func() (string, string),
) {
	if !IsConfigured(&s.config.Notifications.Email) {
		return
	}

	s.stateMu.Lock()
	prev := *prevFailed

	if isFailing {
		*prevFailed = true
		s.stateMu.Unlock()
		subject, body := formatFail()
		s.sendAsync(subject, body)
		return
	}

	if prev {
		*prevFailed = false
		s.stateMu.Unlock()
		subject, body := formatRecover()
		s.sendAsync(subject, body)
		return
	}

	s.stateMu.Unlock()
}

// NotifyBackupResult updates the backup alert/recovery state.
func (s *NotificationService) NotifyBackupResult(r *BackupResult) {
	if r == nil {
		return
	}
	s.notifyOnTransition(&s.prevBackupFailed, !r.Success,
		func() (string, string) { return s.formatBackupFailure(r) },
		func() (string, string) { return s.formatBackupRecovery(r) },
	)
}

// NotifyS3SyncResult updates the S3 sync alert/recovery state.
func (s *NotificationService) NotifyS3SyncResult(filename string, r *S3SyncResult) {
	if r == nil {
		return
	}
	s.notifyOnTransition(&s.prevS3Failed, !r.Synced,
		func() (string, string) { return s.formatS3Failure(filename, r) },
		func() (string, string) { return s.formatS3Recovery(filename) },
	)
}

// LastError returns the last notification send error and when it occurred.
func (s *NotificationService) LastError() (string, *time.Time) {
	s.errorMu.RLock()
	defer s.errorMu.RUnlock()
	return s.lastError, s.lastErrorAt
}

// SecretExpiry returns cached secret-expiry information, or nil when disabled.
// It never blocks on Graph API calls; stale cache data triggers an async refresh.
func (s *NotificationService) SecretExpiry() *SecretExpiryInfo {
	if s.expiryChecker == nil {
		return nil
	}
	info := s.expiryChecker.Info()
	return &info
}

// StartExpiryChecker warms the secret-expiry cache in the background.
func (s *NotificationService) StartExpiryChecker() {
	if s.expiryChecker != nil {
		go s.expiryChecker.refresh()
	}
}

// ValidateAuth verifies Microsoft Graph credentials and mailbox access.
func (s *NotificationService) ValidateAuth() error {
	client, err := s.getOrCreateClient()
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}
	return client.ValidateAuth()
}

// SendTestEmail sends a synchronous probe message to all configured recipients.
// The context controls cancellation of the request.
func (s *NotificationService) SendTestEmail(ctx context.Context) error {
	client, err := s.getOrCreateClient()
	if err != nil {
		return fmt.Errorf("client init: %w", err)
	}

	subject := "[TEST] Aeron Toolbox - Email notification test"
	body := fmt.Sprintf(
		"This is a test email from Aeron Toolbox.\n\nTimestamp: %s\n\n"+
			"If you received this email, notification settings are configured correctly.",
		time.Now().Format(timeFormat))

	return client.SendMail(ctx, s.recipients, subject, body)
}

// Close waits for tracked background notification work to finish.
func (s *NotificationService) Close() {
	s.runner.Close()
}

// getOrCreateClient returns the cached Graph client or builds one for current config.
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
// If the runner is closed the message is dropped, a warning is logged, and the
// drop is recorded so the health endpoint reflects it via LastError.
// TryGoBackground is used because sendAsync is called from scheduler jobs and
// HTTP handlers, not from within an active primary run.
func (s *NotificationService) sendAsync(subject, body string) {
	if !s.runner.TryGoBackground(func() {
		s.send(subject, body)
	}) {
		slog.Warn("Notification email not sent: service is closed", "subject", subject)
		s.trackError(errors.New("notification dropped: service is closed"))
	}
}

// sendTimeout is the maximum time allowed for sending a notification email.
const sendTimeout = 2 * time.Minute

// send delivers one email and records the last failure for health reporting.
func (s *NotificationService) send(subject, body string) {
	client, err := s.getOrCreateClient()
	if err != nil {
		slog.Error("Notification client creation failed", "error", err)
		s.trackError(err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	if err := client.SendMail(ctx, s.recipients, subject, body); err != nil {
		slog.Error("Notification email sending failed", "error", err, "subject", subject)
		s.trackError(err)
		return
	}

	slog.Info("Notification email sent", "subject", subject)
}

// trackError records the last notification error for the health endpoint.
func (s *NotificationService) trackError(err error) {
	s.errorMu.Lock()
	defer s.errorMu.Unlock()
	s.lastError = err.Error()
	now := time.Now()
	s.lastErrorAt = &now
}

func (s *NotificationService) formatBackupFailure(r *BackupResult) (subject, body string) {
	subject = errorSubject("Backup failed")

	var b strings.Builder
	b.WriteString("Backup failed\n\n")
	if r.StartedAt != nil {
		fmt.Fprintf(&b, "Timestamp:      %s\n", r.StartedAt.Format(timeFormat))
	}
	if r.StartedAt != nil && r.EndedAt != nil {
		fmt.Fprintf(&b, "Duration:       %s\n", r.EndedAt.Sub(*r.StartedAt).Round(time.Second))
	}
	if r.Filename != "" {
		fmt.Fprintf(&b, "Filename:       %s\n", r.Filename)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "Error:          %s\n", r.Error)
	}

	return subject, b.String()
}

func (s *NotificationService) formatBackupRecovery(r *BackupResult) (subject, body string) {
	subject = okSubject("Backup recovered")

	var b strings.Builder
	b.WriteString("Backup recovered\n\n")
	b.WriteString("Backup succeeded again after a previous failure.\n\n")
	if r.StartedAt != nil {
		fmt.Fprintf(&b, "Timestamp:      %s\n", r.StartedAt.Format(timeFormat))
	}
	if r.StartedAt != nil && r.EndedAt != nil {
		fmt.Fprintf(&b, "Duration:       %s\n", r.EndedAt.Sub(*r.StartedAt).Round(time.Second))
	}
	if r.Filename != "" {
		fmt.Fprintf(&b, "Filename:       %s\n", r.Filename)
	}

	return subject, b.String()
}

func (s *NotificationService) formatS3Failure(filename string, r *S3SyncResult) (subject, body string) {
	subject = errorSubject("S3 synchronization failed")

	var b strings.Builder
	b.WriteString("S3 synchronization failed\n\n")
	fmt.Fprintf(&b, "Timestamp:      %s\n", time.Now().Format(timeFormat))
	if filename != "" {
		fmt.Fprintf(&b, "Filename:       %s\n", filename)
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "Error:          %s\n", r.Error)
	}

	return subject, b.String()
}

func (s *NotificationService) formatS3Recovery(filename string) (subject, body string) {
	subject = okSubject("S3 synchronization recovered")

	var b strings.Builder
	b.WriteString("S3 synchronization recovered\n\n")
	b.WriteString("S3 synchronization succeeded again after a previous failure.\n\n")
	fmt.Fprintf(&b, "Timestamp:      %s\n", time.Now().Format(timeFormat))
	if filename != "" {
		fmt.Fprintf(&b, "Filename:       %s\n", filename)
	}

	return subject, b.String()
}
