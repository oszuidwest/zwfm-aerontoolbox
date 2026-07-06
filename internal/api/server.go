package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
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

	// SHA-256 hashes of the configured API keys, computed once at construction
	// (config is loaded once at startup and never reloaded). Hashing both sides
	// to a fixed length keeps the comparison constant-time regardless of key
	// length differences.
	apiKeyHashes [][sha256.Size]byte
}

// New returns a Server bound to the service layer and build version.
func New(svc *service.AeronService, version string) *Server {
	keys := svc.Config().API.Keys
	keyHashes := make([][sha256.Size]byte, len(keys))
	for i, key := range keys {
		keyHashes[i] = sha256.Sum256([]byte(key))
	}
	return &Server{
		service:      svc,
		version:      version,
		apiKeyHashes: keyHashes,
	}
}

// Start serves the API on port until shutdown or listener failure.
func (s *Server) Start(port string) error {
	router := s.router()
	s.server = s.newHTTPServer(port, router)

	return s.server.ListenAndServe()
}

func (s *Server) newHTTPServer(port string, handler http.Handler) *http.Server {
	apiCfg := s.service.Config().API
	return &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       apiCfg.GetReadTimeout(),
		WriteTimeout:      apiCfg.GetWriteTimeout(),
		IdleTimeout:       apiCfg.GetIdleTimeout(),
	}
}

// router builds the public health route and authenticated API surface.
func (s *Server) router() http.Handler {
	router := chi.NewRouter()
	backupEnabled := s.requireEnabled("backup is not enabled", func(c *config.Config) bool { return c.Backup.Enabled })

	cop := http.NewCrossOriginProtection()
	router.Use(func(next http.Handler) http.Handler {
		return cop.Handler(next)
	})

	router.Use(middleware.RequestID)
	router.Use(middleware.Recoverer)
	router.Use(middleware.Compress(5))

	// Route HEAD to the matching GET handler so probes that use HEAD (some load
	// balancers, GNU wget --spider) hit /health instead of getting a 405.
	router.Use(middleware.GetHead)

	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		respondError(w, http.StatusNotFound, "Endpoint not found")
	})

	jsonHeader := middleware.SetHeader("Content-Type", "application/json; charset=utf-8")
	router.With(jsonHeader).Get("/health", s.handleHealth)

	router.Route("/api", func(r chi.Router) {
		r.Use(jsonHeader)
		if limiter := newAPIRateLimiter(&s.service.Config().API); limiter != nil {
			r.Use(s.rateLimitMiddleware(limiter))
		}

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			respondError(w, http.StatusNotFound, "Endpoint not found")
		})

		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Get("/health", s.handleHealthDetails)

			s.setupEntityRoutes(r, "/artists", types.EntityTypeArtist)
			s.setupEntityRoutes(r, "/tracks", types.EntityTypeTrack)

			r.Group(func(r chi.Router) {
				r.Use(middleware.Timeout(s.service.Config().API.GetRequestTimeout()))

				r.Get("/playlist", s.handlePlaylist)

				r.Route("/db", func(r chi.Router) {
					r.Route("/maintenance", func(r chi.Router) {
						r.Get("/health", s.handleDatabaseHealth)
					})

					r.Group(func(r chi.Router) {
						r.Use(backupEnabled)
						r.Post("/backup", s.handleCreateBackup)
						r.Get("/backup/status", s.handleBackupStatus)
						r.Get("/backups", s.handleListBackups)
						r.Get("/backups/{filename}/validate", s.handleValidateBackup)
						r.Delete("/backups/{filename}", s.handleDeleteBackup)
					})
				})

				r.Group(func(r chi.Router) {
					r.Use(s.requireEnabled("file monitor is not enabled", func(c *config.Config) bool {
						return c.FileMonitor.Enabled
					}))
					r.Get("/file-monitor/status", s.handleFileMonitorStatus)
					r.Post("/file-monitor/check", s.handleFileMonitorCheck)
				})

				r.Group(func(r chi.Router) {
					r.Use(s.requireEnabled("media file check is not enabled", func(c *config.Config) bool {
						return c.MediaFileCheck.Enabled
					}))
					r.Post("/media/files/check", s.handleMediaFileCheck)
					r.Get("/media/files/check/status", s.handleMediaFileCheckStatus)
				})

				r.Post("/notifications/test-email", s.handleTestEmail)
			})

			// The download route escapes the group-level request timeout:
			// large backup downloads may legitimately exceed it.
			r.Group(func(r chi.Router) {
				r.Use(backupEnabled)
				r.Get("/db/backups/{filename}", s.handleDownloadBackupFile)
			})
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
	apiCfg := s.service.Config().API
	requestTimeout := middleware.Timeout(apiCfg.GetRequestTimeout())
	uploadTimeout := middleware.Timeout(apiCfg.GetUploadReadTimeout() + apiCfg.GetRequestTimeout())

	r.Route(path, func(r chi.Router) {
		r.With(requestTimeout).Get("/", s.handleStats(entityType))
		r.With(requestTimeout).Delete("/bulk-delete", s.handleBulkDelete(entityType))

		r.Route("/{id}", func(r chi.Router) {
			r.With(requestTimeout).Get("/", s.handleEntityByID(entityType))
			r.Route("/image", func(r chi.Router) {
				r.With(requestTimeout).Get("/", s.handleGetImage(entityType))
				r.With(uploadTimeout).Post("/", s.handleImageUpload(entityType))
				r.With(requestTimeout).Delete("/", s.handleDeleteImage(entityType))
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

// requireEnabled builds middleware that 404s when a feature is disabled in the
// configuration, so a whole route group can be gated in one place.
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
	if key == "" {
		return false
	}
	return s.isValidAPIKeyHash(sha256.Sum256([]byte(key)))
}

// isValidAPIKeyHash compares a presented key's hash against every configured
// key hash in constant time.
func (s *Server) isValidAPIKeyHash(keyHash [sha256.Size]byte) bool {
	valid := 0
	for i := range s.apiKeyHashes {
		valid |= subtle.ConstantTimeCompare(keyHash[:], s.apiKeyHashes[i][:])
	}
	return valid == 1
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
