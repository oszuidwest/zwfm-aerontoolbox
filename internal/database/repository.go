// Package database provides PostgreSQL data access for the Aeron database.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// Repository provides data access methods for the Aeron database.
type Repository struct {
	db     *sqlx.DB
	schema string
}

// NewRepository returns a Repository for accessing the specified schema.
func NewRepository(db *sqlx.DB, schema string) *Repository {
	return &Repository{db: db, schema: schema}
}

// DB returns the underlying database connection.
func (r *Repository) DB() *sqlx.DB {
	return r.db
}

// Schema returns the PostgreSQL schema name.
func (r *Repository) Schema() string {
	return r.schema
}

// Ping verifies the database connection is alive.
func (r *Repository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// resolveTable returns the qualified table name, label, and ID column for the given table.
func (r *Repository) resolveTable(table types.Table) (qualifiedName, label, idCol string, err error) {
	qualifiedName, err = types.QualifiedTable(r.schema, table)
	if err != nil {
		return "", "", "", types.NewValidationError("table", fmt.Sprintf("invalid table configuration: %v", err))
	}
	return qualifiedName, string(table), types.IDColumnForTable(table), nil
}

// Artist operations.

// GetArtist retrieves complete artist details by UUID.
func (r *Repository) GetArtist(ctx context.Context, id string) (*ArtistDetails, error) {
	slog.Debug("Entity lookup", "type", "artist", "id", id)
	query := fmt.Sprintf(artistDetailsQuery, r.schema)
	return getEntityByID[ArtistDetails](ctx, r.db, query, id, "artist", "fetch artist")
}

// Track operations.

// GetTrack retrieves complete track details by UUID.
func (r *Repository) GetTrack(ctx context.Context, id string) (*TrackDetails, error) {
	slog.Debug("Entity lookup", "type", "track", "id", id)
	query := fmt.Sprintf(trackDetailsQuery, r.schema)
	return getEntityByID[TrackDetails](ctx, r.db, query, id, "track", "fetch track")
}

// Image operations.

// GetImage retrieves the image for an entity.
func (r *Repository) GetImage(ctx context.Context, table types.Table, id string) ([]byte, error) {
	qualifiedTableName, label, idCol, err := r.resolveTable(table)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf("SELECT picture FROM %s WHERE %s = $1", qualifiedTableName, idCol)

	var imageData []byte
	err = r.db.GetContext(ctx, &imageData, query, id)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, types.NewNotFoundError(label, id)
	}
	if err != nil {
		return nil, types.NewOperationError(fmt.Sprintf("fetch %s image", label), err)
	}
	if imageData == nil {
		return nil, types.NewNoImageError(label, id)
	}

	return imageData, nil
}

// UpdateImage stores new image data for the specified entity.
func (r *Repository) UpdateImage(ctx context.Context, table types.Table, id string, imageData []byte) error {
	qualifiedTableName, label, idCol, err := r.resolveTable(table)
	if err != nil {
		return err
	}

	query := fmt.Sprintf("UPDATE %s SET picture = $1 WHERE %s = $2", qualifiedTableName, idCol)

	_, err = r.db.ExecContext(ctx, query, imageData, id)
	if err != nil {
		return types.NewOperationError(fmt.Sprintf("update %s", label), err)
	}
	return nil
}

// DeleteImage removes the image for an entity.
func (r *Repository) DeleteImage(ctx context.Context, table types.Table, id string) error {
	qualifiedTableName, label, idCol, err := r.resolveTable(table)
	if err != nil {
		return err
	}

	query := fmt.Sprintf("UPDATE %s SET picture = NULL WHERE %s = $1", qualifiedTableName, idCol)

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return types.NewOperationError(fmt.Sprintf("delete %s image", label), err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return types.NewOperationError(fmt.Sprintf("delete %s image", label), err)
	}

	if rowsAffected == 0 {
		return types.NewNotFoundError(label+" image", id)
	}

	return nil
}

// Count operations.

// ImageCounts contains total and with-image counts for a table.
type ImageCounts struct {
	Total      int `db:"total"`
	WithImages int `db:"with_images"`
}

// CountImages returns total entity count and count with images in a single query.
func (r *Repository) CountImages(ctx context.Context, table types.Table) (*ImageCounts, error) {
	qualifiedTableName, label, _, err := r.resolveTable(table)
	if err != nil {
		return nil, err
	}
	query := fmt.Sprintf("SELECT COUNT(*) AS total, COUNT(picture) AS with_images FROM %s", qualifiedTableName)

	var counts ImageCounts
	if err := r.db.GetContext(ctx, &counts, query); err != nil {
		return nil, types.NewOperationError(fmt.Sprintf("count %s", label), err)
	}
	return &counts, nil
}

// CountWithImages counts entities that have images.
func (r *Repository) CountWithImages(ctx context.Context, table types.Table) (int, error) {
	return r.countItems(ctx, table, true)
}

