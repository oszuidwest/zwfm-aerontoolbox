package service

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// TestConvertTableRow covers the convertTableRow helper, including the edge case
// where LiveTuples == 0 but DeadTuples > 0 (all-dead table), which would previously
// leave DeadTuplePct at 0 and potentially miss the NeedsVacuum signal.
//
// defaultCfg uses zero-value fields so getter methods return their built-in defaults:
// BloatThreshold=10.0, DeadTupleThreshold=10000, StaleStatsThresholdPct=10,
// MinRowsForRecommendation=1000.
func TestConvertTableRow(t *testing.T) {
	defaultCfg := &config.MaintenanceConfig{}
	now := time.Now()

	tests := []struct {
		name        string
		row         *database.TableStatsRow
		cfg         *config.MaintenanceConfig
		wantPct     float64
		wantVacuum  bool
		wantAnalyze bool
	}{
		{
			name: "zero tuples",
			row:  &database.TableStatsRow{TableName: "t", LiveTuples: 0, DeadTuples: 0},
		},
		{
			// LiveTuples=0, DeadTuples=50: the guard must use total (live+dead) not
			// just live, otherwise DeadTuplePct stays 0 even though all tuples are
			// dead. NeedsVacuum stays false: no live rows is below
			// MinRowsForRecommendation and 50 is below DeadTupleThreshold.
			name:    "all dead no live",
			row:     &database.TableStatsRow{TableName: "t", LiveTuples: 0, DeadTuples: 50},
			wantPct: 100.0,
		},
		{
			// live=9000, dead=1000, total=10000 -> pct = 1000/10000*100 = 10.0.
			// NeedsVacuum must be false: the code uses >, not >=, so exactly at the
			// default threshold (10.0) is not a trigger. NeedsAnalyze is true
			// because the table has rows but was never analyzed.
			name:        "exactly at bloat threshold",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 9000, DeadTuples: 1000},
			wantPct:     10.0,
			wantAnalyze: true,
		},
		{
			// BloatThreshold=5.0: pct=10.0 > 5.0 -> NeedsVacuum=true, proving cfg is used.
			name:        "needs vacuum by custom threshold",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 9000, DeadTuples: 1000},
			cfg:         &config.MaintenanceConfig{BloatThreshold: 5.0},
			wantPct:     10.0,
			wantVacuum:  true,
			wantAnalyze: true,
		},
		{
			// live=10_000_000, dead=15_000 -> pct ~ 0.15% (below 10% threshold)
			// but dead count 15_000 > DeadTupleThreshold(10_000).
			name:        "needs vacuum by absolute count",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 10_000_000, DeadTuples: 15_000},
			wantPct:     float64(15_000) / float64(10_015_000) * 100,
			wantVacuum:  true,
			wantAnalyze: true,
		},
		{
			// Row count above MinRowsForRecommendation with no analyze timestamps -> NeedsAnalyze=true.
			name:        "needs analyze never analyzed",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 1001},
			wantAnalyze: true,
		},
		{
			// live=2000, ModSinceAnalyze=201 > 2000*10%=200 -> stale stats trigger NeedsAnalyze.
			name: "needs analyze stale stats",
			row: &database.TableStatsRow{
				TableName:       "t",
				LiveTuples:      2000,
				ModSinceAnalyze: 201,
				LastAnalyze:     &now,
			},
			wantAnalyze: true,
		},
		{
			// live=2000, ModSinceAnalyze=200 is not above 10%, recently analyzed -> NeedsAnalyze=false.
			name: "no needs analyze",
			row: &database.TableStatsRow{
				TableName:       "t",
				LiveTuples:      2000,
				ModSinceAnalyze: 200,
				LastAnalyze:     &now,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.cfg
			if cfg == nil {
				cfg = defaultCfg
			}

			got := convertTableRow(tt.row, cfg)
			if got.DeadTuplePct != tt.wantPct {
				t.Errorf("DeadTuplePct = %.4f, want %.4f", got.DeadTuplePct, tt.wantPct)
			}
			if got.NeedsVacuum != tt.wantVacuum {
				t.Errorf("NeedsVacuum = %t, want %t", got.NeedsVacuum, tt.wantVacuum)
			}
			if got.NeedsAnalyze != tt.wantAnalyze {
				t.Errorf("NeedsAnalyze = %t, want %t", got.NeedsAnalyze, tt.wantAnalyze)
			}
		})
	}
}

