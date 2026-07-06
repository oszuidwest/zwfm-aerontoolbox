// Package main wires configuration, storage, scheduling, and HTTP serving for
// the Aeron Toolbox API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/api"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/service"
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

// run initializes dependencies and blocks until the server exits or shuts down.
func run() error {
	configFile := flag.String("config", "", "Path to config file (default: config.json)")
	port := flag.String("port", "8080", "API server port (default: 8080)")
	showVersion := flag.Bool("version", false, "Show version information")
	flag.Parse()

	if *showVersion {
		printVersion()
		return nil
	}

	cfg, err := config.Load(*configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Configuration error: %v\n", err)
		return err
	}

	initLogger(cfg)

	db, dbClose, err := setupDatabase(cfg)
	if err != nil {
		return err
	}
	defer dbClose()

	svc, err := service.New(db, cfg)
	if err != nil {
		slog.Error("Service initialization failed", "error", err)
		return err
	}
	defer svc.Close()

	// Start background secret-expiry refresh so health checks do not block.
	svc.Notify.StartExpiryChecker()

	// Create a root context that cancels on SIGINT/SIGTERM.
	// This ties the scheduler's cron context to the application lifecycle,
	// so scheduled jobs receive cancellation on shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler, err := service.NewScheduler(ctx, svc)
	if err != nil {
		slog.Error("Scheduler initialization failed", "error", err)
		return err
	}
	scheduler.Start()

	server := api.New(svc, Version)

	return serveUntilShutdown(server, *port, scheduler, ctx)
}

// printVersion writes build metadata and exits without starting the server.
func printVersion() {
	fmt.Printf("Aeron Toolbox %s (%s)\n", Version, Commit)
	fmt.Printf("Build time: %s\n", BuildTime)
}

// initLogger initializes the global slog logger with the configured level and format.
func initLogger(cfg *config.Config) {
	level := cfg.Log.GetLevel()
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Log.GetFormat() == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
	slog.Info("Logger initialized", "level", level.String(), "format", cfg.Log.GetFormat())
}

// setupDatabase opens the configured PostgreSQL pool and returns its cleanup function.
func setupDatabase(cfg *config.Config) (*sqlx.DB, func(), error) {
	db, err := sqlx.Open("postgres", cfg.Database.ConnectionString())
	if err != nil {
		slog.Error("Database connection failed", "error", err)
		return nil, nil, err
	}

	db.SetMaxOpenConns(cfg.Database.GetMaxOpenConns())
	db.SetMaxIdleConns(cfg.Database.GetMaxIdleConns())
	db.SetConnMaxLifetime(cfg.Database.GetConnMaxLifetime())

	slog.Info("Database connection pool configured",
		"max_open", cfg.Database.GetMaxOpenConns(),
		"max_idle", cfg.Database.GetMaxIdleConns(),
		"max_lifetime", cfg.Database.GetConnMaxLifetime())

	if err := db.Ping(); err != nil {
		slog.Error("Database ping failed", "error", err)
		if closeErr := db.Close(); closeErr != nil {
			slog.Warn("Failed to close database after ping error", "error", closeErr)
		}
		return nil, nil, err
	}

	cleanup := func() {
		if err := db.Close(); err != nil {
			slog.Error("Failed to close database", "error", err)
		}
	}

	return db, cleanup, nil
}

// serveUntilShutdown runs the API server until ctx is cancelled or serving fails.
func serveUntilShutdown(server *api.Server, port string, scheduler *service.Scheduler, ctx context.Context) error {
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("API server started", "port", port)
		if err := server.Start(port); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case <-ctx.Done():
		slog.Info("Shutdown signal received, stopping server...")
	case err := <-serverErr:
		slog.Error("API server error", "error", err)
		return err
	}

	return gracefulShutdown(server, scheduler)
}

// gracefulShutdown drains scheduled jobs before stopping the HTTP server.
func gracefulShutdown(server *api.Server, scheduler *service.Scheduler) error {
	// Without jobs Stop() returns context.Background(), whose Done() never
	// fires - waiting on it would always burn the full timeout.
	if scheduler.HasJobs() {
		select {
		case <-scheduler.Stop().Done():
			slog.Info("Scheduler stopped successfully")
		case <-time.After(35 * time.Second):
			slog.Warn("Scheduler stop timeout, forcing shutdown")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
		return err
	}

	slog.Info("Server stopped successfully")
	return nil
}
