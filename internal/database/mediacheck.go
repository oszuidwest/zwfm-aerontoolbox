package database

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"
)

// MediaCheckItem holds the database fields needed to verify that a playlist
// item's audio file exists on disk.
//
// The audio reference fields come from the aeron.audio table joined via
// track.audioid. In the real Aeron schema both FilePath and FileName hold full
// Windows paths (and may differ); AudioName is the bare filename without
// extension and is the most reliably populated reference. Empty strings denote
// an absent reference. TrackFound is false when the playlist item's titleid no
// longer resolves to a track (e.g. the track was deleted after airing).
type MediaCheckItem struct {
	TrackID      string `db:"trackid"`
	Artist       string `db:"artist"`
	TrackTitle   string `db:"tracktitle"`
	StartTime    string `db:"start_time"`
	BlockID      string `db:"blockid"`
	BlockName    string `db:"block"`
	TrackFound   bool   `db:"track_found"`
	AudioID      string `db:"audioid"`
	FilePath     string `db:"filepath"`
	FileName     string `db:"filename"`
	AudioName    string `db:"audioname"`
	IsVoicetrack bool   `db:"is_voicetrack"`
}

// MediaCheckOptions contains the scope filters for a media file check query.
//
// Filter precedence mirrors the playlist endpoint: a BlockID selects a single
// block; otherwise Date selects one calendar day; otherwise From/To select a
// date range (at least one bound); otherwise the query defaults to the current
// day.
type MediaCheckOptions struct {
	BlockID            string
	Date               string
	From               string
	To                 string
	Limit              int
	IncludeVoicetracks bool
}

// mediaCheckColumns selects the playlist item, track, audio and block fields.
// %s is the VoicetrackUserID used to flag voice tracks.
const mediaCheckColumns = `
	COALESCE(pi.titleid::text, '') as trackid,
	COALESCE(t.tracktitle, '') as tracktitle,
	COALESCE(t.artist, '') as artist,
	TO_CHAR(pi.startdatetime, 'YYYY-MM-DD"T"HH24:MI:SS') as start_time,
	COALESCE(pi.blockid::text, '') as blockid,
	COALESCE(pb.name, '') as block,
	CASE WHEN t.titleid IS NOT NULL THEN true ELSE false END as track_found,
	COALESCE(t.audioid::text, '') as audioid,
	COALESCE(au.filepath, '') as filepath,
	COALESCE(au.filename, '') as filename,
	COALESCE(au.name, '') as audioname,
	CASE WHEN t.userid = '%s' THEN true ELSE false END as is_voicetrack`

// mediaCheckJoins defines the table relationships. The three %s are the schema
// for playlistitem, track, audio and playlistblock respectively.
const mediaCheckJoins = `
	FROM %s.playlistitem pi
	LEFT JOIN %s.track t ON pi.titleid = t.titleid
	LEFT JOIN %s.audio au ON t.audioid = au.audioid
	LEFT JOIN %s.playlistblock pb ON pi.blockid = pb.blockid`

// BuildMediaCheckQuery generates a parameterized query selecting playlist items
// with their audio references for the given scope. The schema is validated as an
// identifier; all user-supplied values are bound as parameters.
func BuildMediaCheckQuery(schema string, opts *MediaCheckOptions) (query string, params []any, err error) {
	if !types.IsValidIdentifier(schema) {
		return "", nil, types.NewValidationError("schema", fmt.Sprintf("invalid schema name: %s", schema))
	}

	paramCount := 0
	nextParam := func(v any) string {
		paramCount++
		params = append(params, v)
		return "$" + strconv.Itoa(paramCount)
	}

	var conditions []string
	switch {
	case opts.BlockID != "":
		conditions = append(conditions, fmt.Sprintf("pi.blockid = %s", nextParam(opts.BlockID)))
	case opts.Date != "":
		p := nextParam(opts.Date)
		conditions = append(conditions,
			fmt.Sprintf("pi.startdatetime >= %s::date AND pi.startdatetime < %s::date + INTERVAL '1 day'", p, p))
	case opts.From != "" || opts.To != "":
		if opts.From != "" {
			conditions = append(conditions, fmt.Sprintf("pi.startdatetime >= %s::date", nextParam(opts.From)))
		}
		if opts.To != "" {
			conditions = append(conditions, fmt.Sprintf("pi.startdatetime < %s::date + INTERVAL '1 day'", nextParam(opts.To)))
		}
	default:
		conditions = append(conditions,
			"pi.startdatetime >= CURRENT_DATE AND pi.startdatetime < CURRENT_DATE + INTERVAL '1 day'")
	}

	if !opts.IncludeVoicetracks {
		conditions = append(conditions,
			fmt.Sprintf("(t.userid IS NULL OR t.userid <> %s::uuid)", nextParam(types.VoicetrackUserID)))
	}

	columns := fmt.Sprintf(mediaCheckColumns, types.VoicetrackUserID)
	joins := fmt.Sprintf(mediaCheckJoins, schema, schema, schema, schema)
	query = fmt.Sprintf("SELECT %s %s WHERE %s ORDER BY pi.startdatetime",
		columns, joins, strings.Join(conditions, " AND "))

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %s", nextParam(opts.Limit))
	}

	return query, params, nil
}

// GetMediaCheckItems returns playlist items with their audio references for the
// given scope.
func (r *Repository) GetMediaCheckItems(ctx context.Context, opts *MediaCheckOptions) ([]MediaCheckItem, error) {
	query, params, err := BuildMediaCheckQuery(r.schema, opts)
	if err != nil {
		return nil, err
	}

	var items []MediaCheckItem
	if err := r.db.SelectContext(ctx, &items, query, params...); err != nil {
		return nil, types.NewOperationError("fetch media check items", err)
	}
	return items, nil
}
