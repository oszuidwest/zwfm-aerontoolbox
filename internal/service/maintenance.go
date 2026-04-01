package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MaintenanceService handles database health monitoring.
type MaintenanceService struct {
	repo   *database.Repository
	config *config.Config
	notify *notify.NotificationService
}

// newMaintenanceService creates a new MaintenanceService instance.
func newMaintenanceService(repo *database.Repository, cfg *config.Config, notifySvc *notify.NotificationService) *MaintenanceService {
	return &MaintenanceService{
		repo:   repo,
		config: cfg,
		notify: notifySvc,
	}
}

// noIssuesDetected is the placeholder recommendation shown in the API when no problems are found.
const noIssuesDetected = "No issues detected"

// Health types.

// DatabaseHealth represents the overall health status of the database.
type DatabaseHealth struct {
	DatabaseName       string             `json:"database_name"`
	DatabaseVersion    string             `json:"database_version"`
	DatabaseSize       string             `json:"database_size"`
	DatabaseSizeRaw    int64              `json:"database_size_bytes"`
	SchemaName         string             `json:"schema_name"`
	ActiveConnections  int                `json:"active_connections"`
	MaxConnections     int                `json:"max_connections"`
	ConnectionUsagePct float64            `json:"connection_usage_pct"`
	Tables             []TableHealth      `json:"tables"`
	LongRunningQueries []types.LongRunningQuery `json:"long_running_queries"`
	NeedsMaintenance   bool               `json:"needs_maintenance"`
	Recommendations    []string           `json:"recommendations"`
	CheckedAt          time.Time          `json:"checked_at"`
}

