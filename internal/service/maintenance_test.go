package service

import (
	"testing"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
)

// TestConvertTableRow covers the convertTableRow helper, including the edge case
// where LiveTuples == 0 but DeadTuples > 0 (all-dead table), which would previously
// leave DeadTuplePct at 0 and potentially miss the NeedsVacuum signal.
//
// defaultCfg uses zero-value fields so getter methods return their built-in defaults:
// BloatThreshold=10.0, DeadTupleThreshold=10000, StaleStatsThresholdPct=10.
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
			// dead. 100.0 > bloatThreshold(10.0) -> NeedsVacuum must be true.
			name:       "all dead no live",
			row:        &database.TableStatsRow{TableName: "t", LiveTuples: 0, DeadTuples: 50},
			wantPct:    100.0,
			wantVacuum: true,
		},
		{
			// live=900, dead=100, total=1000 -> pct = 100/1000*100 = 10.0.
			// NeedsVacuum must be false: the code uses >, not >=, so exactly at the
			// default threshold (10.0) is not a trigger. NeedsAnalyze is true
			// because the table has rows but was never analyzed.
			name:        "exactly at bloat threshold",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 900, DeadTuples: 100},
			wantPct:     10.0,
			wantAnalyze: true,
		},
		{
			// BloatThreshold=5.0: pct=10.0 > 5.0 -> NeedsVacuum=true, proving cfg is used.
			name:        "needs vacuum by custom threshold",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 900, DeadTuples: 100},
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
			// Row count > 0 with no analyze timestamps -> NeedsAnalyze=true.
			name:        "needs analyze never analyzed",
			row:         &database.TableStatsRow{TableName: "t", LiveTuples: 100},
			wantAnalyze: true,
		},
		{
			// live=100, ModSinceAnalyze=11 > 100*10%=10 -> stale stats trigger NeedsAnalyze.
			name: "needs analyze stale stats",
			row: &database.TableStatsRow{
				TableName:       "t",
				LiveTuples:      100,
				ModSinceAnalyze: 11,
				LastAnalyze:     &now,
			},
			wantAnalyze: true,
		},
		{
			// live=100, ModSinceAnalyze=5 < 10% threshold, recently analyzed -> NeedsAnalyze=false.
			name: "no needs analyze",
			row: &database.TableStatsRow{
				TableName:       "t",
				LiveTuples:      100,
				ModSinceAnalyze: 5,
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
