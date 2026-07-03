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

	t.Run("ZeroTuples", func(t *testing.T) {
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 0, DeadTuples: 0}
		got := convertTableRow(row, defaultCfg)
		if got.DeadTuplePct != 0 {
			t.Errorf("DeadTuplePct = %.2f, want 0", got.DeadTuplePct)
		}
		if got.NeedsVacuum {
			t.Error("NeedsVacuum = true, want false")
		}
		if got.NeedsAnalyze {
			t.Error("NeedsAnalyze = true, want false")
		}
	})

	t.Run("AllDeadNoLive", func(t *testing.T) {
		// LiveTuples=0, DeadTuples=50: the guard must use total (live+dead) not just live,
		// otherwise DeadTuplePct stays 0 even though all tuples are dead.
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 0, DeadTuples: 50}
		got := convertTableRow(row, defaultCfg)
		if got.DeadTuplePct != 100.0 {
			t.Errorf("DeadTuplePct = %.2f, want 100.0", got.DeadTuplePct)
		}
		// 100.0 > bloatThreshold(10.0) → NeedsVacuum must be true.
		if !got.NeedsVacuum {
			t.Error("NeedsVacuum = false, want true (dead-tuple percentage is 100%)")
		}
	})

	t.Run("MixedTuples", func(t *testing.T) {
		// live=900, dead=100, total=1000 → pct = 100/1000*100 = 10.0.
		// NeedsVacuum must be false: the code uses >, not >=, so exactly at the
		// default threshold (10.0) is not a trigger.
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 900, DeadTuples: 100}
		got := convertTableRow(row, defaultCfg)
		const wantPct = 10.0
		if got.DeadTuplePct != wantPct {
			t.Errorf("DeadTuplePct = %.2f, want %.2f", got.DeadTuplePct, wantPct)
		}
		if got.NeedsVacuum {
			t.Errorf("NeedsVacuum = true, want false (%.1f%% is not > threshold=10.0%%)", got.DeadTuplePct)
		}
	})

	t.Run("NeedsVacuumByCustomThreshold", func(t *testing.T) {
		// BloatThreshold=5.0: pct=10.0 > 5.0 → NeedsVacuum=true, proving cfg is used.
		customCfg := &config.MaintenanceConfig{BloatThreshold: 5.0}
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 900, DeadTuples: 100}
		got := convertTableRow(row, customCfg)
		if !got.NeedsVacuum {
			t.Errorf("NeedsVacuum = false, want true (pct=%.1f%% > custom threshold=5.0%%)", got.DeadTuplePct)
		}
	})

	t.Run("NeedsVacuumByRatio", func(t *testing.T) {
		// live=800, dead=200, total=1000 → pct = 20.0 > bloatThreshold(10.0)
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 800, DeadTuples: 200}
		got := convertTableRow(row, defaultCfg)
		if !got.NeedsVacuum {
			t.Errorf("NeedsVacuum = false, want true (pct=%.1f%% > threshold=10%%)", got.DeadTuplePct)
		}
	})

	t.Run("NeedsVacuumByAbsoluteCount", func(t *testing.T) {
		// live=10_000_000, dead=15_000 → pct ≈ 0.15% (below 10% threshold)
		// but dead count 15_000 > DeadTupleThreshold(10_000).
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 10_000_000, DeadTuples: 15_000}
		got := convertTableRow(row, defaultCfg)
		if !got.NeedsVacuum {
			t.Errorf("NeedsVacuum = false, want true (dead=%d > threshold=10000)", row.DeadTuples)
		}
	})

	t.Run("NeedsAnalyzeNeverAnalyzed", func(t *testing.T) {
		// Row count > 0 with no analyze timestamps → NeedsAnalyze=true.
		row := &database.TableStatsRow{TableName: "t", LiveTuples: 100}
		got := convertTableRow(row, defaultCfg)
		if !got.NeedsAnalyze {
			t.Error("NeedsAnalyze = false, want true (table with rows was never analyzed)")
		}
	})

	t.Run("NeedsAnalyzeStaleStats", func(t *testing.T) {
		// live=100, ModSinceAnalyze=11 > 100*10%=10 → stale stats trigger NeedsAnalyze.
		now := time.Now()
		row := &database.TableStatsRow{
			TableName:       "t",
			LiveTuples:      100,
			ModSinceAnalyze: 11,
			LastAnalyze:     &now,
		}
		got := convertTableRow(row, defaultCfg)
		if !got.NeedsAnalyze {
			t.Errorf("NeedsAnalyze = false, want true (modifications=%d > threshold=10)", row.ModSinceAnalyze)
		}
	})

	t.Run("NoNeedsAnalyze", func(t *testing.T) {
		// live=100, ModSinceAnalyze=5 < 10% threshold, recently analyzed → NeedsAnalyze=false.
		now := time.Now()
		row := &database.TableStatsRow{
			TableName:       "t",
			LiveTuples:      100,
			ModSinceAnalyze: 5,
			LastAnalyze:     &now,
		}
		got := convertTableRow(row, defaultCfg)
		if got.NeedsAnalyze {
			t.Errorf("NeedsAnalyze = true, want false (modifications=%d <= threshold=10)", row.ModSinceAnalyze)
		}
	})
}
