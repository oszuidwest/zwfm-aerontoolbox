package database

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// TableStatsRow is the raw PostgreSQL statistics row for one table.
type TableStatsRow struct {
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

// GetDatabaseVersion returns the PostgreSQL server version string.
func (r *Repository) GetDatabaseVersion(ctx context.Context) (string, error) {
	var version string
	if err := r.db.GetContext(ctx, &version, "SELECT version()"); err != nil {
		return "", types.NewOperationError("get database version", err)
	}
	return version, nil
}

// GetDatabaseSize returns the current database size in bytes.
func (r *Repository) GetDatabaseSize(ctx context.Context) (int64, error) {
	var size int64
	if err := r.db.GetContext(ctx, &size, "SELECT pg_database_size(current_database())"); err != nil {
		return 0, types.NewOperationError("get database size", err)
	}
	return size, nil
}

// GetConnectionStats returns the active connection count and the server's
// max_connections setting.
func (r *Repository) GetConnectionStats(ctx context.Context) (active, maxConns int, err error) {
	if err := r.db.GetContext(ctx, &active,
		"SELECT count(*) FROM pg_stat_activity WHERE datname = current_database()"); err != nil {
		return 0, 0, types.NewOperationError("get connection usage", err)
	}

	var maxStr string
	if err := r.db.GetContext(ctx, &maxStr,
		"SELECT current_setting('max_connections')"); err != nil {
		return 0, 0, types.NewOperationError("get connection usage", err)
	}

	maxConns, err = strconv.Atoi(maxStr)
	if err != nil {
		return 0, 0, types.NewOperationError("get connection usage", fmt.Errorf("parse max_connections: %w", err))
	}

	return active, maxConns, nil
}

// GetLongRunningQueries returns queries running longer than thresholdSeconds.
func (r *Repository) GetLongRunningQueries(
	ctx context.Context,
	thresholdSeconds int,
	exposeQueryText bool,
) ([]types.LongRunningQuery, error) {
	query := fmt.Sprintf(`
		SELECT
			pid,
			(now() - query_start)::text AS duration,
			%s AS query,
			state
		FROM pg_stat_activity
		WHERE state != 'idle'
			AND query_start IS NOT NULL
			AND now() - query_start > make_interval(secs => $1)
			AND datname = current_database()
			AND pid != pg_backend_pid()
		ORDER BY query_start ASC
	`, longRunningQueryTextExpr(exposeQueryText))

	var queries []types.LongRunningQuery
	if err := r.db.SelectContext(ctx, &queries, query, thresholdSeconds); err != nil {
		return nil, types.NewOperationError("get long-running queries", err)
	}
	return queries, nil
}

func longRunningQueryTextExpr(expose bool) string {
	if expose {
		return "LEFT(query, 200)"
	}
	return "''"
}

// GetTableStats retrieves maintenance statistics for all user tables in the schema.
func (r *Repository) GetTableStats(ctx context.Context) ([]TableStatsRow, error) {
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

	var rows []TableStatsRow
	if err := r.db.SelectContext(ctx, &rows, query, r.schema); err != nil {
		return nil, types.NewOperationError("get table statistics", err)
	}
	return rows, nil
}
