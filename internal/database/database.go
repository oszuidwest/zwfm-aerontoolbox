package database

import (
	"context"
	"database/sql"
	"errors"

	"github.com/oszuidwest/zwfm-aerontoolbox/internal/types"

	_ "github.com/lib/pq"
)

// DB is the database interface for data access operations.
type DB interface {
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	SelectContext(ctx context.Context, dest any, query string, args ...any) error
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	PingContext(ctx context.Context) error
}

// Artist is a basic artist entity with ID, name, and image status.
type Artist struct {
	ID         string `db:"artistid"`
	ArtistName string `db:"artist"`
	HasImage   bool   `db:"has_image"`
}

// ArtistDetails contains complete artist information including social media and metadata.
type ArtistDetails struct {
	ID          string `db:"artistid" json:"artistid"`
	ArtistName  string `db:"artist" json:"artist"`
	Info        string `db:"info" json:"info"`
	Website     string `db:"website" json:"website"`
	Twitter     string `db:"twitter" json:"twitter"`
	Instagram   string `db:"instagram" json:"instagram"`
	HasImage    bool   `db:"has_image" json:"has_image"`
	RepeatValue int    `db:"repeat_value" json:"repeat_value"`
}

// Track is a basic track entity with ID, title, artist, and image status.
type Track struct {
	ID         string `db:"titleid"`
	TrackTitle string `db:"tracktitle"`
	Artist     string `db:"artist"`
	HasImage   bool   `db:"has_image"`
}

// TrackDetails contains complete track information including timing and audio properties.
type TrackDetails struct {
	ID            string `db:"titleid" json:"titleid"`
	TrackTitle    string `db:"tracktitle" json:"tracktitle"`
	Artist        string `db:"artist" json:"artist"`
	ArtistID      string `db:"artistid" json:"artistid"`
	Year          int    `db:"year" json:"year"`
	KnownLengthMs int    `db:"knownlength" json:"knownlength"`
	IntroTimeMs   int    `db:"introtime" json:"introtime"`
	OutroTimeMs   int    `db:"outrotime" json:"outrotime"`
	Tempo         int    `db:"tempo" json:"tempo"`
	BPM           int    `db:"bpm" json:"bpm"`
	Gender        int    `db:"gender" json:"gender"`
	Language      int    `db:"language" json:"language"`
	Mood          int    `db:"mood" json:"mood"`
	ExportType    int    `db:"exporttype" json:"exporttype"`
	RepeatValue   int    `db:"repeat_value" json:"repeat_value"`
	Rating        int    `db:"rating" json:"rating"`
	HasImage      bool   `db:"has_image" json:"has_image"`
	Website       string `db:"website" json:"website"`
	Conductor     string `db:"conductor" json:"conductor"`
	Orchestra     string `db:"orchestra" json:"orchestra"`
}

const artistDetailsQuery = `
	SELECT
		artistid,
		COALESCE(artist, '') as artist,
		COALESCE(info, '') as info,
		COALESCE(website, '') as website,
		COALESCE(twitter, '') as twitter,
		COALESCE(instagram, '') as instagram,
		CASE WHEN picture IS NOT NULL THEN true ELSE false END as has_image,
		COALESCE(repeatvalue, 0) as repeat_value
	FROM %s.artist
	WHERE artistid = $1`

const trackDetailsQuery = `
	SELECT
		titleid,
		COALESCE(tracktitle, '') as tracktitle,
		COALESCE(artist, '') as artist,
		COALESCE(artistid, '00000000-0000-0000-0000-000000000000') as artistid,
		COALESCE("Year", 0) as year,
		COALESCE(knownlength, 0) as knownlength,
		COALESCE(introtime, 0) as introtime,
		COALESCE(outrotime, 0) as outrotime,
		COALESCE(tempo, 0) as tempo,
		COALESCE(bpm, 0) as bpm,
		COALESCE(gender, 0) as gender,
		COALESCE("Language", 0) as language,
		COALESCE(mood, 0) as mood,
		COALESCE(exporttype, 0) as exporttype,
		COALESCE(repeatvalue, 0) as repeat_value,
		COALESCE(rating, 0) as rating,
		CASE WHEN picture IS NOT NULL THEN true ELSE false END as has_image,
		COALESCE(website, '') as website,
		COALESCE(conductor, '') as conductor,
		COALESCE(orchestra, '') as orchestra
	FROM %s.track
	WHERE titleid = $1`

func getEntityByID[T any](ctx context.Context, db DB, query, id, label, operation string) (*T, error) {
	var entity T
	err := db.GetContext(ctx, &entity, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, types.NewNotFoundError(label, id)
	}
	if err != nil {
		return nil, &types.OperationError{Operation: operation, Err: err}
	}
	return &entity, nil
}
