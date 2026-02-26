package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
)

const (
	// expiryWarningDays is the number of days before expiration to show a warning.
	expiryWarningDays = 30
	// expiryCacheTTL is how long to cache the expiry info before re-checking.
	expiryCacheTTL = 1 * time.Hour
)

// SecretExpiryInfo contains information about client secret expiration.
type SecretExpiryInfo struct {
	ExpiresAt   string `json:"expires_at,omitempty"`
	ExpiresSoon bool   `json:"expires_soon"`
	DaysLeft    int    `json:"days_left"`
	Error       string `json:"error,omitempty"`
}

// SecretExpiryChecker checks client secret expiration via Microsoft Graph API.
type SecretExpiryChecker struct {
	mu         sync.RWMutex
	cfg        *config.GraphConfig
	cached     SecretExpiryInfo
	lastCheck  time.Time
	httpClient *http.Client
	refreshing bool // prevents multiple concurrent refreshes
}

// NewSecretExpiryChecker creates a new expiry checker for the given config.
func NewSecretExpiryChecker(cfg *config.GraphConfig) *SecretExpiryChecker {
	return &SecretExpiryChecker{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: httpTimeout},
	}
}

// InfoCached returns the cached secret expiry information without triggering a refresh.
// Use this for health checks to avoid blocking on external API calls.
func (c *SecretExpiryChecker) InfoCached() SecretExpiryInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cached
}

// Info returns the secret expiry information, using a cached value if fresh enough.
// If the cache is stale, it triggers an async refresh and returns the stale cached value.
// Only one refresh runs at a time to prevent thundering herd during outages.
func (c *SecretExpiryChecker) Info() SecretExpiryInfo {
	c.mu.RLock()
	cached := c.cached
	needsRefresh := time.Since(c.lastCheck) >= expiryCacheTTL || c.lastCheck.IsZero()
	c.mu.RUnlock()

	if needsRefresh {
		// refresh() has its own guard to prevent concurrent refreshes
		go c.refresh()
	}

	return cached
}

// refresh fetches fresh expiry information from the Graph API and updates the cache.
func (c *SecretExpiryChecker) refresh() {
	// Set refreshing flag and ensure it's cleared when done
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		return // Another refresh is already in progress
	}
	c.refreshing = true
	cfg := c.cfg
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()

	if cfg == nil || cfg.TenantID == "" || cfg.ClientID == "" || cfg.ClientSecret == "" {
		c.mu.Lock()
		c.cached = SecretExpiryInfo{Error: "Graph API not configured"}
		c.lastCheck = time.Now()
		c.mu.Unlock()
		return
	}

	info, err := c.fetchExpiryInfo(cfg)
	if err != nil {
		info = SecretExpiryInfo{Error: err.Error()}
	}

	c.mu.Lock()
	c.cached = info
	c.lastCheck = time.Now()
	c.mu.Unlock()
}

// applicationResponse represents a Microsoft Graph application response.
type applicationResponse struct {
	PasswordCredentials []passwordCredential `json:"passwordCredentials"`
}

type passwordCredential struct {
	EndDateTime string `json:"endDateTime"`
}

// fetchExpiryInfo queries the Graph API for credential expiry information.
func (c *SecretExpiryChecker) fetchExpiryInfo(cfg *config.GraphConfig) (SecretExpiryInfo, error) {
	ctx, cancel := context.WithTimeoutCause(
		context.Background(),
		httpTimeout,
		errors.New("graph API request timeout"),
	)
	defer cancel()

	ts, err := tokenSourceContext(ctx, cfg)
	if err != nil {
		return SecretExpiryInfo{}, fmt.Errorf("create token source: %w", err)
	}

	token, err := ts.Token()
	if err != nil {
		return SecretExpiryInfo{}, fmt.Errorf("acquire token: %w", err)
	}

	apiURL := fmt.Sprintf("%s/applications(appId='%s')", graphBaseURL, url.PathEscape(cfg.ClientID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, http.NoBody)
	if err != nil {
		return SecretExpiryInfo{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return SecretExpiryInfo{}, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return SecretExpiryInfo{}, fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var appResp applicationResponse
	if err := json.Unmarshal(body, &appResp); err != nil {
		return SecretExpiryInfo{}, fmt.Errorf("parse response: %w", err)
	}

	// Find the earliest non-expired credential
	now := time.Now()
	var earliest time.Time
	for _, cred := range appResp.PasswordCredentials {
		if cred.EndDateTime == "" {
			continue
		}
		expiry, err := time.Parse(time.RFC3339, cred.EndDateTime)
		if err != nil {
			continue
		}
		if expiry.Before(now) {
			continue // Skip already-expired credentials
		}
		if earliest.IsZero() || expiry.Before(earliest) {
			earliest = expiry
		}
	}

	if earliest.IsZero() {
		return SecretExpiryInfo{Error: "no valid (non-expired) credentials found"}, nil
	}

	daysLeft := int(time.Until(earliest).Hours() / 24)
	if daysLeft < 0 {
		daysLeft = 0
	}

	return SecretExpiryInfo{
		ExpiresAt:   earliest.Format(time.RFC3339),
		ExpiresSoon: daysLeft <= expiryWarningDays,
		DaysLeft:    daysLeft,
	}, nil
}
