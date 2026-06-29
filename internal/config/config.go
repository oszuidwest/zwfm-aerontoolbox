// Package config loads, defaults, and validates JSON configuration for the API server.
package config

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// DatabaseConfig holds PostgreSQL connection settings.
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

// ImageConfig controls image validation and optimization.
type ImageConfig struct {
	TargetWidth               int   `json:"target_width" validate:"required,gt=0"`
	TargetHeight              int   `json:"target_height" validate:"required,gt=0"`
	Quality                   int   `json:"quality" validate:"required,min=1,max=100"`
	RejectSmaller             bool  `json:"reject_smaller"`
	MaxImageDownloadSizeBytes int64 `json:"max_image_download_size_bytes" validate:"gte=0"`
}

// APIConfig controls API authentication and request timeouts.
type APIConfig struct {
	Enabled               bool     `json:"enabled"`
	Keys                  []string `json:"keys" validate:"required_if=Enabled true,dive,required"`
	RequestTimeoutSeconds int      `json:"request_timeout_seconds" validate:"gte=0"`
}

// MaintenanceConfig holds thresholds used by database health checks.
type MaintenanceConfig struct {
	BloatThreshold              float64         `json:"bloat_threshold" validate:"gte=0,lte=100"`
	DeadTupleThreshold          int64           `json:"dead_tuple_threshold" validate:"gte=0"`
	VacuumStalenessDays         int             `json:"vacuum_staleness_days" validate:"gte=0"`
	MinRowsForRecommendation    int64           `json:"min_rows_for_recommendation" validate:"gte=0"`
	ToastSizeWarningBytes       int64           `json:"toast_size_warning_bytes" validate:"gte=0"`
	StaleStatsThresholdPct      int             `json:"stale_stats_threshold_pct" validate:"gte=0,lte=100"`
	SeqScanRatioThreshold       float64         `json:"seq_scan_ratio_threshold" validate:"gte=0"`
	ConnectionUsageThresholdPct int             `json:"connection_usage_threshold_pct" validate:"gte=0,lte=100"`
	LongQueryThresholdSeconds   int             `json:"long_query_threshold_seconds" validate:"gte=0"`
	Scheduler                   SchedulerConfig `json:"scheduler"`
}

// SchedulerConfig controls a single cron-backed operation.
type SchedulerConfig struct {
	Enabled  bool   `json:"enabled"`
	Schedule string `json:"schedule" validate:"required_if=Enabled true"`
}

// S3Config configures optional S3-compatible backup synchronization.
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

// BackupConfig configures local and optional remote database backups.
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

// NotificationsConfig groups all notification providers.
type NotificationsConfig struct {
	Email GraphConfig `json:"email"`
}

// MetricsConfig controls the optional Prometheus-compatible metrics endpoint.
type MetricsConfig struct {
	Enabled bool   `json:"enabled"`
	Path    string `json:"path"`
}

// GraphConfig configures Microsoft Graph email delivery.
type GraphConfig struct {
	TenantID     string `json:"tenant_id" validate:"omitempty,guid"`
	ClientID     string `json:"client_id" validate:"omitempty,guid"`
	ClientSecret string `json:"client_secret"` //nolint:gosec // G101: user-supplied config secret, not a hardcoded credential
	FromAddress  string `json:"from_address"`
	Recipients   string `json:"recipients"`
}

// FileMonitorConfig configures freshness checks for files on disk.
type FileMonitorConfig struct {
	Enabled         bool                     `json:"enabled"`
	IntervalSeconds int                      `json:"interval_seconds" validate:"gte=0"`
	Checks          []FileMonitorCheckConfig `json:"checks" validate:"required_if=Enabled true,dive"`
}

// FileMonitorCheckConfig defines a single file to monitor for staleness.
//
// ActiveWindow is validated by validateFileMonitorConfig (struct-level) so the
// parser's specific reason - equal start/end, range, format - survives into
// the error message instead of collapsing to a generic tag string.
type FileMonitorCheckConfig struct {
	Name           string `json:"name"`
	Path           string `json:"path" validate:"required,absolute_path"`
	MaxAgeMinutes  int    `json:"max_age_minutes" validate:"required,gte=1"`
	StatTimeoutSec int    `json:"stat_timeout_seconds" validate:"gte=0"`
	ActiveWindow   string `json:"active_window"`
}

// DisplayName returns the configured label, falling back to the file path.
func (c *FileMonitorCheckConfig) DisplayName() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Path
}

// DefaultFileMonitorIntervalSeconds is the default polling cadence when interval_seconds is unset.
const DefaultFileMonitorIntervalSeconds = 60

// DefaultFileMonitorStatTimeoutSeconds is the default stat-timeout per check when stat_timeout_seconds is unset.
const DefaultFileMonitorStatTimeoutSeconds = 5

