// Package api provides the HTTP API server for the Aeron radio automation system.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/util"
)

// PlaylistBlockWithTracks is an alias for service.PlaylistBlockWithTracks for API responses.
type PlaylistBlockWithTracks = service.PlaylistBlockWithTracks

// ImageUploadRequest represents the JSON request body for image upload operations.
type ImageUploadRequest struct {
	URL   string `json:"url"`
	Image string `json:"image"`
}

// ImageStatsResponse represents the response format for statistics endpoints.
type ImageStatsResponse struct {
	Total         int `json:"total"`
	WithImages    int `json:"with_images"`
	WithoutImages int `json:"without_images"`
}

// HealthResponse represents the response for the health check endpoint.
type HealthResponse struct {
	Status         string              `json:"status"`
	Version        string              `json:"version"`
	Database       string              `json:"database"`
	DatabaseStatus string              `json:"database_status"`
	Notifications  *NotificationHealth `json:"notifications,omitempty"`
}

// NotificationHealth represents notification system status in the health response.
type NotificationHealth struct {
	Configured   bool                     `json:"configured"`
	LastError    string                   `json:"last_error,omitempty"`
	LastErrorAt  *time.Time               `json:"last_error_at,omitempty"`
	SecretExpiry *notify.SecretExpiryInfo `json:"secret_expiry,omitempty"`
}

// ImageUploadResponse represents the response for image upload operations.
type ImageUploadResponse struct {
	Artist               string  `json:"artist"`
	Track                string  `json:"track,omitzero"`
	OriginalSize         int     `json:"original_size"`
	OptimizedSize        int     `json:"optimized_size"`
	SizeReductionPercent float64 `json:"savings_percent"`
}

// BulkDeleteResponse represents the response for bulk delete operations.
type BulkDeleteResponse struct {
	Deleted int64  `json:"deleted"`
	Message string `json:"message"`
}

// ImageDeleteResponse represents the response for image delete operations.
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
	dbStatus := "connected"
	if err := s.service.Repository().Ping(r.Context()); err != nil {
		dbStatus = "disconnected"
		slog.Warn("Database health check failed", "error", err)
	}

	overallStatus := "healthy"

	resp := HealthResponse{
		Version:        s.version,
		Database:       s.service.Config().Database.Name,
		DatabaseStatus: dbStatus,
	}

	// Add notification health info
	emailCfg := &s.service.Config().Notifications.Email
	configured := notify.IsConfigured(emailCfg)
	nh := &NotificationHealth{Configured: configured}

	if configured {
		lastErr, lastErrAt := s.service.Notify.LastError()
		nh.LastError = lastErr
		nh.LastErrorAt = lastErrAt

		if expiry := s.service.Notify.SecretExpiry(); expiry != nil {
			nh.SecretExpiry = expiry
			if expiry.ExpiresSoon {
				overallStatus = "degraded"
			}
		}
	}
	resp.Notifications = nh

	resp.Status = overallStatus
	respondJSON(w, http.StatusOK, resp)
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
		} else {
			track, err := s.service.Media.GetTrack(r.Context(), entityID)
			if err != nil {
				statusCode := errorCode(err)
				respondError(w, statusCode, err.Error())
				return
			}
			respondJSON(w, http.StatusOK, track)
		}
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

		label := string(entityType)

		message := strconv.FormatInt(result.DeletedCount, 10) + " " + label + " images deleted"
		respondJSON(w, http.StatusOK, BulkDeleteResponse{
			Deleted: result.DeletedCount,
			Message: message,
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
		w.Header().Set("Content-Type", detectImageContentType(imageData))
		w.Header().Set("Content-Length", strconv.Itoa(len(imageData)))

		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(imageData); err != nil { //nolint:gosec // G705: imageData is from internal storage, content type is detected and set explicitly
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
			slog.Error("Failed to delete image", "entityType", entityType, "id", entityID, "error", err) //nolint:gosec // G706: entityID is validated, logged as structured slog value
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

func parsePlaylistOptions(query url.Values) service.PlaylistOptions {
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

	// Single block with items
	if query.Get("block_id") != "" {
		opts := parsePlaylistOptions(query)
		playlist, err := s.service.Media.GetPlaylist(r.Context(), &opts)
		if err != nil {
			slog.Error("Failed to retrieve playlist", "block_id", opts.BlockID, "error", err) //nolint:gosec // G706: block_id is logged as a structured slog value
			respondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		respondJSON(w, http.StatusOK, playlist)
		return
	}

	// All blocks with tracks for a date
	date := query.Get("date")
	result, err := s.service.Media.GetPlaylistWithTracks(r.Context(), date)
	if err != nil {
		slog.Error("Failed to retrieve playlist with tracks", "date", date, "error", err) //nolint:gosec // G706: date is logged as a structured slog value
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

	client, err := notify.NewGraphClient(emailCfg)
	if err != nil {
		respondError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to create Graph client: %v", err))
		return
	}

	if err := client.ValidateAuth(); err != nil {
		respondError(w, http.StatusBadGateway, fmt.Sprintf("Authentication failed: %v", err))
		return
	}

	if err := s.service.Notify.SendTestEmail(r.Context()); err != nil {
		respondError(w, http.StatusBadGateway, fmt.Sprintf("Failed to send test email: %v", err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Test e-mail succesvol verzonden",
	})
}