// TableHealth represents health statistics for a single table.
type TableHealth struct {
	Name            string     `json:"name"`
	RowCount        int64      `json:"row_count"`
	DeadTuples      int64      `json:"dead_tuples"`
	DeadTupleRatio  float64    `json:"dead_tuple_ratio"`
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

// tableHealthRow contains health statistics and size information for a database table.
type tableHealthRow struct {
	TableName       string     `db:"table_name"`
	LiveTuples      int64      `db:"live_tuples"`
	DeadTuples      int64      `db:"dead_tuples"`
	ModSinceAnalyze int64      `db:"mod_since_analyze"`
	LastVacuum      *time.Time `db:"last_vacuum"`
	LastAutovacuum  *time.Time `db:"last_autovacuum"`
	LastAnalyze     *time.Time `db:"last_analyze"`
	LastAutoanalyze *time.Time `db:"last_autoanalyze"`
	SeqScan         int64      `db:"seq_scan"`
	IdxScan         int64      `db:"idx_scan"`
	TotalSize       int64      `db:"total_size"`
	TableSize       int64      `db:"table_size"`
	IndexSize       int64      `db:"index_size"`
	ToastSize       int64      `db:"toast_size"`
}

// longRunningQueryRow is the database scan target for long-running query detection.
type longRunningQueryRow struct {
	PID      int    `db:"pid"`
	Duration string `db:"duration"`
	Query    string `db:"query"`
	State    string `db:"state"`
}

// Health operations.

// GetHealth retrieves comprehensive database health information.
func (s *MaintenanceService) GetHealth(ctx context.Context) (*DatabaseHealth, error) {
	schema := s.repo.Schema()
	health := &DatabaseHealth{
		DatabaseName: s.config.Database.Name,
		SchemaName:   schema,
		CheckedAt:    time.Now(),
	}

	var version string
	if err := s.repo.DB().GetContext(ctx, &version, "SELECT version()"); err == nil {
		health.DatabaseVersion = version
	}

	dbSize, dbSizeRaw, err := s.getDatabaseSize(ctx)
	if err != nil {
		return nil, types.NewOperationError("get database size", err)
	}
	health.DatabaseSize = dbSize
	health.DatabaseSizeRaw = dbSizeRaw

	tables, err := s.getTableHealth(ctx)
	if err != nil {
		return nil, types.NewOperationError("get table statistics", err)
	}
	health.Tables = tables

	active, maxConns, pct, err := s.getConnectionUsage(ctx)
	if err != nil {
		return nil, types.NewOperationError("get connection usage", err)
	}
	health.ActiveConnections = active
	health.MaxConnections = maxConns
	health.ConnectionUsagePct = pct

	queries, err := s.getLongRunningQueries(ctx)
	if err != nil {
		return nil, types.NewOperationError("get long-running queries", err)
	}
	health.LongRunningQueries = queries

	health.Recommendations = s.generateRecommendations(health)
	if len(health.Recommendations) == 0 {
		health.Recommendations = []string{noIssuesDetected}
	}

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

	// Strip the API placeholder so the notifier sees an empty slice for "no issues".
	recs := health.Recommendations
	if len(recs) == 1 && recs[0] == noIssuesDetected {
		recs = nil
	}

	result := &notify.HealthAlertResult{
		Recommendations:    recs,
		ActiveConnections:  health.ActiveConnections,
		MaxConnections:     health.MaxConnections,
		ConnectionUsagePct: health.ConnectionUsagePct,
		LongRunningQueries: health.LongRunningQueries,
		CheckedAt:          health.CheckedAt,
	}

	s.notify.NotifyHealthAlert(result)
}

// getDatabaseSize returns the total database size.
func (s *MaintenanceService) getDatabaseSize(ctx context.Context) (size string, sizeRaw int64, err error) {
	err = s.repo.DB().GetContext(ctx, &sizeRaw, "SELECT pg_database_size(current_database())")
	if err != nil {
		return "", 0, err
	}
	return util.FormatBytes(sizeRaw), sizeRaw, nil
}

// getConnectionUsage returns the current connection count, maximum, and usage percentage.
func (s *MaintenanceService) getConnectionUsage(ctx context.Context) (active, maxConns int, pct float64, err error) {
	if err := s.repo.DB().GetContext(ctx, &active,
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()"); err != nil {
		return 0, 0, 0, err
	}

	var maxStr string
	if err := s.repo.DB().GetContext(ctx, &maxStr,
		"SELECT current_setting('max_connections')"); err != nil {
		return 0, 0, 0, err
	}

	maxConns, err = strconv.Atoi(maxStr)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("parse max_connections: %w", err)
	}

	if maxConns > 0 {
		pct = float64(active) / float64(maxConns) * 100
	}

	return active, maxConns, pct, nil
}

// getLongRunningQueries returns queries running longer than the configured threshold.
func (s *MaintenanceService) getLongRunningQueries(ctx context.Context) ([]types.LongRunningQuery, error) {
	threshold := s.config.Maintenance.GetLongQueryThresholdSeconds()

	query := `
		SELECT
			pid,
			(now() - query_start)::text AS duration,
			LEFT(query, 200) AS query,
			state
		FROM pg_stat_activity
		WHERE state != 'idle'
			AND query_start IS NOT NULL
			AND now() - query_start > make_interval(secs => $1)
			AND datname = current_database()
			AND pid != pg_backend_pid()
		ORDER BY query_start ASC
	`

	var rows []longRunningQueryRow
	if err := s.repo.DB().SelectContext(ctx, &rows, query, threshold); err != nil {
		return nil, err
	}

	queries := make([]types.LongRunningQuery, 0, len(rows))
	for _, row := range rows {
		queries = append(queries, types.LongRunningQuery(row))
	}

	return queries, nil
}

