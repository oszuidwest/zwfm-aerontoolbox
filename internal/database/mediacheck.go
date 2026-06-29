package database

import (
	"context"
	"fmt"
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
// an absent reference.
type MediaCheckItem struct {
	TrackID    string `db:"trackid"`
	Artist     string `db:"artist"`
	TrackTitle string `db:"tracktitle"`
	StartTime  string `db:"start_time"`
	BlockID    string `db:"blockid"`
	BlockName  string `db:"block"`
	FilePath   string `db:"filepath"`
	FileName   string `db:"filename"`
	AudioName  string `db:"audioname"`
}

// MediaCheckOptions defines the playlist scope for a media file check query.
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
	// LookaheadDays widens the default (today) scope to today through
	// today+LookaheadDays inclusive. Only set by the scheduled run; ignored when
	// BlockID/Date/From/To already select an explicit scope.
	LookaheadDays int
}

// mediaCheckColumns selects the playlist item, track, audio and block fields.
const mediaCheckColumns = `
	COALESCE(pi.titleid::text, '') as trackid,
	COALESCE(t.tracktitle, '') as tracktitle,
	COALESCE(t.artist, '') as artist,
	TO_CHAR(pi.startdatetime, 'YYYY-MM-DD"T"HH24:MI:SS') as start_time,
	COALESCE(pi.blockid::text, '') as blockid,
	COALESCE(pb.name, '') as block,
	COALESCE(au.filepath, '') as filepath,
	COALESCE(au.filename, '') as filename,
	COALESCE(au.name, '') as audioname`

// mediaCheckJoins defines the table relationships. The four %s are the schema
// for playlistitem, track, audio and playlistblock respectively.
const mediaCheckJoins = `
	FROM %s.playlistitem pi
	LEFT JOIN %s.track t ON pi.titleid = t.titleid
	LEFT JOIN %s.audio au ON t.audioid = au.audioid
	LEFT JOIN %s.playlistblock pb ON pi.blockid = pb.blockid`

// BuildMediaCheckQuery builds a parameterized playlist-audio query for the
// requested scope. The schema is validated as an identifier; user values are
// always bound as parameters.
func BuildMediaCheckQuery(schema string, opts *MediaCheckOptions) (query string, params []any, err error) {
	if !types.IsValidIdentifier(schema) {
		return "", nil, types.NewValidationError("schema", fmt.Sprintf("invalid schema name: %s", schema))
	}

	pl := &paramList{}

	var conditions []string
	switch {
	case opts.BlockID != "":
		conditions = append(conditions, fmt.Sprintf("pi.blockid = %s", pl.next(opts.BlockID)))
	case opts.Date != "":
		p := pl.next(opts.Date)
		conditions = append(conditions,
			fmt.Sprintf("pi.startdatetime >= %s::date AND pi.startdatetime < %s::date + INTERVAL '1 day'", p, p))
	case opts.From != "" || opts.To != "":
		if opts.From != "" {
			conditions = append(conditions, fmt.Sprintf("pi.startdatetime >= %s::date", pl.next(opts.From)))
		}
		if opts.To != "" {
			conditions = append(conditions, fmt.Sprintf("pi.startdatetime < %s::date + INTERVAL '1 day'", pl.next(opts.To)))
		}
	case opts.LookaheadDays > 0:
		// Today through today+LookaheadDays inclusive. The date math runs in the
		// database (CURRENT_DATE + N) so it stays consistent with the today-scope
		// regardless of the app's timezone.
		conditions = append(conditions, fmt.Sprintf(
			"pi.startdatetime >= CURRENT_DATE AND pi.startdatetime < CURRENT_DATE + %s::int",
			pl.next(opts.LookaheadDays+1)))
	default:
		conditions = append(conditions,
			"pi.startdatetime >= CURRENT_DATE AND pi.startdatetime < CURRENT_DATE + INTERVAL '1 day'")
	}

	if !opts.IncludeVoicetracks {
		conditions = append(conditions,
			fmt.Sprintf("(t.userid IS NULL OR t.userid <> %s::uuid)", pl.next(types.VoicetrackUserID)))
	}

	joins := fmt.Sprintf(mediaCheckJoins, schema, schema, schema, schema)
	query = fmt.Sprintf("SELECT %s %s WHERE %s ORDER BY pi.startdatetime",
		mediaCheckColumns, joins, strings.Join(conditions, " AND "))

	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %s", pl.next(opts.Limit))
	}

	return query, pl.values, nil
}

// GetMediaCheckItems returns playlist items and audio references for opts.
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
