// Package api serves the Aeron Toolbox HTTP API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// ImageUploadRequest accepts either an image URL or base64-encoded image data.
type ImageUploadRequest struct {
	URL   string `json:"url"`
	Image string `json:"image"`
}

// ImageStatsResponse reports image coverage for artists or tracks.
type ImageStatsResponse struct {
	Total         int `json:"total"`
	WithImages    int `json:"with_images"`
	WithoutImages int `json:"without_images"`
}

// PublicHealthResponse is returned by the unauthenticated health endpoint.
type PublicHealthResponse struct {
	Status         string `json:"status"`
	Version        string `json:"version"`
	DatabaseStatus string `json:"database_status"`
}

var dbPing = func(ctx context.Context, repo *database.Repository) error {
	return repo.Ping(ctx)
}

// HealthResponse is returned by the authenticated detailed health endpoint.
type HealthResponse struct {
	Status         string                `json:"status"`
	Version        string                `json:"version"`
	Database       string                `json:"database"`
	DatabaseStatus string                `json:"database_status"`
	Notifications  *NotificationHealth   `json:"notifications"`
	FileMonitor    *FileMonitorHealth    `json:"file_monitor,omitempty"`
	MediaFileCheck *MediaFileCheckHealth `json:"media_file_check,omitempty"`
}

// MediaFileCheckHealth summarizes scheduled media-file check health.
// Problems is the number of missing, ambiguous and errored items in the most
// recent completed run. It only drives the overall degraded status when the
// scheduled check is enabled, so ad-hoc API runs with arbitrary scope do not
// flip the system's health.
type MediaFileCheckHealth struct {
	Enabled  bool `json:"enabled"`
	Problems int  `json:"problems"`
}

// FileMonitorHealth summarizes file monitor health.
//
// ChecksStale is the raw count from the most recent run (includes stale
// files outside their configured ActiveWindow) and is preserved for
// transparency. ChecksAlerting is window-aware - outside-window stales are
// excluded - and is what drives the overall degraded status.
type FileMonitorHealth struct {
	Enabled        bool `json:"enabled"`
	ChecksTotal    int  `json:"checks_total"`
	ChecksStale    int  `json:"checks_stale"`
	ChecksAlerting int  `json:"checks_alerting"`
}

// NotificationHealth summarizes notification configuration and recent failures.
type NotificationHealth struct {
	Configured   bool                     `json:"configured"`
	LastError    string                   `json:"last_error,omitempty"`
	LastErrorAt  *time.Time               `json:"last_error_at,omitempty"`
	SecretExpiry *notify.SecretExpiryInfo `json:"secret_expiry,omitempty"`
}

// ImageUploadResponse reports the stored entity label and optimization result.
type ImageUploadResponse struct {
	Artist               string  `json:"artist"`
	Track                string  `json:"track,omitzero"`
	OriginalSize         int     `json:"original_size"`
	OptimizedSize        int     `json:"optimized_size"`
	SizeReductionPercent float64 `json:"savings_percent"`
}

// BulkDeleteResponse reports how many images were removed.
type BulkDeleteResponse struct {
	Deleted int64  `json:"deleted"`
	Message string `json:"message"`
}

// ImageDeleteResponse identifies the entity whose image was removed.
type ImageDeleteResponse struct {
	Message  string `json:"message"`
	ArtistID string `json:"artist_id,omitzero"`
	TrackID  string `json:"track_id,omitzero"`
}

// validateAndGetEntityID extracts and validates the entity ID from the request URL.
func (s *Server) validateAndGetEntityID(w http.ResponseWriter, r *http.Request, entityType types.EntityType) string {
	entityID := chi.URLParam(r, "id")
	if err := util.ValidateEntityID(entityID, string(entityType)); err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return ""
	}
	return entityID
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	dbStatus, dbConnected := s.databaseStatus(r.Context())

	statusCode := http.StatusOK
	if !dbConnected {
		statusCode = http.StatusServiceUnavailable
	}

	respondJSON(w, statusCode, PublicHealthResponse{
		Status:         overallHealthStatus(dbConnected, false),
		Version:        s.version,
		DatabaseStatus: dbStatus,
	})
}

