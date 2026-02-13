package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Server represents the HTTP API server for the Aeron radio automation system.
type Server struct {
	service *service.AeronService
	version string
	server  *http.Server
}

// New creates a new Server instance.
func New(svc *service.AeronService, version string) *Server {
	return &Server{
		service: svc,
		version: version,
	}
}

// Start initializes and starts the HTTP server on the specified port.
func (s *Server) Start(port string) error {
	router := chi.NewRouter()

	cop := http.NewCrossOriginProtection()
	router.Use(func(next http.Handler) http.Handler {
		return cop.Handler(next)
	})

	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.RealIP)
	router.Use(middleware.Compress(5))

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		respondError(w, http.StatusNotFound, "Endpoint not found")
	})

	router.Route("/api", func(r chi.Router) {
		r.Use(middleware.SetHeader("Content-Type", "application/json; charset=utf-8"))

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			respondError(w, http.StatusNotFound, "Endpoint not found")
		})

		r.Get("/health", s.handleHealth)

		// Routes with standard request timeout
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Use(middleware.Timeout(s.service.Config().API.GetRequestTimeout()))

			s.setupEntityRoutes(r, "/artists", types.EntityTypeArtist)
			s.setupEntityRoutes(r, "/tracks", types.EntityTypeTrack)

			r.Get("/playlist", s.handlePlaylist)

			r.Route("/db", func(r chi.Router) {
				// Maintenance endpoints (async)
				r.Route("/maintenance", func(r chi.Router) {
					r.Get("/health", s.handleDatabaseHealth)
					r.Post("/vacuum", s.handleVacuum)
					r.Post("/analyze", s.handleAnalyze)
					r.Get("/status", s.handleMaintenanceStatus)
				})

				// Backup endpoints
				r.Get("/backups", s.handleListBackups)
				r.Get("/backup/status", s.handleBackupStatus)
				r.Get("/backups/{filename}/validate", s.handleValidateBackup)
				r.Delete("/backups/{filename}", s.handleDeleteBackup)
			})
		})

		// Notification endpoints
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Use(middleware.Timeout(s.service.Config().API.GetRequestTimeout()))

			r.Post("/notifications/test-email", s.handleTestEmail)
		})

		// Backup routes - no special timeout needed
		// POST /backup returns immediately (async), downloads are served via http.ServeFile
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Use(middleware.Timeout(s.service.Config().API.GetRequestTimeout()))

			r.Post("/db/backup", s.handleCreateBackup)
			r.Get("/db/backups/{filename}", s.handleDownloadBackupFile)
		})
	})

	s.server = &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *Server) setupEntityRoutes(r chi.Router, path string, entityType types.EntityType) {
	r.Route(path, func(r chi.Router) {
		r.Get("/", s.handleStats(entityType))
		r.Delete("/bulk-delete", s.handleBulkDelete(entityType))

		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", s.handleEntityByID(entityType))
			r.Route("/image", func(r chi.Router) {
				r.Get("/", s.handleGetImage(entityType))
				r.Post("/", s.handleImageUpload(entityType))
				r.Delete("/", s.handleDeleteImage(entityType))
			})
		})
	})
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.service.Config()
		if !cfg.API.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		apiKey := r.Header.Get("X-API-Key")

		if !s.isValidAPIKey(apiKey) {
			slog.Warn("Authentication failed", //nolint:gosec // G706: logged as structured slog value for security auditing
				"reason", "invalid_api_key",
				"path", r.URL.Path,
				"method", r.Method,
				"remote_addr", r.RemoteAddr)

			respondError(w, http.StatusUnauthorized, "Unauthorized: invalid or missing API key")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) isValidAPIKey(key string) bool {
	return key != "" && slices.Contains(s.service.Config().API.Keys, key)
}

func detectImageContentType(data []byte) string {
	return http.DetectContentType(data)
}

func parseQueryBoolParam(value string) *bool {
	switch value {
	case "yes", "true", "1":
		val := true
		return &val
	case "no", "false", "0":
		val := false
		return &val
	}
	return nil
}
