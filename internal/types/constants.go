package types

import "fmt"

// EntityType identifies the type of entity (artist or track).
type EntityType string

const (
	// EntityTypeArtist represents an artist entity.
	EntityTypeArtist EntityType = "artist"
	// EntityTypeTrack represents a track entity.
	EntityTypeTrack EntityType = "track"
)

// Table represents a database table name.
type Table string

const (
	// TableArtist is the database table name for artist records.
	TableArtist Table = "artist"
	// TableTrack is the database table name for track records.
	TableTrack Table = "track"
)

// LongRunningQuery represents a query that has been running longer than the configured threshold.
type LongRunningQuery struct {
	PID      int    `db:"pid" json:"pid"`
	Duration string `db:"duration" json:"duration"`
	Query    string `db:"query" json:"query"`
	State    string `db:"state" json:"state"`
}

// VoicetrackUserID is the UUID used in Aeron to identify voice tracks.
const VoicetrackUserID = "021F097E-B504-49BB-9B89-16B64D2E8422"

// SupportedFormats lists the image formats that can be processed.
var SupportedFormats = []string{"jpeg", "jpg", "png"}

// IDColumnForTable returns the primary key column name for the given table.
func IDColumnForTable(table Table) string {
	if table == TableTrack {
		return "titleid"
	}
	return "artistid"
}

// IsValidIdentifier reports whether name contains only valid SQL identifier characters.
func IsValidIdentifier(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

// QualifiedTable returns a fully qualified schema.table name after validating both identifiers.
func QualifiedTable(schema string, table Table) (string, error) {
	if !IsValidIdentifier(schema) {
		return "", fmt.Errorf("invalid schema name: %s", schema)
	}
	if !IsValidIdentifier(string(table)) {
		return "", fmt.Errorf("invalid table name: %s", table)
	}
	return fmt.Sprintf("%s.%s", schema, table), nil
}