func (s *Server) handleHealthDetails(w http.ResponseWriter, r *http.Request) {
	dbStatus, dbConnected := s.databaseStatus(r.Context())

	resp := HealthResponse{
		Version:        s.version,
		Database:       s.service.Config().Database.Name,
		DatabaseStatus: dbStatus,
	}

	emailCfg := &s.service.Config().Notifications.Email
	configured := notify.IsConfigured(emailCfg)
	nh := &NotificationHealth{Configured: configured}

	var notifyExpiresSoon bool
	if configured {
		lastErr, lastErrAt := s.service.Notify.LastError()
		nh.LastError = lastErr
		nh.LastErrorAt = lastErrAt

		if expiry := s.service.Notify.SecretExpiry(); expiry != nil {
			nh.SecretExpiry = expiry
			notifyExpiresSoon = expiry.ExpiresSoon
		}
	}
	resp.Notifications = nh

	var fmAlerting int
	fmCfg := s.service.Config().FileMonitor
	if fmCfg.Enabled {
		fmAlerting = s.service.FileMonitor.AlertingCount()
		resp.FileMonitor = &FileMonitorHealth{
			Enabled:        true,
			ChecksTotal:    len(fmCfg.Checks),
			ChecksStale:    s.service.FileMonitor.StaleCount(),
			ChecksAlerting: fmAlerting,
		}
	}

	var mediaProblems int
	mfcCfg := s.service.Config().MediaFileCheck
	if mfcCfg.Enabled {
		problems := s.service.MediaFileCheck.ProblemCount()
		resp.MediaFileCheck = &MediaFileCheckHealth{Enabled: true, Problems: problems}
		if mfcCfg.Scheduler.Enabled {
			mediaProblems = problems
		}
	}

	resp.Status = overallHealthStatus(dbConnected, notifyExpiresSoon || fmAlerting > 0 || mediaProblems > 0)
	respondJSON(w, http.StatusOK, resp)
}

func (s *Server) databaseStatus(ctx context.Context) (string, bool) {
	dbStatus := "connected"
	dbConnected := true
	if err := dbPing(ctx, s.service.Repository()); err != nil {
		dbStatus = "disconnected"
		dbConnected = false
		slog.Warn("Database health check failed", "error", err)
	}
	return dbStatus, dbConnected
}

// overallHealthStatus returns the highest-severity status given the database
// connectivity and whether any component reported a degraded signal. Callers OR
// their per-component signals into degraded, so adding a component never changes
// this signature. Severity order: "unhealthy" > "degraded" > "healthy".
func overallHealthStatus(dbConnected, degraded bool) string {
	switch {
	case !dbConnected:
		return "unhealthy"
	case degraded:
		return "degraded"
	default:
		return "healthy"
	}
}

func (s *Server) handleStats(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stats, err := s.service.Media.GetStatistics(r.Context(), entityType)
		if err != nil {
			slog.Error("Failed to retrieve statistics", "entityType", entityType, "error", err)
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		response := ImageStatsResponse{
			Total:         stats.Total,
			WithImages:    stats.WithImages,
			WithoutImages: stats.WithoutImages,
		}

		respondJSON(w, http.StatusOK, response)
	}
}

func (s *Server) handleEntityByID(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID := s.validateAndGetEntityID(w, r, entityType)
		if entityID == "" {
			return
		}

		if entityType == types.EntityTypeArtist {
			artist, err := s.service.Media.GetArtist(r.Context(), entityID)
			if err != nil {
				statusCode := errorCode(err)
				respondError(w, statusCode, err.Error())
				return
			}
			respondJSON(w, http.StatusOK, artist)
			return
		}

		track, err := s.service.Media.GetTrack(r.Context(), entityID)
		if err != nil {
			statusCode := errorCode(err)
			respondError(w, statusCode, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, track)
	}
}

func (s *Server) uploadResponse(result *service.ImageUploadResult, entityType types.EntityType) ImageUploadResponse {
	response := ImageUploadResponse{
		Artist:               result.ArtistName,
		OriginalSize:         result.OriginalSize,
		OptimizedSize:        result.OptimizedSize,
		SizeReductionPercent: result.SizeReductionPercent,
	}

	if entityType == types.EntityTypeTrack {
		response.Track = result.TrackTitle
	}

	return response
}

func (s *Server) handleBulkDelete(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const confirmHeader = "X-Confirm-Bulk-Delete"
		const confirmValue = "DELETE ALL"

		if r.Header.Get(confirmHeader) != confirmValue {
			respondError(w, http.StatusBadRequest, "Missing confirmation header: "+confirmHeader)
			return
		}

		result, err := s.service.Media.DeleteAllImages(r.Context(), entityType)
		if err != nil {
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}

		respondJSON(w, http.StatusOK, BulkDeleteResponse{
			Deleted: result.DeletedCount,
			Message: fmt.Sprintf("%d %s images deleted", result.DeletedCount, entityType),
		})
	}
}

