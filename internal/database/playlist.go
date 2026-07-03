package database

import (
	"fmt"
	"strings"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// playlistItemColumns selects the API projection for playlist items.
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

// playlistItemJoins joins playlist items to track and artist data.
const playlistItemJoins = `
	FROM %s.playlistitem pi
	LEFT JOIN %s.track t ON pi.titleid = t.titleid
	LEFT JOIN %s.artist a ON t.artistid = a.artistid`

// playlistItemColumnsSQL is playlistItemColumns with the voicetrack marker
// rendered; it contains no request-dependent input.
var playlistItemColumnsSQL = fmt.Sprintf(playlistItemColumns, types.VoicetrackUserID)

// PlaylistBlock is a scheduled programming block.
type PlaylistBlock struct {
	BlockID        string `db:"blockid" json:"blockid"`
	Name           string `db:"name" json:"name"`
	StartTimeOfDay string `db:"start_time" json:"start_time"`
	EndTimeOfDay   string `db:"end_time" json:"end_time"`
	Date           string `db:"date" json:"date"`
}

// PlaylistItem is one scheduled track or media item within a block.
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

// PlaylistOptions controls playlist filtering, sorting, and pagination.
type PlaylistOptions struct {
	BlockID     string
	Limit       int
	Offset      int
	SortBy      string
	SortDesc    bool
	TrackImage  *bool
	ArtistImage *bool
}

// BuildPlaylistQuery builds a parameterized block-item query.
func BuildPlaylistQuery(schema string, opts *PlaylistOptions) (query string, params []any, err error) {
	if opts.BlockID == "" {
		return "", nil, types.NewValidationError("block_id", "is required")
	}

	pl := &paramList{}
	conditions := []string{fmt.Sprintf("pi.blockid = %s", pl.next(opts.BlockID))}

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

	joins := fmt.Sprintf(playlistItemJoins, schema, schema, schema)
	query = fmt.Sprintf("SELECT %s %s WHERE %s ORDER BY %s", playlistItemColumnsSQL, joins, whereClause, orderBy)

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %s", pl.next(opts.Limit))
		if opts.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %s", pl.next(opts.Offset))
		}
	}

	return query, pl.values, nil
}