func (r *Repository) countItems(ctx context.Context, table types.Table, hasImage bool) (int, error) {
	condition := "IS NULL"
	if hasImage {
		condition = "IS NOT NULL"
	}

	qualifiedTableName, label, _, err := r.resolveTable(table)
	if err != nil {
		return 0, err
	}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE picture %s", qualifiedTableName, condition)

	var count int
	err = r.db.GetContext(ctx, &count, query)
	if err != nil {
		return 0, types.NewOperationError(fmt.Sprintf("count %s", label), err)
	}

	return count, nil
}

// DeleteAllImages removes all images for entities in the specified table.
func (r *Repository) DeleteAllImages(ctx context.Context, table types.Table) (int64, error) {
	qualifiedTableName, label, _, err := r.resolveTable(table)
	if err != nil {
		return 0, err
	}

	query := fmt.Sprintf("UPDATE %s SET picture = NULL WHERE picture IS NOT NULL", qualifiedTableName)

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, types.NewOperationError(fmt.Sprintf("delete %s images", label), err)
	}

	return result.RowsAffected()
}

// Playlist operations.

// GetPlaylist retrieves playlist items based on options.
func (r *Repository) GetPlaylist(ctx context.Context, opts *PlaylistOptions) ([]PlaylistItem, error) {
	query, params, err := BuildPlaylistQuery(r.schema, opts)
	if err != nil {
		return nil, err
	}
	return ExecutePlaylistQuery(ctx, r.db, query, params)
}

// GetPlaylistBlocks retrieves all playlist blocks for a specific date.
func (r *Repository) GetPlaylistBlocks(ctx context.Context, date string) ([]PlaylistBlock, error) {
	var dateFilter string
	params := []any{}

	if date != "" {
		dateFilter = "pb.startdatetime >= $1::date AND pb.startdatetime < $1::date + INTERVAL '1 day'"
		params = append(params, date)
	} else {
		dateFilter = "pb.startdatetime >= CURRENT_DATE AND pb.startdatetime < CURRENT_DATE + INTERVAL '1 day'"
	}

	query := fmt.Sprintf(`
		SELECT
			pb.blockid,
			COALESCE(pb.name, '') as name,
			DATE(pb.startdatetime)::text as date,
			TO_CHAR(pb.startdatetime, 'HH24:MI:SS') as start_time,
			TO_CHAR(pb.enddatetime, 'HH24:MI:SS') as end_time
		FROM %s.playlistblock pb
		WHERE %s
		ORDER BY pb.startdatetime
	`, r.schema, dateFilter)

	var blocks []PlaylistBlock
	err := r.db.SelectContext(ctx, &blocks, query, params...)
	if err != nil {
		return nil, types.NewOperationError("fetch playlist blocks", err)
	}

	return blocks, nil
}

// GetPlaylistWithTracks retrieves all blocks with their associated tracks for a date.
func (r *Repository) GetPlaylistWithTracks(ctx context.Context, date string) ([]PlaylistBlock, map[string][]PlaylistItem, error) {
	blocks, err := r.GetPlaylistBlocks(ctx, date)
	if err != nil {
		return nil, nil, err
	}

	if len(blocks) == 0 {
		return blocks, make(map[string][]PlaylistItem), nil
	}

	blockIDs := make([]string, len(blocks))
	for i, block := range blocks {
		blockIDs[i] = block.BlockID
	}

	var dateFilter string
	params := []any{}
	paramCount := 0

	if date != "" {
		dateFilter = "pi.startdatetime >= $1::date AND pi.startdatetime < $1::date + INTERVAL '1 day'"
		params = append(params, date)
		paramCount = 1
	} else {
		dateFilter = "pi.startdatetime >= CURRENT_DATE AND pi.startdatetime < CURRENT_DATE + INTERVAL '1 day'"
	}

	placeholders := make([]string, len(blockIDs))
	for i, id := range blockIDs {
		paramCount++
		placeholders[i] = fmt.Sprintf("$%d", paramCount)
		params = append(params, id)
	}

	type playlistItemWithBlockID struct {
		PlaylistItem
		TempBlockID string `db:"blockid"`
	}

	columns := fmt.Sprintf(playlistItemColumns, types.VoicetrackUserID)
	joins := fmt.Sprintf(playlistItemJoins, r.schema, r.schema, r.schema)
	query := fmt.Sprintf("SELECT %s, COALESCE(pi.blockid::text, '') as blockid %s WHERE %s AND pi.blockid IN (%s) ORDER BY pi.blockid, pi.startdatetime",
		columns, joins, dateFilter, strings.Join(placeholders, ","))

	var tempItems []playlistItemWithBlockID
	err = r.db.SelectContext(ctx, &tempItems, query, params...)
	if err != nil {
		return nil, nil, types.NewOperationError("fetch playlist items", err)
	}

	tracksByBlock := make(map[string][]PlaylistItem)
	for i := range tempItems {
		tracksByBlock[tempItems[i].TempBlockID] = append(tracksByBlock[tempItems[i].TempBlockID], tempItems[i].PlaylistItem)
	}

	return blocks, tracksByBlock, nil
}