// getTableHealth retrieves health statistics for all tables in the schema.
func (s *MaintenanceService) getTableHealth(ctx context.Context) ([]TableHealth, error) {
	schema := s.repo.Schema()
	query := `
		SELECT
			s.relname as table_name,
			COALESCE(s.n_live_tup, 0) as live_tuples,
			COALESCE(s.n_dead_tup, 0) as dead_tuples,
			COALESCE(s.n_mod_since_analyze, 0) as mod_since_analyze,
			s.last_vacuum,
			s.last_autovacuum,
			s.last_analyze,
			s.last_autoanalyze,
			COALESCE(s.seq_scan, 0) as seq_scan,
			COALESCE(s.idx_scan, 0) as idx_scan,
			COALESCE(pg_total_relation_size(c.oid), 0) as total_size,
			COALESCE(pg_table_size(c.oid), 0) as table_size,
			COALESCE(pg_indexes_size(c.oid), 0) as index_size,
			COALESCE(pg_total_relation_size(c.reltoastrelid), 0) as toast_size
		FROM pg_stat_user_tables s
		JOIN pg_class c ON c.relname = s.relname
		JOIN pg_namespace n ON n.oid = c.relnamespace AND n.nspname = s.schemaname
		WHERE s.schemaname = $1 AND c.relkind = 'r'
		ORDER BY s.n_live_tup DESC
	`

	var rows []tableHealthRow
	if err := s.repo.DB().SelectContext(ctx, &rows, query, schema); err != nil {
		return nil, types.NewOperationError("get table statistics", err)
	}

	cfg := s.config.Maintenance
	tables := make([]TableHealth, 0, len(rows))
	for _, row := range rows {
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

		if row.LiveTuples > 0 {
			table.DeadTupleRatio = float64(row.DeadTuples) / float64(row.LiveTuples+row.DeadTuples) * 100
		}

		table.NeedsVacuum = table.DeadTupleRatio > cfg.GetBloatThreshold() ||
			table.DeadTuples > cfg.GetDeadTupleThreshold()

		neverAnalyzed := table.LastAnalyze == nil && table.LastAutoanalyze == nil && table.RowCount > 0
		staleStats := table.RowCount > 0 && table.ModSinceAnalyze > (table.RowCount*int64(cfg.GetStaleStatsThreshold())/100)
		table.NeedsAnalyze = neverAnalyzed || staleStats

		tables = append(tables, table)
	}

	return tables, nil
}

// Recommendations.

// generateRecommendations returns recommendations for the entire database health report.
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

	if t.DeadTupleRatio > cfg.GetBloatThreshold() {
		recs = append(recs, fmt.Sprintf("Table '%s' has %.1f%% dead tuples - VACUUM recommended", t.Name, t.DeadTupleRatio))
	}

	if t.DeadTuples > cfg.GetDeadTupleThreshold() {
		recs = append(recs, fmt.Sprintf("Table '%s' has %d dead tuples - VACUUM recommended", t.Name, t.DeadTuples))
	}

	if t.LastVacuum == nil && t.LastAutovacuum == nil && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf("Table '%s' has never been vacuumed", t.Name))
	}

	if lastVac := lastVacuumTime(t); lastVac != nil && time.Since(*lastVac) > cfg.GetVacuumStaleness() && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf("Table '%s' has not been vacuumed in over %d days", t.Name, cfg.GetVacuumStalenessDays()))
	}

	if t.LastAnalyze == nil && t.LastAutoanalyze == nil && t.RowCount > minRows {
		recs = append(recs, fmt.Sprintf("Table '%s' has never been analyzed - ANALYZE recommended", t.Name))
	}

	if t.RowCount > 0 && t.ModSinceAnalyze > 0 {
		threshold := t.RowCount * int64(cfg.GetStaleStatsThreshold()) / 100
		if t.ModSinceAnalyze > threshold {
			recs = append(recs, fmt.Sprintf("Table '%s' has %d modifications since last ANALYZE - statistics stale", t.Name, t.ModSinceAnalyze))
		}
	}

	if t.SeqScans > 1000 && t.IdxScans > 0 && float64(t.SeqScans)/float64(t.IdxScans) > cfg.GetSeqScanRatioThreshold() {
		recs = append(recs, fmt.Sprintf("Table '%s' has high sequential scans (%d) vs index scans (%d) - possible missing index", t.Name, t.SeqScans, t.IdxScans))
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
