package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MaintenanceService inspects PostgreSQL health and emits maintenance alerts.
type MaintenanceService struct {
	repo   *database.Repository
	config *config.Config
	notify *notify.NotificationService
}

// newMaintenanceService returns a maintenance service using repo, cfg, and notifySvc.
func newMaintenanceService(
	repo *database.Repository, cfg *config.Config, notifySvc *notify.NotificationService,
) *MaintenanceService {
	return &MaintenanceService{
		repo:   repo,
		config: cfg,
		notify: notifySvc,
	}
}

// DatabaseHealth is the API health report for the configured database.
type DatabaseHealth struct {
	DatabaseName       string                   `json:"database_name"`
	DatabaseVersion    string                   `json:"database_version"`
	DatabaseSize       string                   `json:"database_size"`
	DatabaseSizeRaw    int64                    `json:"database_size_bytes"`
	SchemaName         string                   `json:"schema_name"`
	ActiveConnections  int                      `json:"active_connections"`
	MaxConnections     int                      `json:"max_connections"`
	ConnectionUsagePct float64                  `json:"connection_usage_pct"`
	Tables             []TableHealth            `json:"tables"`
	LongRunningQueries []types.LongRunningQuery `json:"long_running_queries"`
	NeedsMaintenance   bool                     `json:"needs_maintenance"`
	Recommendations    []string                 `json:"recommendations"`
	CheckedAt          time.Time                `json:"checked_at"`
}

// TableHealth is the maintenance snapshot for one table.
type TableHealth struct {
	Name            string     `json:"name"`
	RowCount        int64      `json:"row_count"`
	DeadTuples      int64      `json:"dead_tuples"`
	DeadTuplePct    float64    `json:"dead_tuple_pct"`
	ModSinceAnalyze int64      `json:"modifications_since_analyze"`
	TotalSize       string     `json:"total_size"`
	TotalSizeRaw    int64      `json:"total_size_bytes"`
	TableSize       string     `json:"table_size"`
	TableSizeRaw    int64      `json:"table_size_bytes"`
	IndexSize       string     `json:"index_size"`
	IndexSizeRaw    int64      `json:"index_size_bytes"`
	ToastSize       string     `json:"toast_size"`
	ToastSizeRaw    int64      `json:"toast_size_bytes"`
	LastVacuum      *time.Time `json:"last_vacuum"`
	LastAutovacuum  *time.Time `json:"last_autovacuum"`
	LastAnalyze     *time.Time `json:"last_analyze"`
	LastAutoanalyze *time.Time `json:"last_autoanalyze"`
	SeqScans        int64      `json:"seq_scans"`
	IdxScans        int64      `json:"idx_scans"`
	NeedsVacuum     bool       `json:"needs_vacuum"`
	NeedsAnalyze    bool       `json:"needs_analyze"`
}

// GetHealth collects database, table, connection, and query health.
func (s *MaintenanceService) GetHealth(ctx context.Context) (*DatabaseHealth, error) {
	schema := s.repo.Schema()
	health := &DatabaseHealth{
		DatabaseName: s.config.Database.Name,
		SchemaName:   schema,
		CheckedAt:    time.Now(),
	}

	version, err := s.repo.GetDatabaseVersion(ctx)
	if err != nil {
		slog.Warn("Maintenance: could not retrieve database version", "error", err)
		version = "unavailable"
	}
	health.DatabaseVersion = version

	dbSizeRaw, err := s.repo.GetDatabaseSize(ctx)
	if err != nil {
		return nil, err
	}
	health.DatabaseSize = util.FormatBytes(dbSizeRaw)
	health.DatabaseSizeRaw = dbSizeRaw

	tables, err := s.getTableHealth(ctx)
	if err != nil {
		return nil, err
	}
	health.Tables = tables

	active, maxConns, err := s.repo.GetConnectionStats(ctx)
	if err != nil {
		return nil, err
	}
	health.ActiveConnections = active
	health.MaxConnections = maxConns
	if maxConns > 0 {
		health.ConnectionUsagePct = float64(active) / float64(maxConns) * 100
	}

	queries, err := s.repo.GetLongRunningQueries(ctx, s.config.Maintenance.GetLongQueryThresholdSeconds())
	if err != nil {
		return nil, err
	}
	if !s.config.Maintenance.ExposeLongRunningQueryText {
		for i := range queries {
			queries[i].Query = ""
		}
	}
	health.LongRunningQueries = queries

	health.Recommendations = s.generateRecommendations(health)

	for i := range tables {
		if tables[i].NeedsVacuum || tables[i].NeedsAnalyze {
			health.NeedsMaintenance = true
			break
		}
	}

	return health, nil
}

