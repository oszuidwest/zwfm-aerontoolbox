package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/config"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/database"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/image"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// MediaService coordinates artist, track, image, and playlist workflows.
type MediaService struct {
	repo   *database.Repository
	config *config.Config
}

// newMediaService returns a media service using repo and cfg.
func newMediaService(repo *database.Repository, cfg *config.Config) *MediaService {
	return &MediaService{
		repo:   repo,
		config: cfg,
	}
}

// GetArtist returns artist details for id.
func (s *MediaService) GetArtist(ctx context.Context, id string) (*database.ArtistDetails, error) {
	return s.repo.GetArtist(ctx, id)
}

// GetTrack returns track details for id.
func (s *MediaService) GetTrack(ctx context.Context, id string) (*database.TrackDetails, error) {
	return s.repo.GetTrack(ctx, id)
}

// GetImage returns stored artwork for an artist or track.
func (s *MediaService) GetImage(ctx context.Context, entityType types.EntityType, id string) ([]byte, error) {
	table := types.Table(entityType)
	return s.repo.GetImage(ctx, table, id)
}

// DeleteImage clears stored artwork for an artist or track.
func (s *MediaService) DeleteImage(ctx context.Context, entityType types.EntityType, id string) error {
	table := types.Table(entityType)
	return s.repo.DeleteImage(ctx, table, id)
}

// ImageUploadParams selects the target entity and exactly one image source.
type ImageUploadParams struct {
	EntityType types.EntityType
	ID         string
	ImageURL   string
	ImageData  []byte
}

// ImageUploadResult reports the stored entity label and optimization outcome.
type ImageUploadResult struct {
	ArtistName           string
	TrackTitle           string
	OriginalSize         int
	OptimizedSize        int
	SizeReductionPercent float64
}

// UploadImage validates, optimizes, and stores artwork for an artist or track.
func (s *MediaService) UploadImage(ctx context.Context, params *ImageUploadParams) (*ImageUploadResult, error) {
	slog.Debug("Image upload started",
		"entityType", params.EntityType,
		"id", params.ID,
		"hasURL", params.ImageURL != "",
		"hasData", len(params.ImageData) > 0)

	if err := validateImageUploadParams(params); err != nil {
		return nil, err
	}

	var name, title string

	if params.EntityType == types.EntityTypeArtist {
		artist, err := s.repo.GetArtist(ctx, params.ID)
		if err != nil {
			return nil, err
		}
		name = artist.ArtistName
	} else {
		track, err := s.repo.GetTrack(ctx, params.ID)
		if err != nil {
			return nil, err
		}
		name = track.Artist
		title = track.TrackTitle
	}

	var imageData []byte
	var err error
	if params.ImageURL != "" {
		imageData, err = image.DownloadImage(params.ImageURL, s.config.Image.GetMaxDownloadBytes())
		if err != nil {
			slog.Error("Image download failed", "url", params.ImageURL, "error", err)
			return nil, types.NewValidationError("image", fmt.Sprintf("download failed: %v", err))
		}
	} else {
		imageData = params.ImageData
	}

	imgConfig := image.Config{
		TargetWidth:   s.config.Image.TargetWidth,
		TargetHeight:  s.config.Image.TargetHeight,
		Quality:       s.config.Image.Quality,
		RejectSmaller: s.config.Image.RejectSmaller,
	}
	slog.Debug("Image processing started",
		"inputSize", len(imageData),
		"targetWidth", imgConfig.TargetWidth,
		"targetHeight", imgConfig.TargetHeight)
	processingResult, err := image.Process(imageData, imgConfig)
	if err != nil {
		slog.Error("Image processing failed", "error", err)
		return nil, types.NewValidationError("image", fmt.Sprintf("processing failed: %v", err))
	}
	slog.Debug("Image processing completed",
		"originalSize", processingResult.Original.Size,
		"optimizedSize", processingResult.Optimized.Size,
		"savings", processingResult.Savings)

	table := types.Table(params.EntityType)
	if err := s.repo.UpdateImage(ctx, table, params.ID, processingResult.Data); err != nil {
		slog.Error("Image save failed", "entityType", params.EntityType, "id", params.ID, "error", err)
		return nil, err
	}

	return &ImageUploadResult{
		OriginalSize:         processingResult.Original.Size,
		OptimizedSize:        processingResult.Optimized.Size,
		SizeReductionPercent: processingResult.Savings,
		ArtistName:           name,
		TrackTitle:           title,
	}, nil
}

