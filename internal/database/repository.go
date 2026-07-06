// Package database queries the Aeron PostgreSQL schema and maps rows to API models.
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

// Repository binds Aeron queries to a PostgreSQL schema.
type Repository struct {
	db     *sqlx.DB
	schema string

	// Schema-dependent SQL rendered once here; the schema never changes for
	// the lifetime of the process.
	artistDetailsSQL string
	trackDetailsSQL  string
	playlistJoinsSQL string
}

// NewRepository returns a Repository scoped to schema.
func NewRepository(db *sqlx.DB, schema string) *Repository {
	return &Repository{
		db:               db,
		schema:           schema,
		artistDetailsSQL: fmt.Sprintf(artistDetailsQuery, schema),
		trackDetailsSQL:  fmt.Sprintf(trackDetailsQuery, schema),
		playlistJoinsSQL: fmt.Sprintf(playlistItemJoins, schema, schema, schema),
	}
}

// Schema returns the PostgreSQL schema name.
func (r *Repository) Schema() string {
	return r.schema
}

// Ping verifies the database connection can answer requests.
func (r *Repository) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// resolveTable returns the qualified table name, label, and ID column for the given entity.
func (r *Repository) resolveTable(entity types.EntityType) (qualifiedName, label, idCol string, err error) {
	qualifiedName, err = types.QualifiedTable(r.schema, entity)
	if err != nil {
		return "", "", "", types.NewValidationError("table", fmt.Sprintf("invalid table configuration: %v", err))
	}
	return qualifiedName, string(entity), entity.IDColumn(), nil
}

// GetArtist retrieves complete artist details by UUID.
func (r *Repository) GetArtist(ctx context.Context, id string) (*ArtistDetails, error) {
	slog.Debug("Entity lookup", "type", "artist", "id", id)
	return getEntityByID[ArtistDetails](ctx, r.db, r.artistDetailsSQL, id, "artist")
}

// GetTrack retrieves complete track details by UUID.
func (r *Repository) GetTrack(ctx context.Context, id string) (*TrackDetails, error) {
	slog.Debug("Entity lookup", "type", "track", "id", id)
	return getEntityByID[TrackDetails](ctx, r.db, r.trackDetailsSQL, id, "track")
}

// GetImage retrieves the stored image for an artist or track.
func (r *Repository) GetImage(ctx context.Context, entity types.EntityType, id string) ([]byte, error) {
	qualifiedTableName, label, idCol, err := r.resolveTable(entity)
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

// UpdateImage stores image bytes for an artist or track.
func (r *Repository) UpdateImage(ctx context.Context, entity types.EntityType, id string, imageData []byte) error {
	qualifiedTableName, label, idCol, err := r.resolveTable(entity)
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

// DeleteImage clears the stored image for an artist or track.
func (r *Repository) DeleteImage(ctx context.Context, entity types.EntityType, id string) error {
	qualifiedTableName, label, idCol, err := r.resolveTable(entity)
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

// ImageCounts is the total and with-image count for a table.
type ImageCounts struct {
	Total      int `db:"total"`
	WithImages int `db:"with_images"`
}

// CountImages returns total rows and rows with images in one query.
func (r *Repository) CountImages(ctx context.Context, entity types.EntityType) (*ImageCounts, error) {
	qualifiedTableName, label, _, err := r.resolveTable(entity)
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

// DeleteAllImages clears every stored image in table.
func (r *Repository) DeleteAllImages(ctx context.Context, entity types.EntityType) (int64, error) {
	qualifiedTableName, label, _, err := r.resolveTable(entity)
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

// GetPlaylist retrieves playlist items for the requested block scope.
func (r *Repository) GetPlaylist(ctx context.Context, opts *PlaylistOptions) ([]PlaylistItem, error) {
	query, params, err := BuildPlaylistQuery(r.schema, opts)
	if err != nil {
		return nil, err
	}
	var items []PlaylistItem
	if err := r.db.SelectContext(ctx, &items, query, params...); err != nil {
		return nil, types.NewOperationError("fetch playlist", err)
	}
	return items, nil
}

// GetPlaylistBlocks retrieves all playlist blocks for date or today when empty.
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

// GetPlaylistWithTracks retrieves date-scoped blocks and their items.
func (r *Repository) GetPlaylistWithTracks(
	ctx context.Context, date string,
) ([]PlaylistBlock, map[string][]PlaylistItem, error) {
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

	pl := &paramList{}

	var dateFilter string
	if date != "" {
		p := pl.next(date)
		dateFilter = fmt.Sprintf(
			"pi.startdatetime >= %s::date AND pi.startdatetime < %s::date + INTERVAL '1 day'", p, p)
	} else {
		dateFilter = "pi.startdatetime >= CURRENT_DATE AND pi.startdatetime < CURRENT_DATE + INTERVAL '1 day'"
	}

	placeholders := make([]string, len(blockIDs))
	for i, id := range blockIDs {
		placeholders[i] = pl.next(id)
	}

	type playlistItemWithBlockID struct {
		PlaylistItem
		TempBlockID string `db:"blockid"`
	}

	query := fmt.Sprintf(
		"SELECT %s, COALESCE(pi.blockid::text, '') as blockid %s WHERE %s AND pi.blockid IN (%s)"+
			" ORDER BY pi.blockid, pi.startdatetime",
		playlistItemColumnsSQL, r.playlistJoinsSQL, dateFilter, strings.Join(placeholders, ","))

	var tempItems []playlistItemWithBlockID
	err = r.db.SelectContext(ctx, &tempItems, query, pl.values...)
	if err != nil {
		return nil, nil, types.NewOperationError("fetch playlist items", err)
	}

	tracksByBlock := make(map[string][]PlaylistItem)
	for i := range tempItems {
		tracksByBlock[tempItems[i].TempBlockID] = append(tracksByBlock[tempItems[i].TempBlockID], tempItems[i].PlaylistItem)
	}

	return blocks, tracksByBlock, nil
}
