package service

import (
	"testing"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/async"
)

func TestMediaFileCheckProblemCountUsesOnlyScheduledResult(t *testing.T) {
	svc := &MediaFileCheckService{runner: async.New()}
	defer svc.Close()

	manual := &MediaCheckResult{
		Summary: MediaCheckSummary{Missing: 3},
	}
	svc.publishCompleted(manual, 1, false)

	if got := svc.Status().Result; got != manual {
		t.Fatalf("status result = %p, want manual result %p", got, manual)
	}
	if got := svc.ProblemCount(); got != 0 {
		t.Fatalf("manual ProblemCount = %d, want 0", got)
	}

	scheduled := &MediaCheckResult{
		Summary: MediaCheckSummary{Missing: 1, Ambiguous: 2, Errors: 3},
	}
	svc.publishCompleted(scheduled, 2, true)

	if got := svc.ProblemCount(); got != 6 {
		t.Fatalf("scheduled ProblemCount = %d, want 6", got)
	}
}

func TestProblemCountRunErrorCountsAsOneProblem(t *testing.T) {
	result := &MediaCheckResult{
		Error:   "context deadline exceeded",
		Summary: MediaCheckSummary{Missing: 20},
	}

	if got := problemCount(result); got != 1 {
		t.Fatalf("problemCount = %d, want 1", got)
	}
}
