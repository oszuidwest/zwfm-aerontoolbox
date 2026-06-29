package api

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// MediaCheckStartResponse is returned by POST /media/files/check. RunID is the
// monotonic ID of the run just started; clients poll /status until
// completed_run_id >= RunID && running == false.
type MediaCheckStartResponse struct {
	Message string `json:"message"`
	RunID   uint64 `json:"run_id"`
	Check   string `json:"check"`
}

// dateParam is the accepted date format for media file check scope parameters.
const dateParam = "2006-01-02"

func (s *Server) handleMediaFileCheck(w http.ResponseWriter, r *http.Request) {
	opts, err := parseMediaCheckOptions(r.URL.Query(), s.service.Config().MediaFileCheck.GetMaxRangeDays())
	if err != nil {
		respondError(w, errorCode(err), err.Error())
		return
	}

	runID, err := s.service.MediaFileCheck.TriggerCheck(opts)
	if err != nil {
		respondError(w, errorCode(err), err.Error())
		return
	}

	respondJSON(w, http.StatusAccepted, MediaCheckStartResponse{
		Message: "Media file check started",
		RunID:   runID,
		Check:   "/api/media/files/check/status",
	})
}

func (s *Server) handleMediaFileCheckStatus(w http.ResponseWriter, r *http.Request) {
	respondJSON(w, http.StatusOK, s.service.MediaFileCheck.Status())
}

// parseMediaCheckOptions validates the scope query parameters and builds the
// repository options. It enforces the configured maximum from/to range.
func parseMediaCheckOptions(query url.Values, maxRangeDays int) (*database.MediaCheckOptions, error) {
	opts := &database.MediaCheckOptions{
		BlockID: query.Get("block_id"),
		Date:    query.Get("date"),
		From:    query.Get("from"),
		To:      query.Get("to"),
	}

	if opts.BlockID != "" && !util.GUIDPattern.MatchString(opts.BlockID) {
		return nil, types.NewValidationError("block_id", "must be a valid UUID")
	}

	for field, value := range map[string]string{"date": opts.Date, "from": opts.From, "to": opts.To} {
		if value == "" {
			continue
		}
		if _, err := time.Parse(dateParam, value); err != nil {
			return nil, types.NewValidationError(field, "must be a valid date (YYYY-MM-DD)")
		}
	}

	if err := validateDateRange(opts.From, opts.To, maxRangeDays); err != nil {
		return nil, err
	}

	if limit := query.Get("limit"); limit != "" {
		l, err := strconv.Atoi(limit)
		if err != nil || l < 0 {
			return nil, types.NewValidationError("limit", "must be a non-negative integer")
		}
		opts.Limit = l
	}

	if query.Get("include_voicetracks") == "true" {
		opts.IncludeVoicetracks = true
	}

	return opts, nil
}

// validateDateRange enforces from <= to and a maximum span, when both bounds are
// given. A single bound is left to the database (open-ended on one side).
func validateDateRange(from, to string, maxRangeDays int) error {
	if from == "" || to == "" {
		return nil
	}
	fromDate, err := time.Parse(dateParam, from)
	if err != nil {
		return types.NewValidationError("from", "must be a valid date (YYYY-MM-DD)")
	}
	toDate, err := time.Parse(dateParam, to)
	if err != nil {
		return types.NewValidationError("to", "must be a valid date (YYYY-MM-DD)")
	}
	if toDate.Before(fromDate) {
		return types.NewValidationError("to", "must not be before 'from'")
	}
	if maxRangeDays > 0 {
		// Inclusive day span: a single day is span 1.
		days := int(toDate.Sub(fromDate).Hours()/24) + 1
		if days > maxRangeDays {
			return types.NewValidationError("range",
				"date range exceeds the maximum of "+strconv.Itoa(maxRangeDays)+" days")
		}
	}
	return nil
}
