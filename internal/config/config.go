// Package config provides application configuration management.
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// DatabaseConfig contains PostgreSQL database connection parameters.
type DatabaseConfig struct {
	Host                   string `json:"host" validate:"required"`
	Port                   string `json:"port" validate:"required"`
	Name                   string `json:"name" validate:"required"`
	User                   string `json:"user" validate:"required"`
	Password               string `json:"password" validate:"required"` //nolint:gosec // G117: intentional config field for database auth
	Schema                 string `json:"schema" validate:"required,identifier"`
	SSLMode                string `json:"sslmode" validate:"required"`
	MaxOpenConns           int    `json:"max_open_conns" validate:"gte=0"`
	MaxIdleConns           int    `json:"max_idle_conns" validate:"gte=0"`
	ConnMaxLifetimeMinutes int    `json:"conn_max_lifetime_minutes" validate:"gte=0"`
}

// ImageConfig contains image processing and optimization settings.
type ImageConfig struct {
	TargetWidth               int   `json:"target_width" validate:"required,gt=0"`
	TargetHeight              int   `json:"target_height" validate:"required,gt=0"`
	Quality                   int   `json:"quality" validate:"required,min=1,max=100"`
	RejectSmaller             bool  `json:"reject_smaller"`
	MaxImageDownloadSizeBytes int64 `json:"max_image_download_size_bytes" validate:"gte=0"`
}

// APIConfig contains API authentication and server settings.
type APIConfig struct {
	Enabled               bool     `json:"enabled"`
	Keys                  []string `json:"keys" validate:"required_if=Enabled true,dive,required"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds" validate:"gte=0"`
}

// MaintenanceConfig contains thresholds and settings for database maintenance operations.
type MaintenanceConfig struct {
	BloatThreshold           float64         `json:"bloat_threshold" validate:"gte=0,lte=100"`
	DeadTupleThreshold       int64           `json:"dead_tuple_threshold" validate:"gte=0"`
	VacuumStalenessDays      int             `json:"vacuum_staleness_days" validate:"gte=0"`
	MinRowsForRecommendation int64           `json:"min_rows_for_recommendation" validate:"gte=0"`
	ToastSizeWarningBytes    int64           `json:"toast_size_warning_bytes" validate:"gte=0"`
	StaleStatsThresholdPct   int             `json:"stale_stats_threshold_pct" validate:"gte=0,lte=100"`
	SeqScanRatioThreshold    float64         `json:"seq_scan_ratio_threshold" validate:"gte=0"`
	TimeoutMinutes           int             `json:"timeout_minutes" validate:"gte=0"`
	Scheduler                SchedulerConfig `json:"scheduler"`
}

// SchedulerConfig contains settings for individual scheduled operations.
type SchedulerConfig struct {
	Enabled  bool   `json:"enabled"`
	Schedule string `json:"schedule" validate:"required_if=Enabled true"`
}

// S3Config contains settings for S3-compatible storage synchronization.
type S3Config struct {
	Enabled         bool   `json:"enabled"`
	Bucket          string `json:"bucket" validate:"required_if=Enabled true"`
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	AccessKeyID     string `json:"access_key_id" validate:"required_if=Enabled true"`
	SecretAccessKey string `json:"secret_access_key" validate:"required_if=Enabled true"`
	PathPrefix      string `json:"path_prefix"`
	ForcePathStyle  bool   `json:"force_path_style"`
}

// BackupConfig contains settings for database backup functionality.
type BackupConfig struct {
	Enabled            bool            `json:"enabled"`
	Path               string          `json:"path" validate:"required_if=Enabled true"`
	RetentionDays      int             `json:"retention_days" validate:"gte=0"`
	MaxBackups         int             `json:"max_backups" validate:"gte=0"`
	DefaultCompression int             `json:"default_compression" validate:"gte=0,lte=9"`
	TimeoutMinutes     int             `json:"timeout_minutes" validate:"gte=0"`
	PgDumpPath         string          `json:"pg_dump_path"`
	PgRestorePath      string          `json:"pg_restore_path"`
	Scheduler          SchedulerConfig `json:"scheduler"`
	S3                 S3Config        `json:"s3"`
}

// LogConfig contains logging configuration.
type LogConfig struct {
	Level  string `json:"level" validate:"omitempty,oneof=debug info warn error"`
	Format string `json:"format" validate:"omitempty,oneof=text json"`
}

// Config represents the complete application configuration.
type Config struct {
	Database    DatabaseConfig    `json:"database"`
	Image       ImageConfig       `json:"image"`
	API         APIConfig         `json:"api"`
	Maintenance MaintenanceConfig `json:"maintenance"`
	Backup      BackupConfig      `json:"backup"`
	Log         LogConfig         `json:"log"`
}

