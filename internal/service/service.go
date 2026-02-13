// Package service provides business logic for the Aeron Toolbox.
package service

import (
	"encoding/base64"
	"io"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/notify"
)

// AeronService is the main service that provides access to all sub-services.
type AeronService struct {
	Media       *MediaService
	Backup      *BackupService
	Maintenance *MaintenanceService
	Notify      *notify.NotificationService

	repo   *database.Repository
	config *config.Config
}

// New creates a new AeronService instance with all sub-services.
func New(db *sqlx.DB, cfg *config.Config) (*AeronService, error) {
	repo := database.NewRepository(db, cfg.Database.Schema)
	notifySvc := notify.New(cfg)

	backupSvc, err := newBackupService(repo, cfg, notifySvc)
	if err != nil {
		return nil, err
	}

	return &AeronService{
		Media:       newMediaService(repo, cfg),
		Backup:      backupSvc,
		Maintenance: newMaintenanceService(repo, cfg, notifySvc),
		Notify:      notifySvc,
		repo:        repo,
		config:      cfg,
	}, nil
}

// Config returns the service configuration.
func (s *AeronService) Config() *config.Config {
	return s.config
}

// Repository returns the database repository.
func (s *AeronService) Repository() *database.Repository {
	return s.repo
}

// Close gracefully shuts down all services.
func (s *AeronService) Close() {
	s.Maintenance.Close()
	s.Backup.Close()
	s.Notify.Close()
}

// DecodeBase64 decodes a base64 string, stripping any data URL prefix if present.
func DecodeBase64(data string) ([]byte, error) {
	if _, after, found := strings.Cut(data, ","); found {
		data = after
	}
	return io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
}
