// Package notify sends operational emails through Microsoft Graph.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

const (
	graphBaseURL     = "https://graph.microsoft.com/v1.0"
	graphScope       = "https://graph.microsoft.com/.default"
	tokenURLTemplate = "https://login.microsoftonline.com/%s/oauth2/v2.0/token" //nolint:gosec // URL template, not a credential

	maxRetries       = 3
	initialRetryWait = 1 * time.Second
	maxRetryWait     = 30 * time.Second

	httpTimeout = 30 * time.Second
)

// GraphClient sends plain-text mail through Microsoft Graph.
type GraphClient struct {
	fromAddress string
	httpClient  *http.Client
}

// NewGraphClient returns a Graph mail client for cfg.
func NewGraphClient(cfg *config.GraphConfig) (*GraphClient, error) {
	if err := validateCredentials(cfg, false); err != nil {
		return nil, err
	}
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("from address (shared mailbox) is required")
	}

	conf := newCredentialsConfig(cfg)

	baseClient := &http.Client{Timeout: httpTimeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, baseClient)
	httpClient := conf.Client(ctx)

	return &GraphClient{
		fromAddress: cfg.FromAddress,
		httpClient:  httpClient,
	}, nil
}

type graphMailRequest struct {
	Message graphMessage `json:"message"`
}

type graphMessage struct {
	Subject      string           `json:"subject"`
	Body         graphBody        `json:"body"`
	ToRecipients []graphRecipient `json:"toRecipients"`
}

type graphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type graphRecipient struct {
	EmailAddress graphEmailAddress `json:"emailAddress"`
}

type graphEmailAddress struct {
	Address string `json:"address"`
}

// SendMail sends one plain-text email to the non-empty recipients.
// The context controls cancellation of the request and any retries.
func (c *GraphClient) SendMail(ctx context.Context, recipients []string, subject, body string) error {
	if len(recipients) == 0 {
		return fmt.Errorf("no recipients specified")
	}

	toRecipients := make([]graphRecipient, 0, len(recipients))
	for _, addr := range recipients {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			toRecipients = append(toRecipients, graphRecipient{
				EmailAddress: graphEmailAddress{Address: addr},
			})
		}
	}

	if len(toRecipients) == 0 {
		return fmt.Errorf("no valid recipients after filtering")
	}

	payload := graphMailRequest{
		Message: graphMessage{
			Subject: subject,
			Body: graphBody{
				ContentType: "Text",
				Content:     body,
			},
			ToRecipients: toRecipients,
		},
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	return c.doWithRetry(ctx, jsonData)
}

// doWithRetry sends the request with bounded retries and exponential backoff.
// Context cancellation stops the retry loop immediately.
func (c *GraphClient) doWithRetry(ctx context.Context, jsonData []byte) error {
	apiURL := fmt.Sprintf("%s/users/%s/sendMail", graphBaseURL, url.PathEscape(c.fromAddress))
	backoff := NewBackoff(initialRetryWait, maxRetryWait)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("request cancelled: %w", err)
		}

		if attempt > 0 {
			if err := sleepWithContext(ctx, backoff.Next()); err != nil {
				return fmt.Errorf("request cancelled during retry wait: %w", err)
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL is hardcoded Microsoft Graph API endpoint
		if err != nil {
			lastErr = fmt.Errorf("send request: %w", err)
			continue
		}

		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		switch resp.StatusCode {
		case http.StatusAccepted, http.StatusOK, http.StatusNoContent:
			return nil
		case http.StatusTooManyRequests:
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
					if err := sleepWithContext(ctx, time.Duration(seconds)*time.Second); err != nil {
						return fmt.Errorf("request cancelled during rate limit wait: %w", err)
					}
				}
			}
			lastErr = fmt.Errorf("graph API rate limited (429): %s", string(respBody))
			continue
		case http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			lastErr = fmt.Errorf("graph API returned %d: %s", resp.StatusCode, string(respBody))
			continue
		default:
			return fmt.Errorf("graph API error %d: %s", resp.StatusCode, string(respBody))
		}
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// sleepWithContext sleeps for d unless ctx is cancelled first.
func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ValidateAuth verifies credentials by requesting the configured mailbox.
func (c *GraphClient) ValidateAuth() error {
	apiURL := fmt.Sprintf("%s/users/%s", graphBaseURL, url.PathEscape(c.fromAddress))
	req, err := http.NewRequest(http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("create validation request: %w", err)
	}

	resp, err := c.httpClient.Do(req) //nolint:gosec // G704: URL is hardcoded Microsoft Graph API endpoint
	if err != nil {
		if strings.Contains(err.Error(), "oauth2") || strings.Contains(err.Error(), "token") {
			return fmt.Errorf("authentication failed: %w", err)
		}
		return fmt.Errorf("validation request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusForbidden:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("mailbox %s not found", c.fromAddress)
	case http.StatusUnauthorized:
		return fmt.Errorf("authentication failed: invalid credentials")
	default:
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("validation failed with status %d: %s", resp.StatusCode, string(body))
	}
}

// ValidateConfig validates required Graph email fields and GUID shapes.
func ValidateConfig(cfg *config.GraphConfig) error {
	if err := validateCredentials(cfg, true); err != nil {
		return err
	}
	if cfg.FromAddress == "" {
		return fmt.Errorf("from address (shared mailbox) is required")
	}
	if len(ParseRecipients(cfg.Recipients)) == 0 {
		return fmt.Errorf("at least one valid recipient is required")
	}
	return nil
}

// IsConfigured reports whether enough Graph email config exists to send mail.
func IsConfigured(cfg *config.GraphConfig) bool {
	if cfg.TenantID == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.FromAddress == "" {
		return false
	}
	return len(ParseRecipients(cfg.Recipients)) > 0
}

// ParseRecipients splits comma-separated recipients and drops empty entries.
func ParseRecipients(recipients string) []string {
	var result []string
	for r := range strings.SplitSeq(recipients, ",") {
		if r = strings.TrimSpace(r); r != "" {
			result = append(result, r)
		}
	}
	return result
}

// validateCredentials checks credential presence and, in strict mode, GUID shape.
func validateCredentials(cfg *config.GraphConfig, strict bool) error {
	if cfg.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	if strict && !util.GUIDPattern.MatchString(cfg.TenantID) {
		return fmt.Errorf("tenant ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	if strict && !util.GUIDPattern.MatchString(cfg.ClientID) {
		return fmt.Errorf("client ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientSecret == "" {
		return fmt.Errorf("client secret is required")
	}
	return nil
}

// newCredentialsConfig builds the OAuth2 client-credentials config.
func newCredentialsConfig(cfg *config.GraphConfig) *clientcredentials.Config {
	return &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     fmt.Sprintf(tokenURLTemplate, cfg.TenantID),
		Scopes:       []string{graphScope},
	}
}

// tokenSourceContext returns an OAuth2 token source bound to the given context.
// The context controls token acquisition timeouts.
func tokenSourceContext(ctx context.Context, cfg *config.GraphConfig) (oauth2.TokenSource, error) {
	if err := validateCredentials(cfg, false); err != nil {
		return nil, err
	}
	return newCredentialsConfig(cfg).TokenSource(ctx), nil
}
