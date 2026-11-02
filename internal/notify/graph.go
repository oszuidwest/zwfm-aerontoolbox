// Package notify sends operational emails through Microsoft Graph.
package notify

import (
	"fmt"
	"strings"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// ValidateConfig validates required Graph email fields and GUID shapes.
func ValidateConfig(cfg *config.GraphConfig) error {
	if cfg.TenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	if !util.GUIDPattern.MatchString(cfg.TenantID) {
		return fmt.Errorf("tenant ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientID == "" {
		return fmt.Errorf("client ID is required")
	}
	if !util.GUIDPattern.MatchString(cfg.ClientID) {
		return fmt.Errorf("client ID must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)")
	}
	if cfg.ClientSecret == "" {
		return fmt.Errorf("client secret is required")
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
