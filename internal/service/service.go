// Package service coordinates media, backup, monitoring, and notification workflows.
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

// AeronService owns the application sub-services and shared dependencies.
type AeronService struct {
	Media          *MediaService
	Backup         *BackupService
	Maintenance    *MaintenanceService
	FileMonitor    *FileMonitorService
	MediaFileCheck *MediaFileCheckService
	Notify         *notify.NotificationService

	repo   *database.Repository
	config *config.Config
}

// New wires the service layer around db and cfg.
func New(db *sqlx.DB, cfg *config.Config) (*AeronService, error) {
	repo := database.NewRepository(db, cfg.Database.Schema)
	notifySvc := notify.New(cfg)

	backupSvc, err := newBackupService(repo, cfg, notifySvc)
	if err != nil {
		return nil, err
	}

	fileMonitorSvc, err := newFileMonitorService(cfg, notifySvc)
	if err != nil {
		return nil, err
	}

	return &AeronService{
		Media:          newMediaService(repo, cfg),
		Backup:         backupSvc,
		Maintenance:    newMaintenanceService(repo, cfg, notifySvc),
		FileMonitor:    fileMonitorSvc,
		MediaFileCheck: newMediaFileCheckService(repo, cfg, notifySvc),
		Notify:         notifySvc,
		repo:           repo,
		config:         cfg,
	}, nil
}

// Config returns the shared configuration.
func (s *AeronService) Config() *config.Config {
	return s.config
}

// Repository returns the shared database repository.
func (s *AeronService) Repository() *database.Repository {
	return s.repo
}

// Close drains background work owned by sub-services.
func (s *AeronService) Close() {
	s.Backup.Close()
	s.FileMonitor.Close()
	s.MediaFileCheck.Close()
	s.Notify.Close()
}

// DecodeBase64 decodes raw base64 or a data URL payload.
func DecodeBase64(data string) ([]byte, error) {
	if _, after, found := strings.Cut(data, ","); found {
		data = after
	}
	return io.ReadAll(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
}