// CheckHealthAndAlert runs a health check and sends an alert if issues are detected.
// When the health check itself fails (e.g. database unreachable), an error alert is sent.
func (s *MaintenanceService) CheckHealthAndAlert(ctx context.Context) {
	health, err := s.GetHealth(ctx)
	if err != nil {
		slog.Error("Scheduled health check failed", "error", err)
		s.notify.NotifyHealthCheckError(err)
		return
	}

	result := &notify.HealthAlertResult{
		Recommendations:    health.Recommendations,
		ActiveConnections:  health.ActiveConnections,
		MaxConnections:     health.MaxConnections,
		ConnectionUsagePct: health.ConnectionUsagePct,
		LongRunningQueries: health.LongRunningQueries,
		CheckedAt:          health.CheckedAt,
	}

	s.notify.NotifyHealthAlert(result)
}

// getTableHealth retrieves maintenance statistics for all user tables in schema.
func (s *MaintenanceService) getTableHealth(ctx context.Context) ([]TableHealth, error) {
	rows, err := s.repo.GetTableStats(ctx)
	if err != nil {
		return nil, err
	}

	cfg := s.config.Maintenance
	tables := make([]TableHealth, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, convertTableRow(&row, &cfg))
	}

	return tables, nil
}

// convertTableRow maps raw PostgreSQL stats and derives maintenance flags.
func convertTableRow(row *database.TableStatsRow, cfg *config.MaintenanceConfig) TableHealth {
	table := TableHealth{
		Name:            row.TableName,
		RowCount:        row.LiveTuples,
		DeadTuples:      row.DeadTuples,
		ModSinceAnalyze: row.ModSinceAnalyze,
		LastVacuum:      row.LastVacuum,
		LastAutovacuum:  row.LastAutovacuum,
		LastAnalyze:     row.LastAnalyze,
		LastAutoanalyze: row.LastAutoanalyze,
		SeqScans:        row.SeqScan,
		IdxScans:        row.IdxScan,
		TotalSizeRaw:    row.TotalSize,
		TotalSize:       util.FormatBytes(row.TotalSize),
		TableSizeRaw:    row.TableSize,
		TableSize:       util.FormatBytes(row.TableSize),
		IndexSizeRaw:    row.IndexSize,
		IndexSize:       util.FormatBytes(row.IndexSize),
		ToastSizeRaw:    row.ToastSize,
		ToastSize:       util.FormatBytes(row.ToastSize),
	}

	if total := row.LiveTuples + row.DeadTuples; total > 0 {
		table.DeadTuplePct = float64(row.DeadTuples) / float64(total) * 100
	}

	neverAnalyzed := table.LastAnalyze == nil && table.LastAutoanalyze == nil && table.RowCount > 0
	table.NeedsVacuum = exceedsBloat(&table, cfg) || exceedsDeadTuples(&table, cfg)
	table.NeedsAnalyze = neverAnalyzed || hasStaleStats(&table, cfg)

	return table
}

// exceedsBloat reports whether the dead-tuple percentage crosses the bloat threshold.
func exceedsBloat(t *TableHealth, cfg *config.MaintenanceConfig) bool {
	return t.DeadTuplePct > cfg.GetBloatThreshold()
}

