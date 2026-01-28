package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MaintenanceService handles database health monitoring and maintenance operations.
type MaintenanceService struct {
	repo     *database.Repository
	config   *config.Config
	runner   *async.Runner
	statusMu sync.RWMutex
	status   *MaintenanceStatus
}

// MaintenanceStatus tracks the progress of an async maintenance operation.
type MaintenanceStatus struct {
	Running      bool                 `json:"running"`
	Operation    string               `json:"operation,omitempty"`
	StartedAt    *time.Time           `json:"started_at,omitempty"`
	EndedAt      *time.Time           `json:"ended_at,omitempty"`
	Success      bool                 `json:"success"`
	TablesTotal  int                  `json:"tables_total"`
	TablesDone   int                  `json:"tables_done"`
	CurrentTable string               `json:"current_table,omitempty"`
	LastResult   *MaintenanceResponse `json:"last_result,omitempty"`
	Error        string               `json:"error,omitempty"`
}

// newMaintenanceService creates a new MaintenanceService instance.
func newMaintenanceService(repo *database.Repository, cfg *config.Config) *MaintenanceService {
	return &MaintenanceService{
		repo:   repo,
		config: cfg,
		runner: async.New(),
	}
}

// Close stops the maintenance service and waits for any running operation to complete.
func (s *MaintenanceService) Close() {
	s.runner.Close()
}

// Maintenance types.