// StatTimeout returns the per-check stat timeout. Falls back to
// DefaultFileMonitorStatTimeoutSeconds when StatTimeoutSec is zero or negative.
func (c *FileMonitorCheckConfig) StatTimeout() time.Duration {
	if c.StatTimeoutSec <= 0 {
		return DefaultFileMonitorStatTimeoutSeconds * time.Second
	}
	return time.Duration(c.StatTimeoutSec) * time.Second
}

// Interval returns the polling cadence for the file monitor scheduler.
// When IntervalSeconds is unset (0) or negative, it falls back to DefaultFileMonitorIntervalSeconds.
func (c *FileMonitorConfig) Interval() time.Duration {
	if c.IntervalSeconds <= 0 {
		return DefaultFileMonitorIntervalSeconds * time.Second
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}

// MediaFileCheckConfig configures playlist audio verification against disk files.
//
// The Aeron database stores Windows paths (e.g. O:\Audio\85\Artist - Title.wav)
// while this service typically runs on Linux. Two complementary strategies map a
// database reference to a real file:
//
//   - DriveMounts translates a Windows drive prefix to a host directory
//     (e.g. "O:" -> "/mnt/aeron-o"), preserving the directory structure so an
//     exact path can be stat'd. This is the fast, unambiguous primary strategy.
//   - SearchDirs are directories indexed recursively; a reference is then matched
//     by filename (with and, as a fallback, without extension). This is the
//     fallback for files whose exact path moved or that are mounted flat.
//
// At least one of DriveMounts or SearchDirs must be configured when Enabled.
type MediaFileCheckConfig struct {
	Enabled            bool              `json:"enabled"`
	SearchDirs         []string          `json:"search_dirs"`
	DriveMounts        map[string]string `json:"drive_mounts"`
	StatTimeoutSec     int               `json:"stat_timeout_seconds" validate:"gte=0"`
	MaxRangeDays       int               `json:"max_range_days" validate:"gte=0"`
	CaseInsensitive    *bool             `json:"case_insensitive"`
	IncludeVoicetracks bool              `json:"include_voicetracks"`
	// LookaheadDays extends scheduled runs from today through today+N
	// inclusive. A zero value checks only today; manual API runs ignore this.
	LookaheadDays int             `json:"lookahead_days" validate:"gte=0"`
	Scheduler     SchedulerConfig `json:"scheduler"`
}

// DefaultMediaFileCheckStatTimeoutSeconds is the default per-stat timeout for media file checks.
const DefaultMediaFileCheckStatTimeoutSeconds = 5

// DefaultMediaFileCheckMaxRangeDays caps the from/to span of a single media file check.
const DefaultMediaFileCheckMaxRangeDays = 31

// StatTimeout returns the per-stat timeout. Falls back to
// DefaultMediaFileCheckStatTimeoutSeconds when StatTimeoutSec is zero or negative.
func (c *MediaFileCheckConfig) StatTimeout() time.Duration {
	if c.StatTimeoutSec <= 0 {
		return DefaultMediaFileCheckStatTimeoutSeconds * time.Second
	}
	return time.Duration(c.StatTimeoutSec) * time.Second
}

// GetMaxRangeDays returns the configured from/to span cap or its default.
func (c *MediaFileCheckConfig) GetMaxRangeDays() int {
	return cmp.Or(c.MaxRangeDays, DefaultMediaFileCheckMaxRangeDays)
}

// IsCaseInsensitive reports whether filename matching ignores case. Defaults to
// true (the references originate on case-insensitive Windows) when unset.
func (c *MediaFileCheckConfig) IsCaseInsensitive() bool {
	if c.CaseInsensitive == nil {
		return true
	}
	return *c.CaseInsensitive
}

// LogConfig configures global structured logging.
type LogConfig struct {
	Level  string `json:"level" validate:"omitempty,oneof=debug info warn error"`
	Format string `json:"format" validate:"omitempty,oneof=text json"`
}

// Config is the fully loaded application configuration.
type Config struct {
	Database       DatabaseConfig       `json:"database"`
	Image          ImageConfig          `json:"image"`
	API            APIConfig            `json:"api"`
	Maintenance    MaintenanceConfig    `json:"maintenance"`
	Backup         BackupConfig         `json:"backup"`
	FileMonitor    FileMonitorConfig    `json:"file_monitor"`
	MediaFileCheck MediaFileCheckConfig `json:"media_file_check"`
	Notifications  NotificationsConfig  `json:"notifications"`
	Metrics        MetricsConfig        `json:"metrics"`
	Log            LogConfig            `json:"log"`
}

const (
	DefaultMaxOpenConnections          = 25
	DefaultMaxIdleConnections          = 5
	DefaultConnMaxLifetimeMinutes      = 5
	DefaultMaxImageDownloadSizeBytes   = 50 * 1024 * 1024
	DefaultRequestTimeoutSeconds       = 30
	DefaultBloatThreshold              = 10.0
	DefaultDeadTupleThreshold          = 10000
	DefaultVacuumStalenessDays         = 7
	DefaultMinRowsForRecommendation    = 1000
	DefaultToastSizeWarningBytes       = 500 * 1024 * 1024
	DefaultStaleStatsThresholdPct      = 10
	DefaultSeqScanRatioThreshold       = 10.0
	DefaultConnectionUsageThresholdPct = 80
	DefaultLongQueryThresholdSeconds   = 10
	DefaultBackupRetentionDays         = 30
	DefaultBackupMaxBackups            = 10
	DefaultBackupCompression           = 9
	DefaultBackupPath                  = "./backups"
	DefaultBackupTimeoutMinutes        = 30
	DefaultMetricsPath                 = "/metrics"
)

// GetMaxDownloadBytes returns the configured image download cap or its default.
func (c *ImageConfig) GetMaxDownloadBytes() int64 {
	return cmp.Or(c.MaxImageDownloadSizeBytes, DefaultMaxImageDownloadSizeBytes)
}

// GetRequestTimeout returns the configured HTTP timeout or its default.
func (c *APIConfig) GetRequestTimeout() time.Duration {
	return time.Duration(cmp.Or(c.RequestTimeoutSeconds, DefaultRequestTimeoutSeconds)) * time.Second
}

// GetMaxOpenConns returns the configured open-connection cap or its default.
func (c *DatabaseConfig) GetMaxOpenConns() int {
	return cmp.Or(c.MaxOpenConns, DefaultMaxOpenConnections)
}

// GetMaxIdleConns returns the configured idle-connection cap or its default.
func (c *DatabaseConfig) GetMaxIdleConns() int {
	return cmp.Or(c.MaxIdleConns, DefaultMaxIdleConnections)
}

// GetConnMaxLifetime returns the configured connection lifetime or its default.
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

// GetConnectionUsageThreshold returns the connection usage percentage that triggers alerts.
func (c *MaintenanceConfig) GetConnectionUsageThreshold() int {
	return cmp.Or(c.ConnectionUsageThresholdPct, DefaultConnectionUsageThresholdPct)
}

// GetLongQueryThresholdSeconds returns the duration in seconds after which a query is considered long-running.
func (c *MaintenanceConfig) GetLongQueryThresholdSeconds() int {
	return cmp.Or(c.LongQueryThresholdSeconds, DefaultLongQueryThresholdSeconds)
}

// GetPath returns the backup directory or its default.
func (c *BackupConfig) GetPath() string {
	return cmp.Or(c.Path, DefaultBackupPath)
}

// GetRetentionDays returns the backup age limit or its default.
func (c *BackupConfig) GetRetentionDays() int {
	return cmp.Or(c.RetentionDays, DefaultBackupRetentionDays)
}

// GetMaxBackups returns the backup count limit or its default.
func (c *BackupConfig) GetMaxBackups() int {
	return cmp.Or(c.MaxBackups, DefaultBackupMaxBackups)
}

// GetDefaultCompression returns the backup compression level, capped at 9.
func (c *BackupConfig) GetDefaultCompression() int {
	return min(cmp.Or(c.DefaultCompression, DefaultBackupCompression), 9)
}

// GetTimeout returns the backup timeout or its default.
func (c *BackupConfig) GetTimeout() time.Duration {
	return time.Duration(cmp.Or(c.TimeoutMinutes, DefaultBackupTimeoutMinutes)) * time.Minute
}

// GetPathPrefix returns the S3 object prefix with a trailing slash when set.
func (c *S3Config) GetPathPrefix() string {
	prefix := c.PathPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix
}

// GetPath returns the scrape path for the metrics endpoint.
func (c *MetricsConfig) GetPath() string {
	if c.Path == "" {
		return DefaultMetricsPath
	}
	if strings.HasPrefix(c.Path, "/") {
		return c.Path
	}
	return "/" + c.Path
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

// Load reads JSON configuration, applies environment overrides, and validates it.
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

	_ = v.RegisterValidation("guid", func(fl validator.FieldLevel) bool {
		return util.GUIDPattern.MatchString(fl.Field().String())
	})

	_ = v.RegisterValidation("absolute_path", func(fl validator.FieldLevel) bool {
		return filepath.IsAbs(fl.Field().String())
	})

	v.RegisterStructValidation(validateS3Config, S3Config{})
	v.RegisterStructValidation(validateFileMonitorConfig, FileMonitorConfig{})
	v.RegisterStructValidation(validateMediaFileCheckConfig, MediaFileCheckConfig{})

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

// validateFileMonitorConfig checks that at least one check is configured when
// file monitor is enabled, that no two checks share the same path, and that
// each ActiveWindow parses cleanly. The window check runs even when the
// monitor is disabled so format/range typos surface during config load
// rather than only after the operator flips the feature on. The parser
// error string is forwarded verbatim via the tag param so the user sees the
// concrete reason (e.g. "start and end are equal") instead of a generic
// "must be HH:MM-HH:MM".
func validateFileMonitorConfig(sl validator.StructLevel) {
	fm := sl.Current().Interface().(FileMonitorConfig)

	// Encode the slice index in the reported field name (e.g.
	// "checks[1].active_window") so multiple bad windows produce distinct,
	// locatable error labels. ReportError otherwise registers at the parent
	// struct level - every bad check would collapse onto a single
	// "filemonitor.active_window" key, defeating the point of telling the
	// operator which entry to fix.
	for i, c := range fm.Checks {
		if c.ActiveWindow == "" {
			continue
		}
		if _, err := ParseTimeWindow(c.ActiveWindow); err != nil {
			fieldName := fmt.Sprintf("checks[%d].active_window", i)
			structFieldName := fmt.Sprintf("Checks[%d].ActiveWindow", i)
			sl.ReportError(fm.Checks[i].ActiveWindow, fieldName, structFieldName, "invalid_time_window", err.Error())
		}
	}

	if !fm.Enabled {
		return
	}
	if len(fm.Checks) == 0 {
		sl.ReportError(fm.Checks, "checks", "Checks", "required_when_enabled", "")
		return
	}
	seen := make(map[string]bool, len(fm.Checks))
	for i, c := range fm.Checks {
		if seen[c.Path] {
			sl.ReportError(fm.Checks[i].Path, "path", "Path", "duplicate_path", c.Path)
		}
		seen[c.Path] = true
	}
}

// driveLetterPattern matches a Windows drive prefix such as "O:" (optionally
// written "O:\"). Capture group 1 is the letter.
var driveLetterPattern = regexp.MustCompile(`^([A-Za-z]):\\?$`)

// validateMediaFileCheckConfig checks that, when the media file check is
// enabled, at least one resolution source is configured and that every path is
// absolute. Drive-mount keys must be Windows drive prefixes ("O:") and their
// targets absolute host directories. The checks only run when enabled so a
// disabled, half-filled block does not block startup.
func validateMediaFileCheckConfig(sl validator.StructLevel) {
	mfc := sl.Current().Interface().(MediaFileCheckConfig)
	if !mfc.Enabled {
		return
	}

	if len(mfc.SearchDirs) == 0 && len(mfc.DriveMounts) == 0 {
		sl.ReportError(mfc.SearchDirs, "search_dirs", "SearchDirs", "media_check_no_source", "")
	}

	for i, dir := range mfc.SearchDirs {
		if !filepath.IsAbs(dir) {
			fieldName := fmt.Sprintf("search_dirs[%d]", i)
			structFieldName := fmt.Sprintf("SearchDirs[%d]", i)
			sl.ReportError(mfc.SearchDirs[i], fieldName, structFieldName, "absolute_path", "")
		}
	}

	for drive, target := range mfc.DriveMounts {
		if !driveLetterPattern.MatchString(drive) {
			sl.ReportError(target, "drive_mounts", "DriveMounts", "media_check_drive_key", drive)
			continue
		}
		if !filepath.IsAbs(target) {
			sl.ReportError(target, "drive_mounts", "DriveMounts", "media_check_drive_target", drive)
		}
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
		field := strings.ToLower(e.Namespace()[7:]) // strip "Config." prefix
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
	case "required_when_enabled":
		return "must have at least one entry when enabled"
	case "media_check_no_source":
		return "requires at least one search dir or drive mount when enabled"
	case "media_check_drive_key":
		return fmt.Sprintf("has an invalid drive key %q (expected e.g. \"O:\")", param)
	case "media_check_drive_target":
		return fmt.Sprintf("target for drive %q must be an absolute path", param)
	case "duplicate_path":
		return fmt.Sprintf("is duplicated (%s)", param)
	case "absolute_path":
		return "must be an absolute path"
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
	case "guid":
		return "must be a valid GUID (e.g., 12345678-1234-1234-1234-123456789abc)"
	case "invalid_time_window":
		return param
	default:
		return fmt.Sprintf("is invalid (%s)", tag)
	}
}

// ConnectionString returns a PostgreSQL connection string.
func (c *DatabaseConfig) ConnectionString() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.Name, c.SSLMode)
}