const (
	DefaultMaxOpenConnections        = 25
	DefaultMaxIdleConnections        = 5
	DefaultConnMaxLifetimeMinutes    = 5
	DefaultMaxImageDownloadSizeBytes = 50 * 1024 * 1024
	DefaultRequestTimeoutSeconds     = 30
	DefaultBloatThreshold            = 10.0
	DefaultDeadTupleThreshold        = 10000
	DefaultVacuumStalenessDays       = 7
	DefaultMinRowsForRecommendation  = 1000
	DefaultToastSizeWarningBytes     = 500 * 1024 * 1024
	DefaultStaleStatsThresholdPct    = 10
	DefaultSeqScanRatioThreshold     = 10.0
	DefaultMaintenanceTimeoutMinutes = 30
	DefaultBackupRetentionDays       = 30
	DefaultBackupMaxBackups          = 10
	DefaultBackupCompression         = 9
	DefaultBackupPath                = "./backups"
	DefaultBackupTimeoutMinutes      = 30
)

// GetMaxDownloadBytes returns the maximum allowed image download size in bytes.
func (c *ImageConfig) GetMaxDownloadBytes() int64 {
	return cmp.Or(c.MaxImageDownloadSizeBytes, DefaultMaxImageDownloadSizeBytes)
}

// GetRequestTimeout returns the HTTP request timeout as a Duration.
func (c *APIConfig) GetRequestTimeout() time.Duration {
	return time.Duration(cmp.Or(c.RequestTimeoutSeconds, DefaultRequestTimeoutSeconds)) * time.Second
}

// GetMaxOpenConns returns the maximum number of open database connections.
func (c *DatabaseConfig) GetMaxOpenConns() int {
	return cmp.Or(c.MaxOpenConns, DefaultMaxOpenConnections)
}

// GetMaxIdleConns returns the maximum number of idle database connections.
func (c *DatabaseConfig) GetMaxIdleConns() int {
	return cmp.Or(c.MaxIdleConns, DefaultMaxIdleConnections)
}

// GetConnMaxLifetime returns the maximum lifetime of database connections as a Duration.
func (c *DatabaseConfig) GetConnMaxLifetime() time.Duration {
	return time.Duration(cmp.Or(c.ConnMaxLifetimeMinutes, DefaultConnMaxLifetimeMinutes)) * time.Minute
}

// GetBloatThreshold returns the table bloat percentage that triggers maintenance recommendations.
func (c *MaintenanceConfig) GetBloatThreshold() float64 {
	return cmp.Or(c.BloatThreshold, DefaultBloatThreshold)
}

// GetDeadTupleThreshold returns the dead tuple count that triggers vacuum recommendations.
func (c *MaintenanceConfig) GetDeadTupleThreshold() int64 {
	return cmp.Or(c.DeadTupleThreshold, DefaultDeadTupleThreshold)
}

// GetVacuumStalenessDays returns the number of days after which a table is considered stale.
func (c *MaintenanceConfig) GetVacuumStalenessDays() int {
	return cmp.Or(c.VacuumStalenessDays, DefaultVacuumStalenessDays)
}

// GetVacuumStaleness returns the staleness threshold as a Duration.
func (c *MaintenanceConfig) GetVacuumStaleness() time.Duration {
	return time.Duration(c.GetVacuumStalenessDays()) * 24 * time.Hour
}

// GetMinRowsForRecommendation returns the minimum row count for maintenance recommendations.
func (c *MaintenanceConfig) GetMinRowsForRecommendation() int64 {
	return cmp.Or(c.MinRowsForRecommendation, DefaultMinRowsForRecommendation)
}

// GetToastSizeWarningBytes returns the TOAST size threshold for warnings.
func (c *MaintenanceConfig) GetToastSizeWarningBytes() int64 {
	return cmp.Or(c.ToastSizeWarningBytes, DefaultToastSizeWarningBytes)
}

// GetStaleStatsThreshold returns the percentage of modified rows that triggers ANALYZE.
func (c *MaintenanceConfig) GetStaleStatsThreshold() int {
	return cmp.Or(c.StaleStatsThresholdPct, DefaultStaleStatsThresholdPct)
}

// GetSeqScanRatioThreshold returns the seq_scan/idx_scan ratio for missing index warnings.
func (c *MaintenanceConfig) GetSeqScanRatioThreshold() float64 {
	return cmp.Or(c.SeqScanRatioThreshold, DefaultSeqScanRatioThreshold)
}

// GetTimeout returns the maximum duration for maintenance operations.
func (c *MaintenanceConfig) GetTimeout() time.Duration {
	return time.Duration(cmp.Or(c.TimeoutMinutes, DefaultMaintenanceTimeoutMinutes)) * time.Minute
}