// DatabaseHealth represents the overall health status of the database.
type DatabaseHealth struct {
	DatabaseName     string        `json:"database_name"`
	DatabaseVersion  string        `json:"database_version"`
	DatabaseSize     string        `json:"database_size"`
	DatabaseSizeRaw  int64         `json:"database_size_bytes"`
	SchemaName       string        `json:"schema_name"`
	Tables           []TableHealth `json:"tables"`
	NeedsMaintenance bool          `json:"needs_maintenance"`
	Recommendations  []string      `json:"recommendations"`
	CheckedAt        time.Time     `json:"checked_at"`
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

// VacuumOptions configures vacuum operation parameters.
type VacuumOptions struct {
	Tables  []string // Tables lists the tables to vacuum; auto-selects when empty
	Analyze bool     // Analyze runs ANALYZE after VACUUM completes
}

// MaintenanceResult represents the result of a maintenance operation (vacuum or analyze) on a single table.
type MaintenanceResult struct {
	Table          string  `json:"table"`
	Success        bool    `json:"success"`
	Message        string  `json:"message"`
	DeadTuples     int64   `json:"dead_tuples_before"`
	DeadTupleRatio float64 `json:"dead_tuple_ratio_before"`
	Duration       string  `json:"duration,omitempty"`
	Analyzed       bool    `json:"analyzed"`
	Skipped        bool    `json:"skipped,omitempty"`
	SkippedReason  string  `json:"skipped_reason,omitempty"`
}

// MaintenanceResponse represents the overall result of maintenance operations (vacuum/analyze).
type MaintenanceResponse struct {
	TablesTotal   int                 `json:"tables_total"`
	TablesSuccess int                 `json:"tables_success"`
	TablesFailed  int                 `json:"tables_failed"`
	TablesSkipped int                 `json:"tables_skipped"`
	Results       []MaintenanceResult `json:"results"`
	ExecutedAt    time.Time           `json:"executed_at"`
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

// maintenanceContext provides table health data for vacuum and analyze operations.
type maintenanceContext struct {
	tables       []TableHealth
	tablesByName map[string]TableHealth
	schema       string
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

	health.Recommendations = s.generateRecommendations(tables)

	for i := range tables {
		if tables[i].NeedsVacuum || tables[i].NeedsAnalyze {
			health.NeedsMaintenance = true
			break
		}
	}

	return health, nil
}

// getDatabaseSize returns the total database size.
func (s *MaintenanceService) getDatabaseSize(ctx context.Context) (size string, sizeRaw int64, err error) {
	err = s.repo.DB().GetContext(ctx, &sizeRaw, "SELECT pg_database_size(current_database())")
	if err != nil {
		return "", 0, err
	}
	return util.FormatBytes(sizeRaw), sizeRaw, nil
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

// generateRecommendations returns maintenance recommendations for tables requiring attention.
func (s *MaintenanceService) generateRecommendations(tables []TableHealth) []string {
	var recs []string

	for i := range tables {
		t := &tables[i]
		recs = s.checkTableHealth(t, recs)
	}

	if len(recs) == 0 {
		return []string{"No issues detected"}
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

// Maintenance operations.

// newMaintenanceContext loads current table health data for maintenance operations.
func (s *MaintenanceService) newMaintenanceContext(ctx context.Context) (*maintenanceContext, error) {
	tables, err := s.getTableHealth(ctx)
	if err != nil {
		return nil, err
	}

	tablesByName := make(map[string]TableHealth, len(tables))
	for i := range tables {
		tablesByName[tables[i].Name] = tables[i]
	}

	return &maintenanceContext{
		tables:       tables,
		tablesByName: tablesByName,
		schema:       s.repo.Schema(),
	}, nil
}

// selectTablesToProcess determines tables for maintenance based on request or automatic selection.
// Returns tables to process and any skipped table results.
func (mctx *maintenanceContext) selectTablesToProcess(requestedTables []string, autoSelectFn func(TableHealth) bool) ([]TableHealth, []MaintenanceResult) {
	var tablesToProcess []TableHealth
	var skipped []MaintenanceResult

	if len(requestedTables) > 0 {
		for _, tableName := range requestedTables {
			if t, exists := mctx.tablesByName[tableName]; exists {
				tablesToProcess = append(tablesToProcess, t)
			} else {
				skipped = append(skipped, MaintenanceResult{
					Table:         tableName,
					Success:       false,
					Message:       fmt.Sprintf("Table '%s' not found in schema '%s'", tableName, mctx.schema),
					Skipped:       true,
					SkippedReason: "not found",
				})
			}
		}
	} else {
		for i := range mctx.tables {
			if autoSelectFn(mctx.tables[i]) {
				tablesToProcess = append(tablesToProcess, mctx.tables[i])
			}
		}
	}

	return tablesToProcess, skipped
}

// executeVacuum executes VACUUM on a table with optional ANALYZE.
func (s *MaintenanceService) executeVacuum(ctx context.Context, tableName string, analyze bool) error {
	if !types.IsValidIdentifier(tableName) {
		return types.NewValidationError("table", fmt.Sprintf("invalid table name: %s", tableName))
	}

	schema := s.repo.Schema()
	var query string
	if analyze {
		query = fmt.Sprintf("VACUUM ANALYZE %s.%s", schema, tableName)
	} else {
		query = fmt.Sprintf("VACUUM %s.%s", schema, tableName)
	}

	_, err := s.repo.DB().ExecContext(ctx, query)
	return err
}

// executeAnalyze executes ANALYZE on the specified table.
func (s *MaintenanceService) executeAnalyze(ctx context.Context, tableName string) error {
	if !types.IsValidIdentifier(tableName) {
		return types.NewValidationError("table", fmt.Sprintf("invalid table name: %s", tableName))
	}

	schema := s.repo.Schema()
	query := fmt.Sprintf("ANALYZE %s.%s", schema, tableName)
	_, err := s.repo.DB().ExecContext(ctx, query)
	return err
}

// Async operations.

// maintenanceTask defines parameters for a generic maintenance operation.
type maintenanceTask struct {
	operationName string                                        // "VACUUM", "VACUUM ANALYZE", "ANALYZE"
	tables        []string                                      // Requested tables (empty = auto-select)
	autoSelect    func(TableHealth) bool                        // Auto-selection criteria
	execute       func(ctx context.Context, table string) error // Execute operation on a table
	analyzed      bool                                          // Whether this operation includes ANALYZE
}

// Status returns the current state and result of the last maintenance operation.
func (s *MaintenanceService) Status() *MaintenanceStatus {
	s.statusMu.RLock()
	defer s.statusMu.RUnlock()

	if s.status == nil {
		return &MaintenanceStatus{Running: s.runner.IsRunning()}
	}

	status := *s.status
	status.Running = s.runner.IsRunning()
	return &status
}

// StartVacuum starts an async vacuum operation.
// Returns an error if a maintenance operation is already running.
func (s *MaintenanceService) StartVacuum(opts VacuumOptions) error {
	if !s.runner.TryStart() {
		return types.NewConflictError("maintenance", "maintenance operation already in progress")
	}

	statusKey, opName := "vacuum", "VACUUM"
	if opts.Analyze {
		statusKey, opName = "vacuum_analyze", "VACUUM ANALYZE"
	}
	s.initStatus(statusKey)

	cfg := s.config.Maintenance
	task := maintenanceTask{
		operationName: opName,
		tables:        opts.Tables,
		autoSelect: func(t TableHealth) bool {
			return t.DeadTupleRatio > cfg.GetBloatThreshold() || t.DeadTuples > cfg.GetDeadTupleThreshold()
		},
		execute: func(ctx context.Context, table string) error {
			return s.executeVacuum(ctx, table, opts.Analyze)
		},
		analyzed: opts.Analyze,
	}

	s.runner.Go(func() {
		ctx, cancel := s.runner.Context(s.config.Maintenance.GetTimeout())
		defer cancel()
		s.runMaintenance(ctx, task)
	})
	return nil
}

// StartAnalyze starts an async analyze operation.
// Returns an error if a maintenance operation is already running.
func (s *MaintenanceService) StartAnalyze(tableNames []string) error {
	if !s.runner.TryStart() {
		return types.NewConflictError("maintenance", "maintenance operation already in progress")
	}

	s.initStatus("analyze")

	cfg := s.config.Maintenance
	task := maintenanceTask{
		operationName: "ANALYZE",
		tables:        tableNames,
		autoSelect: func(t TableHealth) bool {
			if t.LastAnalyze == nil && t.LastAutoanalyze == nil && t.RowCount > 0 {
				return true
			}
			if t.RowCount > 0 {
				threshold := t.RowCount * int64(cfg.GetStaleStatsThreshold()) / 100
				if t.ModSinceAnalyze > threshold {
					return true
				}
			}
			return false
		},
		execute: func(ctx context.Context, table string) error {
			return s.executeAnalyze(ctx, table)
		},
		analyzed: true,
	}

	s.runner.Go(func() {
		ctx, cancel := s.runner.Context(s.config.Maintenance.GetTimeout())
		defer cancel()
		s.runMaintenance(ctx, task)
	})
	return nil
}

// initStatus initializes the status for a new maintenance operation.
func (s *MaintenanceService) initStatus(operation string) {
	now := time.Now()
	s.statusMu.Lock()
	s.status = &MaintenanceStatus{
		Operation: operation,
		StartedAt: &now,
	}
	s.statusMu.Unlock()
}

// runMaintenance executes a maintenance task. Context is managed by the caller via runner.Go().
func (s *MaintenanceService) runMaintenance(ctx context.Context, task maintenanceTask) {
	mctx, err := s.newMaintenanceContext(ctx)
	if err != nil {
		s.completeWithError(err.Error())
		return
	}

	tables, skipped := mctx.selectTablesToProcess(task.tables, task.autoSelect)

	response := &MaintenanceResponse{
		ExecutedAt:    time.Now(),
		Results:       skipped,
		TablesTotal:   len(tables) + len(skipped),
		TablesSkipped: len(skipped),
	}

	s.statusMu.Lock()
	s.status.TablesTotal = response.TablesTotal
	s.statusMu.Unlock()

	for i := range tables {
		// Abort early if operation was cancelled
		if ctx.Err() != nil {
			s.completeWithError("operation cancelled")
			return
		}

		s.statusMu.Lock()
		s.status.CurrentTable = tables[i].Name
		s.status.TablesDone = i
		s.statusMu.Unlock()

		result := MaintenanceResult{
			Table:          tables[i].Name,
			DeadTuples:     tables[i].DeadTuples,
			DeadTupleRatio: tables[i].DeadTupleRatio,
			Analyzed:       task.analyzed,
		}

		start := time.Now()
		err := task.execute(ctx, tables[i].Name)
		result.Duration = time.Since(start).Round(time.Millisecond).String()

		if err != nil {
			result.Success = false
			result.Message = fmt.Sprintf("%s failed on '%s': %v", task.operationName, tables[i].Name, err)
			response.TablesFailed++
		} else {
			result.Success = true
			result.Message = fmt.Sprintf("%s completed successfully on '%s'", task.operationName, tables[i].Name)
			response.TablesSuccess++
		}

		response.Results = append(response.Results, result)
	}

	s.completeWithResult(response)
}

// completeWithResult marks the maintenance operation as completed with a result.
func (s *MaintenanceService) completeWithResult(result *MaintenanceResponse) {
	now := time.Now()
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.EndedAt = &now
	s.status.Success = true
	s.status.CurrentTable = ""
	s.status.TablesDone = s.status.TablesTotal
	s.status.LastResult = result
}

// completeWithError marks the maintenance operation as completed with an error.
func (s *MaintenanceService) completeWithError(errMsg string) {
	now := time.Now()
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.status.EndedAt = &now
	s.status.Success = false
	s.status.CurrentTable = ""
	s.status.Error = errMsg
}
