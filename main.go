// Package main implements the Aeron Toolbox API server.
//
// This server provides an unofficial REST API for the Aeron radio automation system.
// It offers image management, database browsing, database maintenance, and backup
// functionality through direct database access.
//
// The API server can be configured via JSON configuration file and supports
// optional API key authentication for secure access.
package main

import (
	"context"
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

// run executes the server lifecycle from initialization through graceful shutdown.
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

	scheduler, err := service.NewScheduler(context.Background(), svc)
	if err != nil {
		slog.Error("Scheduler initialization failed", "error", err)
		return err
	}
	scheduler.Start()

	server := api.New(svc, Version)

	return serveUntilShutdown(server, *port, scheduler)
}

// printVersion prints the application version, commit hash, and build time.
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

// setupDatabase establishes a database connection pool and returns a cleanup function.
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

// serveUntilShutdown runs the API server until a shutdown signal or error occurs.
func serveUntilShutdown(server *api.Server, port string, scheduler *service.Scheduler) error {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("API server started", "port", port)
		if err := server.Start(port); err != nil && err != http.ErrServerClosed {
			serverErr <- err
		}
	}()

	select {
	case <-stop:
		slog.Info("Shutdown signal received, stopping server...")
	case err := <-serverErr:
		slog.Error("API server error", "error", err)
		return err
	}

	return gracefulShutdown(server, scheduler)
}

// gracefulShutdown performs orderly shutdown of the scheduler and server.
func gracefulShutdown(server *api.Server, scheduler *service.Scheduler) error {
	// Stop scheduler (handles both backup and maintenance jobs)
	ctx := scheduler.Stop()
	select {
	case <-ctx.Done():
		if scheduler.HasJobs() {
			slog.Info("Scheduler stopped successfully")
		}
	case <-time.After(35 * time.Second):
		slog.Warn("Scheduler stop timeout, forcing shutdown")
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
