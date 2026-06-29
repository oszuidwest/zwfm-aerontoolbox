package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Server owns the HTTP routing, middleware, and listener lifecycle.
type Server struct {
	service *service.AeronService
	version string
	server  *http.Server
}

// New returns a Server bound to the service layer and build version.
func New(svc *service.AeronService, version string) *Server {
	return &Server{
		service: svc,
		version: version,
	}
}

// Start serves the API on port until shutdown or listener failure.
func (s *Server) Start(port string) error {
	router := s.router()

	s.server = &http.Server{
		Addr:              ":" + port,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	return s.server.ListenAndServe()
}

// router builds the public health route and authenticated API surface.
func (s *Server) router() http.Handler {
	router := chi.NewRouter()

	cop := http.NewCrossOriginProtection()
	router.Use(func(next http.Handler) http.Handler {
		return cop.Handler(next)
	})

	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
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

		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)
			r.Use(middleware.Timeout(s.service.Config().API.GetRequestTimeout()))

			s.setupEntityRoutes(r, "/artists", types.EntityTypeArtist)
			s.setupEntityRoutes(r, "/tracks", types.EntityTypeTrack)

			r.Get("/playlist", s.handlePlaylist)

			r.Route("/db", func(r chi.Router) {
				r.Route("/maintenance", func(r chi.Router) {
					r.Get("/health", s.handleDatabaseHealth)
				})

				r.Group(func(r chi.Router) {
					r.Use(s.requireEnabled("backup is not enabled", func(c *config.Config) bool { return c.Backup.Enabled }))
					r.Post("/backup", s.handleCreateBackup)
					r.Get("/backup/status", s.handleBackupStatus)
					r.Get("/backups", s.handleListBackups)
					r.Get("/backups/{filename}", s.handleDownloadBackupFile)
					r.Get("/backups/{filename}/validate", s.handleValidateBackup)
					r.Delete("/backups/{filename}", s.handleDeleteBackup)
				})
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requireEnabled("file monitor is not enabled", func(c *config.Config) bool { return c.FileMonitor.Enabled }))
				r.Get("/file-monitor/status", s.handleFileMonitorStatus)
				r.Post("/file-monitor/check", s.handleFileMonitorCheck)
			})

			r.Group(func(r chi.Router) {
				r.Use(s.requireEnabled("media file check is not enabled", func(c *config.Config) bool { return c.MediaFileCheck.Enabled }))
				r.Post("/media/files/check", s.handleMediaFileCheck)
				r.Get("/media/files/check/status", s.handleMediaFileCheckStatus)
			})

			r.Post("/notifications/test-email", s.handleTestEmail)
		})
	})

	return router
}

// Shutdown gracefully stops the active HTTP server. It is a no-op before Start.
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
			//nolint:gosec // G706: structured auth-failure audit log; remote_addr is the direct peer.
			slog.Warn("Authentication failed",
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

// requireEnabled builds middleware that 404s when a feature is disabled. The
// enabled predicate is evaluated per request against the live config, so a
// runtime config change is reflected without rebuilding the router.
func (s *Server) requireEnabled(message string, enabled func(*config.Config) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled(s.service.Config()) {
				respondError(w, http.StatusNotFound, message)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) isValidAPIKey(key string) bool {
	return key != "" && slices.Contains(s.service.Config().API.Keys, key)
}

func parseQueryBoolParam(value string) *bool {
	switch value {
	case "yes", "true", "1":
		v := true
		return &v
	case "no", "false", "0":
		v := false
		return &v
	default:
		return nil
	}
}