// ImageStats reports image coverage for one entity type.
type ImageStats struct {
	Total         int
	WithImages    int
	WithoutImages int
}

// GetStatistics returns image coverage for artists or tracks.
func (s *MediaService) GetStatistics(ctx context.Context, entityType types.EntityType) (*ImageStats, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}

	counts, err := s.repo.CountImages(ctx, types.Table(entityType))
	if err != nil {
		return nil, err
	}

	return &ImageStats{
		Total:         counts.Total,
		WithImages:    counts.WithImages,
		WithoutImages: counts.Total - counts.WithImages,
	}, nil
}

// DeleteResult reports a bulk artwork deletion.
type DeleteResult struct {
	CountBefore  int
	DeletedCount int64
}

// DeleteAllImages clears all artwork for one entity type.
func (s *MediaService) DeleteAllImages(ctx context.Context, entityType types.EntityType) (*DeleteResult, error) {
	if err := validateEntityType(entityType); err != nil {
		return nil, err
	}

	table := types.Table(entityType)

	count, err := s.repo.CountWithImages(ctx, table)
	if err != nil {
		return nil, err
	}

	if count == 0 {
		return &DeleteResult{CountBefore: count}, nil
	}

	deleted, err := s.repo.DeleteAllImages(ctx, table)
	if err != nil {
		return nil, err
	}

	return &DeleteResult{CountBefore: count, DeletedCount: deleted}, nil
}

// DefaultPlaylistOptions returns the API defaults for playlist queries.
func DefaultPlaylistOptions() database.PlaylistOptions {
	return database.PlaylistOptions{
		ExportTypes: []int{},
		SortBy:      "starttime",
	}
}

// GetPlaylist returns block-scoped playlist items with filtering and pagination.
func (s *MediaService) GetPlaylist(
	ctx context.Context, opts *database.PlaylistOptions,
) ([]database.PlaylistItem, error) {
	return s.repo.GetPlaylist(ctx, opts)
}

// PlaylistBlockWithTracks combines a block with its scheduled items.
type PlaylistBlockWithTracks struct {
	database.PlaylistBlock
	Tracks []database.PlaylistItem `json:"tracks"`
}

// GetPlaylistWithTracks returns date-scoped blocks with their items.
func (s *MediaService) GetPlaylistWithTracks(ctx context.Context, date string) ([]PlaylistBlockWithTracks, error) {
	blocks, tracksByBlock, err := s.repo.GetPlaylistWithTracks(ctx, date)
	if err != nil {
		return nil, err
	}

	result := make([]PlaylistBlockWithTracks, len(blocks))
	for i := range blocks {
		tracks := tracksByBlock[blocks[i].BlockID]
		if tracks == nil {
			tracks = []database.PlaylistItem{}
		}
		result[i] = PlaylistBlockWithTracks{
			PlaylistBlock: blocks[i],
			Tracks:        tracks,
		}
	}

	return result, nil
}

// validateEntityType accepts only artist and track operations.
func validateEntityType(entityType types.EntityType) error {
	if entityType != types.EntityTypeArtist && entityType != types.EntityTypeTrack {
		return types.NewValidationError("entityType",
			fmt.Sprintf("invalid type: use '%s' or '%s'", types.EntityTypeArtist, types.EntityTypeTrack))
	}
	return nil
}

// validateImageUploadParams requires a valid entity and exactly one image source.
func validateImageUploadParams(params *ImageUploadParams) error {
	if err := validateEntityType(params.EntityType); err != nil {
		return err
	}

	hasURL := params.ImageURL != ""
	hasImageData := len(params.ImageData) > 0

	if !hasURL && !hasImageData {
		return types.NewValidationError("image", "image is required")
	}

	if hasURL && hasImageData {
		return types.NewValidationError("image", "use either URL or upload, not both")
	}

	return nil
}