// exceedsDeadTuples reports whether the absolute dead-tuple count crosses its threshold.
func exceedsDeadTuples(t *TableHealth, cfg *config.MaintenanceConfig) bool {
	return t.DeadTuples > cfg.GetDeadTupleThreshold()
}

// hasStaleStats reports whether modifications since the last ANALYZE exceed the
// configured percentage of the row count.
func hasStaleStats(t *TableHealth, cfg *config.MaintenanceConfig) bool {
	return t.RowCount > 0 && t.ModSinceAnalyze > t.RowCount*int64(cfg.GetStaleStatsThreshold())/100
}

// generateRecommendations returns operator actions for the health report.
func (s *MaintenanceService) generateRecommendations(health *DatabaseHealth) []string {
	var recs []string

	for i := range health.Tables {
		t := &health.Tables[i]
		recs = s.checkTableHealth(t, recs)
	}

	cfg := s.config.Maintenance
	if int(health.ConnectionUsagePct) >= cfg.GetConnectionUsageThreshold() {
		recs = append(recs, fmt.Sprintf("Connection usage is high: %d/%d (%.0f%%) - check for connection leaks",
			health.ActiveConnections, health.MaxConnections, health.ConnectionUsagePct))
	}

	for _, q := range health.LongRunningQueries {
		recs = append(recs, fmt.Sprintf("Long-running query detected (PID %d, running for %s)", q.PID, q.Duration))
	}

	return recs
}

func (s *MaintenanceService) checkTableHealth(t *TableHealth, recs []string) []string {
	cfg := s.config.Maintenance
	minRows := cfg.GetMinRowsForRecommendation()

	if exceedsBloat(t, &cfg) {
		recs = append(recs, fmt.Sprintf("Table '%s' has %.1f%% dead tuples - VACUUM recommended", t.Name, t.DeadTuplePct))
	}

	if exceedsDeadTuples(t, &cfg) {
		recs = append(recs, fmt.Sprintf("Table '%s' has %d dead tuples - VACUUM recommended", t.Name, t.DeadTuples))
	}

	if t.LastVacuum == nil && t.LastAutovacuum == nil && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf("Table '%s' has never been vacuumed", t.Name))
	}

	if lastVac := lastVacuumTime(t); lastVac != nil &&
		time.Since(*lastVac) > cfg.GetVacuumStaleness() && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf(
			"Table '%s' has not been vacuumed in over %d days", t.Name, cfg.GetVacuumStalenessDays()))
	}

	if t.LastAnalyze == nil && t.LastAutoanalyze == nil && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf("Table '%s' has never been analyzed - ANALYZE recommended", t.Name))
	}

	if hasStaleStats(t, &cfg) {
		recs = append(recs, fmt.Sprintf(
			"Table '%s' has %d modifications since last ANALYZE - statistics stale",
			t.Name, t.ModSinceAnalyze))
	}

	if t.SeqScans > 1000 && t.IdxScans > 0 &&
		float64(t.SeqScans)/float64(t.IdxScans) > cfg.GetSeqScanRatioThreshold() {
		recs = append(recs, fmt.Sprintf(
			"Table '%s' has high sequential scans (%d) vs index scans (%d) - possible missing index",
			t.Name, t.SeqScans, t.IdxScans))
	}

	if t.ToastSizeRaw > cfg.GetToastSizeWarningBytes() {
		recs = append(recs, fmt.Sprintf("Table '%s' has %s of TOAST data (images)", t.Name, t.ToastSize))
	}

	return recs
}

func lastVacuumTime(t *TableHealth) *time.Time {
	if t.LastVacuum != nil && (t.LastAutovacuum == nil || t.LastVacuum.After(*t.LastAutovacuum)) {
		return t.LastVacuum
	}
	return t.LastAutovacuum
}