// GetPath returns the directory path where backup files are stored.
func (c *BackupConfig) GetPath() string {
	return cmp.Or(c.Path, DefaultBackupPath)
}

// GetRetentionDays returns the number of days to keep backup files before automatic deletion.
func (c *BackupConfig) GetRetentionDays() int {
	return cmp.Or(c.RetentionDays, DefaultBackupRetentionDays)
}

// GetMaxBackups returns the maximum number of backup files to retain.
func (c *BackupConfig) GetMaxBackups() int {
	return cmp.Or(c.MaxBackups, DefaultBackupMaxBackups)
}

// GetDefaultCompression returns the compression level (0-9) for backups.
func (c *BackupConfig) GetDefaultCompression() int {
	return min(cmp.Or(c.DefaultCompression, DefaultBackupCompression), 9)
}

// GetTimeout returns the maximum duration for backup operations.
func (c *BackupConfig) GetTimeout() time.Duration {
	return time.Duration(cmp.Or(c.TimeoutMinutes, DefaultBackupTimeoutMinutes)) * time.Minute
}

// GetPathPrefix returns the S3 path prefix for constructing object keys.
func (c *S3Config) GetPathPrefix() string {
	prefix := c.PathPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}

// GetLevel returns the configured log level as an slog.Level.
func (c *LogConfig) GetLevel() slog.Level {
	switch strings.ToLower(c.Level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// GetFormat returns the configured log format ("text" or "json").
func (c *LogConfig) GetFormat() string {
	if strings.EqualFold(c.Format, "json") {
		return "json"
	}
	return "text"
}

// Load loads and validates application configuration from a JSON file.
func Load(configPath string) (*Config, error) {
	config := &Config{}

	if configPath == "" {
		if _, err := os.Stat("config.json"); err == nil {
			configPath = "config.json"
		} else {
			return nil, fmt.Errorf("config file config.json not found")
		}
	}

	data, err := os.ReadFile(configPath) //nolint:gosec // config path is validated via CLI flag or default
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("config file error: %w", err)
	}

	if envLevel := os.Getenv("LOG_LEVEL"); envLevel != "" {
		config.Log.Level = envLevel
	}

	if err := validate(config); err != nil {
		return nil, fmt.Errorf("configuration invalid: %w", err)
	}

	return config, nil
}

// configValidator is the singleton validator instance with custom validations.
var configValidator = newConfigValidator()

func newConfigValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())

	_ = v.RegisterValidation("identifier", func(fl validator.FieldLevel) bool {
		return types.IsValidIdentifier(fl.Field().String())
	})

	v.RegisterStructValidation(validateS3Config, S3Config{})

	return v
}

// validateS3Config checks that S3 has either a region or custom endpoint when enabled.
func validateS3Config(sl validator.StructLevel) {
	s3 := sl.Current().Interface().(S3Config)
	if !s3.Enabled {
		return
	}
	if s3.Region == "" && s3.Endpoint == "" {
		sl.ReportError(s3.Region, "region", "Region", "required_without_endpoint", "")
	}
}

// validate validates the configuration using struct tags and struct-level validators.
func validate(config *Config) error {
	if err := configValidator.Struct(config); err != nil {
		return formatErrors(err)
	}
	return nil
}

// formatErrors converts validator errors to user-friendly messages.
func formatErrors(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return err
	}

	var msgs []string
	for _, e := range ve {
		field := strings.ToLower(e.Namespace()[7:]) // Strip "Config." prefix
		msgs = append(msgs, fmt.Sprintf("%s %s", field, tagMessage(e.Tag(), e.Param())))
	}

	return fmt.Errorf("%s", strings.Join(msgs, "; "))
}

// tagMessage returns an English message for a validation tag.
func tagMessage(tag, param string) string {
	switch tag {
	case "required":
		return "is required"
	case "required_if":
		return "is required when enabled"
	case "required_without_endpoint":
		return "is required when no endpoint is specified"
	case "gt":
		return fmt.Sprintf("must be greater than %s", param)
	case "gte":
		return fmt.Sprintf("must be %s or greater", param)
	case "min":
		return fmt.Sprintf("must be at least %s", param)
	case "max", "lte":
		return fmt.Sprintf("must be at most %s", param)
	case "oneof":
		return fmt.Sprintf("must be one of [%s]", param)
	case "identifier":
		return "contains invalid characters (only letters, numbers and underscores allowed)"
	default:
		return fmt.Sprintf("is invalid (%s)", tag)
	}
}

// ConnectionString returns a PostgreSQL connection string.
func (c *DatabaseConfig) ConnectionString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}
