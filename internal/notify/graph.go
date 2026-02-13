// Package notify provides email notifications via Microsoft Graph API.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

const (
	graphBaseURL     = "https://graph.microsoft.com/v1.0"
	graphScope       = "https://graph.microsoft.com/.default"
	tokenURLTemplate = "https://login.microsoftonline.com/%s/oauth2/v2.0/token" //nolint:gosec // URL template, not a credential

	// Retry settings.
	maxRetries       = 3
	initialRetryWait = 1 * time.Second
	maxRetryWait     = 30 * time.Second

	// HTTP client timeout.
	httpTimeout = 30 * time.Second
)

// GUIDPattern matches the standard GUID format.
var GUIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// GraphClient sends emails via Microsoft Graph API.
type GraphClient struct {
	fromAddress string
	httpClient  *http.Client
}

// NewGraphClient creates a new Graph API email client.
func NewGraphClient(cfg *config.GraphConfig) (*GraphClient, error) {
	if err := validateCredentials(cfg, false); err != nil {
		return nil, err
	}
	if cfg.FromAddress == "" {
		return nil, fmt.Errorf("from address (shared mailbox) is required")
	}

	conf := newCredentialsConfig(cfg)

	// Configure base HTTP client with timeout to prevent indefinite hangs
	baseClient := &http.Client{Timeout: httpTimeout}
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, baseClient)
	httpClient := conf.Client(ctx)

	return &GraphClient{
		fromAddress: cfg.FromAddress,
		httpClient:  httpClient,
	}, nil
}

// Graph API request types.

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

// SendMail sends a plain text email to the specified recipients.
func (c *GraphClient) SendMail(recipients []string, subject, body string) error {
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
	return c.doWithRetry(jsonData)
}

// doWithRetry sends the email request with automatic retries and exponential backoff.
func (c *GraphClient) doWithRetry(jsonData []byte) error {
	apiURL := fmt.Sprintf("%s/users/%s/sendMail", graphBaseURL, url.PathEscape(c.fromAddress))
	backoff := NewBackoff(initialRetryWait, maxRetryWait)

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(backoff.Next())
		}

		req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(jsonData))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
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
					time.Sleep(time.Duration(seconds) * time.Second)
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

// ValidateAuth verifies that the email credentials are valid by making a lightweight request.
func (c *GraphClient) ValidateAuth() error {
	apiURL := fmt.Sprintf("%s/users/%s", graphBaseURL, url.PathEscape(c.fromAddress))
	req, err := http.NewRequest(http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return fmt.Errorf("create validation request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
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

// ValidateConfig validates that cfg has all required fields with strict GUID format checking.
func ValidateConfig(cfg *config.GraphConfig) error {
	if err := validateCredentials(cfg, true); err != nil {
		return err
	}
	if cfg.FromAddress == "" {
		return fmt.Errorf("from address (shared mailbox) is required")
	}
	if cfg.Recipients == "" {
		return fmt.Errorf("recipients are required")
	}
	return nil
}

// IsConfigured reports whether the Graph configuration has the minimum required fields.
func IsConfigured(cfg *config.GraphConfig) bool {
	return cfg.TenantID != "" && cfg.ClientID != "" && cfg.ClientSecret != "" &&
		cfg.FromAddress != "" && cfg.Recipients != ""
}

// ParseRecipients splits a comma-separated recipients string into a slice.
func ParseRecipients(recipients string) []string {
	var result []string
	for _, r := range strings.Split(recipients, ",") {
		if r = strings.TrimSpace(r); r != "" {
			result = append(result, r)
		}
	}
	return result
}

// validateCredentials checks that required credential fields are present.
func validateCredentials(cfg *config.GraphConfig, strict bool) error {
	if cfg.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	if strict && !GUIDPattern.MatchString(cfg.TenantID) {
		return fmt.Errorf("tenant ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	if strict && !GUIDPattern.MatchString(cfg.ClientID) {
		return fmt.Errorf("client ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientSecret == "" {
		return fmt.Errorf("client secret is required")
	}
	return nil
}

// newCredentialsConfig creates an OAuth2 client credentials configuration.
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
