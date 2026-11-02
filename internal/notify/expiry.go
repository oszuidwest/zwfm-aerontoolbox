package notify

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	// expiryWarningDays is the warning threshold for client secret expiry.
	expiryWarningDays = 30
	// expiryCacheTTL is the maximum age of cached expiry data.
	expiryCacheTTL = 1 * time.Hour
	// expiryTimeout bounds one Graph lookup.
	expiryTimeout = 30 * time.Second
)

// SecretExpiryInfo is the health payload for Graph client secret expiry.
type SecretExpiryInfo struct {
	ExpiresAt   string `json:"expires_at,omitempty"`
	ExpiresSoon bool   `json:"expires_soon"`
	DaysLeft    int    `json:"days_left"`
	Error       string `json:"error,omitempty"`
}

// SecretExpiryChecker caches Microsoft Graph client secret expiry.
type SecretExpiryChecker struct {
	mu         sync.RWMutex
	lookup     func(context.Context) (time.Time, error)
	cached     SecretExpiryInfo
	lastCheck  time.Time
	refreshing bool // prevents multiple concurrent refreshes
}

// NewSecretExpiryChecker returns an expiry checker that asks lookup for the
// secret's expiry time, such as graphmail.Client.SecretExpiry.
func NewSecretExpiryChecker(lookup func(context.Context) (time.Time, error)) *SecretExpiryChecker {
	return &SecretExpiryChecker{lookup: lookup}
}

// Info returns cached expiry information and refreshes stale data asynchronously.
// Only one refresh runs at a time to prevent thundering herd during outages.
func (c *SecretExpiryChecker) Info() SecretExpiryInfo {
	c.mu.RLock()
	cached := c.cached
	needsRefresh := time.Since(c.lastCheck) >= expiryCacheTTL || c.lastCheck.IsZero()
	c.mu.RUnlock()

	if needsRefresh {
		go c.refresh()
	}

	return cached
}

// refresh fetches expiry information from Graph and updates the cache.
func (c *SecretExpiryChecker) refresh() {
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		return
	}
	c.refreshing = true
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.refreshing = false
		c.mu.Unlock()
	}()

	info, err := c.fetchExpiryInfo()
	if err != nil {
		info = SecretExpiryInfo{Error: err.Error()}
	}

	c.mu.Lock()
	c.cached = info
	c.lastCheck = time.Now()
	c.mu.Unlock()
}

// fetchExpiryInfo asks Graph when the configured client secret expires.
func (c *SecretExpiryChecker) fetchExpiryInfo() (SecretExpiryInfo, error) {
	ctx, cancel := context.WithTimeoutCause(
		context.Background(),
		expiryTimeout,
		errors.New("graph API request timeout"),
	)
	defer cancel()

	earliest, err := c.lookup(ctx)
	if err != nil {
		return SecretExpiryInfo{}, err
	}

	daysLeft := max(int(time.Until(earliest).Hours()/24), 0)

	return SecretExpiryInfo{
		ExpiresAt:   earliest.Format(time.RFC3339),
		ExpiresSoon: daysLeft <= expiryWarningDays,
		DaysLeft:    daysLeft,
	}, nil
}