func TestGenerateRecommendationsPointsAtXminHolder(t *testing.T) {
	svc := &MaintenanceService{config: &config.Config{}}
	now := time.Now()
	table := TableHealth{
		Name: "track", RowCount: 5000, DeadTuples: 20000, DeadTuplePct: 80,
		LastAutovacuum: &now, LastAutoanalyze: &now,
	}
	started := time.Date(2026, 9, 26, 12, 36, 0, 0, time.Local)
	queries := []types.LongRunningQuery{
		{PID: 7, Duration: "00:01:00", State: "active"},
		{PID: 50, Duration: "00:00:30", State: "active", XminAge: 5},
		{PID: 42, Duration: "00:00:20", State: "idle in transaction", ApplicationName: "DBmod", User: "aeron",
			StartedAt: started, XminAge: 900},
		{PID: 60, Duration: "00:00:10", State: "active", XminAge: 3},
	}

	got := svc.generateRecommendations(&DatabaseHealth{Tables: []TableHealth{table}, LongRunningQueries: queries})
	want := []string{
		`VACUUM is blocked: PID 42 (application "DBmod", user aeron, idle in transaction since 2026-09-26 12:36:00) ` +
			"holds back the xmin horizon (xmin age 900) - dead rows newer than its snapshot cannot be removed until it ends",
		"Table 'track' has 80.0% dead tuples - VACUUM may be blocked by PID 42",
		"Table 'track' has 20000 dead tuples - VACUUM may be blocked by PID 42",
		"Long-running query detected (PID 7, running for 00:01:00)",
		"Long-running query detected (PID 50, running for 00:00:30)",
		"Long-running query detected (PID 60, running for 00:00:10)",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("recommendations =\n%q\nwant\n%q", got, want)
	}

	got = svc.generateRecommendations(&DatabaseHealth{Tables: []TableHealth{table}, LongRunningQueries: queries[:1]})
	if want := "Table 'track' has 80.0% dead tuples - VACUUM recommended"; len(got) == 0 || got[0] != want {
		t.Fatalf("recommendations without holder = %q, want first %q", got, want)
	}
}

func TestCheckTableHealthSkipsRatioChecksOnSmallTables(t *testing.T) {
	svc := &MaintenanceService{config: &config.Config{}}
	now := time.Now()
	row := database.TableStatsRow{TableName: "computer", ModSinceAnalyze: 500, SeqScan: 2000, IdxScan: 1, LastAutovacuum: &now}

	// The default min_rows_for_recommendation is 1000. The flags follow the advice.
	row.LiveTuples, row.DeadTuples = 1000, 1000
	table := convertTableRow(&row, &svc.config.Maintenance)
	if got := svc.checkTableHealth(&table, nil, "VACUUM recommended"); len(got) != 0 || table.NeedsVacuum || table.NeedsAnalyze {
		t.Fatalf("1000 rows: recommendations = %q, needs vacuum %v, needs analyze %v, want none", got, table.NeedsVacuum, table.NeedsAnalyze)
	}

	row.LiveTuples, row.DeadTuples = 1001, 1001
	table = convertTableRow(&row, &svc.config.Maintenance)
	want := []string{
		"Table 'computer' has 50.0% dead tuples - VACUUM recommended",
		"Table 'computer' has never been analyzed - ANALYZE recommended",
		"Table 'computer' has 500 modifications since last ANALYZE - statistics stale",
		"Table 'computer' has high sequential scans (2000) vs index scans (1) - possible missing index",
	}
	if got := svc.checkTableHealth(&table, nil, "VACUUM recommended"); !slices.Equal(got, want) || !table.NeedsVacuum || !table.NeedsAnalyze {
		t.Fatalf("1001 rows: recommendations = %q, needs vacuum %v, needs analyze %v, want %q and both flags", got, table.NeedsVacuum, table.NeedsAnalyze, want)
	}
}

func TestCheckTableHealthReportsVacuumAgeOnlyWithDeadTuples(t *testing.T) {
	svc := &MaintenanceService{config: &config.Config{}}
	now := time.Now()
	monthAgo := now.AddDate(0, -1, 0)
	table := TableHealth{Name: "track", RowCount: 5000, LastAutovacuum: &monthAgo, LastAutoanalyze: &now}

	if got := svc.checkTableHealth(&table, nil, "VACUUM recommended"); len(got) != 0 {
		t.Fatalf("recommendations without dead tuples = %q, want none", got)
	}

	table.DeadTuples = 1
	want := []string{"Table 'track' has not been vacuumed in over 7 days"}
	if got := svc.checkTableHealth(&table, nil, "VACUUM recommended"); !slices.Equal(got, want) {
		t.Fatalf("recommendations with dead tuples = %q, want %q", got, want)
	}

	table.LastAutovacuum = nil
	want = []string{"Table 'track' has never been vacuumed"}
	if got := svc.checkTableHealth(&table, nil, "VACUUM recommended"); !slices.Equal(got, want) {
		t.Fatalf("recommendations when never vacuumed = %q, want %q", got, want)
	}
}

func TestGenerateRecommendationsWarnsAboveFreezeMaxAge(t *testing.T) {
	svc := &MaintenanceService{config: &config.Config{}}
	health := &DatabaseHealth{FrozenXIDAge: 200_000_000, FreezeMaxAge: 200_000_000}

	if got := svc.generateRecommendations(health); len(got) != 0 {
		t.Fatalf("recommendations at freeze max age = %q, want none", got)
	}

	health.FrozenXIDAge++
	if got := svc.generateRecommendations(health); len(got) != 1 || !strings.HasPrefix(got[0], "Transaction ID age 200000001 is above autovacuum_freeze_max_age (200000000) - ") {
		t.Fatalf("recommendations above freeze max age = %q", got)
	}
}
