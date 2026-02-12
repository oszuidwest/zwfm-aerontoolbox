package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// playlistItemColumns defines the fields returned for each playlist item.
const playlistItemColumns = `
	pi.titleid as trackid,
	COALESCE(t.tracktitle, '') as tracktitle,
	COALESCE(t.artistid, '00000000-0000-0000-0000-000000000000') as artistid,
	COALESCE(t.artist, '') as artistname,
	TO_CHAR(pi.startdatetime, 'HH24:MI:SS') as start_time,
	TO_CHAR(pi.startdatetime + INTERVAL '1 millisecond' * COALESCE(t.knownlength, 0), 'HH24:MI:SS') as end_time,
	COALESCE(t.knownlength, 0) as duration,
	CASE WHEN t.picture IS NOT NULL THEN true ELSE false END as has_track_image,
	CASE WHEN a.picture IS NOT NULL THEN true ELSE false END as has_artist_image,
	COALESCE(t.exporttype, 0) as exporttype,
	COALESCE(pi.mode, 0) as mode,
	CASE WHEN t.userid = '%s' THEN true ELSE false END as is_voicetrack,
	CASE WHEN COALESCE(pi.commblock, 0) > 0 THEN true ELSE false END as is_commblock`

// playlistItemJoins defines the table relationships for playlist item queries.
const playlistItemJoins = `
	FROM %s.playlistitem pi
	LEFT JOIN %s.track t ON pi.titleid = t.titleid
	LEFT JOIN %s.artist a ON t.artistid = a.artistid`

// PlaylistBlock represents a programming block with scheduling information.
type PlaylistBlock struct {
	BlockID        string `db:"blockid" json:"blockid"`
	Name           string `db:"name" json:"name"`
	StartTimeOfDay string `db:"start_time" json:"start_time"`
	EndTimeOfDay   string `db:"end_time" json:"end_time"`
	Date           string `db:"date" json:"date"`
}

// PlaylistItem represents a single scheduled track or media item.
type PlaylistItem struct {
	TrackID        string `db:"trackid" json:"trackid"`
	TrackTitle     string `db:"tracktitle" json:"tracktitle"`
	ArtistID       string `db:"artistid" json:"artistid"`
	ArtistName     string `db:"artistname" json:"artistname"`
	StartTime      string `db:"start_time" json:"start_time"`
	EndTime        string `db:"end_time" json:"end_time"`
	Duration       int    `db:"duration" json:"duration"`
	HasTrackImage  bool   `db:"has_track_image" json:"has_track_image"`
	HasArtistImage bool   `db:"has_artist_image" json:"has_artist_image"`
	ExportType     int    `db:"exporttype" json:"exporttype"`
	Mode           int    `db:"mode" json:"mode"`
	IsVoicetrack   bool   `db:"is_voicetrack" json:"is_voicetrack"`
	IsCommblock    bool   `db:"is_commblock" json:"is_commblock"`
}

// PlaylistOptions contains filter, sort, and pagination parameters for playlist queries.
type PlaylistOptions struct {
	BlockID     string
	Date        string
	ExportTypes []int
	Limit       int
	Offset      int
	SortBy      string
	SortDesc    bool
	TrackImage  *bool
	ArtistImage *bool
}

// BuildPlaylistQuery generates a parameterized SQL query from playlist filter options.
func BuildPlaylistQuery(schema string, opts *PlaylistOptions) (query string, params []any, err error) {
	var conditions []string
	paramCount := 0

	nextParam := func() string {
		paramCount++
		return fmt.Sprintf("$%d", paramCount)
	}

	if opts.BlockID != "" {
		conditions = append(conditions, fmt.Sprintf("pi.blockid = %s", nextParam()))
		params = append(params, opts.BlockID)
	} else {
		return "", []any{}, nil
	}

	if len(opts.ExportTypes) > 0 {
		placeholders := make([]string, len(opts.ExportTypes))
		for i, t := range opts.ExportTypes {
			placeholders[i] = nextParam()
			params = append(params, t)
		}
		conditions = append(conditions, fmt.Sprintf("COALESCE(t.exporttype, 1) NOT IN (%s)", strings.Join(placeholders, ",")))
	}

	if opts.TrackImage != nil {
		if *opts.TrackImage {
			conditions = append(conditions, "t.picture IS NOT NULL")
		} else {
			conditions = append(conditions, "t.picture IS NULL")
		}
	}

	if opts.ArtistImage != nil {
		if *opts.ArtistImage {
			conditions = append(conditions, "a.picture IS NOT NULL")
		} else {
			conditions = append(conditions, "a.picture IS NULL")
		}
	}

	whereClause := strings.Join(conditions, " AND ")

	orderBy := "pi.startdatetime"
	switch opts.SortBy {
	case "artist":
		orderBy = "t.artist"
	case "track":
		orderBy = "t.tracktitle"
	case "start_time":
		orderBy = "pi.startdatetime"
	}
	if opts.SortDesc {
		orderBy += " DESC"
	}

	if !types.IsValidIdentifier(schema) {
		return "", nil, types.NewValidationError("schema", fmt.Sprintf("invalid schema name: %s", schema))
	}

	columns := fmt.Sprintf(playlistItemColumns, types.VoicetrackUserID)
	joins := fmt.Sprintf(playlistItemJoins, schema, schema, schema)
	query = fmt.Sprintf("SELECT %s %s WHERE %s ORDER BY %s", columns, joins, whereClause, orderBy)

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %s", nextParam())
		params = append(params, opts.Limit)
		if opts.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %s", nextParam())
			params = append(params, opts.Offset)
		}
	}

	return query, params, nil
}

// ExecutePlaylistQuery executes a playlist query and maps results to PlaylistItem structs.
func ExecutePlaylistQuery(ctx context.Context, db DB, query string, params []any) ([]PlaylistItem, error) {
	var items []PlaylistItem
	err := db.SelectContext(ctx, &items, query, params...)
	if err != nil {
		return nil, &types.OperationError{Operation: "fetch playlist", Err: err}
	}

	return items, nil
}