func (s *Server) handleGetImage(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID := s.validateAndGetEntityID(w, r, entityType)
		if entityID == "" {
			return
		}

		imageData, err := s.service.Media.GetImage(r.Context(), entityType, entityID)
		if err != nil {
			statusCode := errorCode(err)
			respondError(w, statusCode, err.Error())
			return
		}

		w.Header().Del("Content-Type")
		w.Header().Set("Content-Type", http.DetectContentType(imageData))
		w.Header().Set("Content-Length", strconv.Itoa(len(imageData)))

		w.WriteHeader(http.StatusOK)
		//nolint:gosec // G705: imageData is from internal storage, content type is detected and set explicitly
		if _, err := w.Write(imageData); err != nil {
			slog.Debug("Failed to write image to client", "error", err)
		}
	}
}

func (s *Server) handleImageUpload(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID := s.validateAndGetEntityID(w, r, entityType)
		if entityID == "" {
			return
		}

		var req ImageUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			respondError(w, http.StatusBadRequest, "Invalid request content")
			return
		}

		params := &service.ImageUploadParams{
			EntityType: entityType,
			ID:         entityID,
			ImageURL:   req.URL,
		}

		if req.Image != "" {
			imageData, err := service.DecodeBase64(req.Image)
			if err != nil {
				respondError(w, http.StatusBadRequest, "Invalid base64 image")
				return
			}
			params.ImageData = imageData
		}

		result, err := s.service.Media.UploadImage(r.Context(), params)
		if err != nil {
			statusCode := errorCode(err)
			respondError(w, statusCode, err.Error())
			return
		}

		response := s.uploadResponse(result, entityType)
		respondJSON(w, http.StatusOK, response)
	}
}

func (s *Server) handleDeleteImage(entityType types.EntityType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entityID := s.validateAndGetEntityID(w, r, entityType)
		if entityID == "" {
			return
		}

		err := s.service.Media.DeleteImage(r.Context(), entityType, entityID)
		if err != nil {
			//nolint:gosec // G706: entityID is validated, logged as structured slog value
			slog.Error("Failed to delete image",
				"entityType", entityType,
				"id", entityID,
				"error", err)
			respondError(w, errorCode(err), err.Error())
			return
		}

		response := ImageDeleteResponse{
			Message: string(entityType) + " image deleted successfully",
		}
		if entityType == types.EntityTypeArtist {
			response.ArtistID = entityID
		} else {
			response.TrackID = entityID
		}

		respondJSON(w, http.StatusOK, response)
	}
}

func parsePlaylistOptions(query url.Values) database.PlaylistOptions {
	opts := service.DefaultPlaylistOptions()
	opts.BlockID = query.Get("block_id")

	if limit := query.Get("limit"); limit != "" {
		if l, err := strconv.Atoi(limit); err == nil && l > 0 {
			opts.Limit = l
		}
	}
	if offset := query.Get("offset"); offset != "" {
		if o, err := strconv.Atoi(offset); err == nil && o >= 0 {
			opts.Offset = o
		}
	}

	if trackImage := query.Get("track_image"); trackImage != "" {
		opts.TrackImage = parseQueryBoolParam(trackImage)
	}
	if artistImage := query.Get("artist_image"); artistImage != "" {
		opts.ArtistImage = parseQueryBoolParam(artistImage)
	}

	if sort := query.Get("sort"); sort != "" {
		opts.SortBy = sort
	}
	if query.Get("desc") == "true" {
		opts.SortDesc = true
	}

	return opts
}

func (s *Server) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	if query.Get("block_id") != "" {
		opts := parsePlaylistOptions(query)
		playlist, err := s.service.Media.GetPlaylist(r.Context(), &opts)
		if err != nil {
			//nolint:gosec // G706: block_id is logged as a structured slog value
			slog.Error("Failed to retrieve playlist",
				"block_id", opts.BlockID,
				"error", err)
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, playlist)
		return
	}

	date := query.Get("date")
	result, err := s.service.Media.GetPlaylistWithTracks(r.Context(), date)
	if err != nil {
		//nolint:gosec // G706: date is logged as a structured slog value
		slog.Error("Failed to retrieve playlist with tracks",
			"date", date,
			"error", err)
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, result)
}

func (s *Server) handleTestEmail(w http.ResponseWriter, r *http.Request) {
	emailCfg := &s.service.Config().Notifications.Email
	if err := notify.ValidateConfig(emailCfg); err != nil {
		respondError(w, http.StatusBadRequest, fmt.Sprintf("Notification configuration invalid: %v", err))
		return
	}

	if err := s.service.Notify.ValidateAuth(); err != nil {
		respondError(w, http.StatusBadGateway, fmt.Sprintf("Authentication failed: %v", err))
		return
	}

	if err := s.service.Notify.SendTestEmail(r.Context()); err != nil {
		respondError(w, http.StatusBadGateway, fmt.Sprintf("Failed to send test email: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Test email sent successfully",
	})
}
