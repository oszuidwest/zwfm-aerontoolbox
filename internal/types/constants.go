package types

import "fmt"

// EntityType identifies the media entity kind used by API routes.
type EntityType string

const (
	// EntityTypeArtist selects artist records.
	EntityTypeArtist EntityType = "artist"
	// EntityTypeTrack selects track records.
	EntityTypeTrack EntityType = "track"
)

// LongRunningQuery is a database query exceeding the configured duration threshold.
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

// IDColumn returns the Aeron primary-key column for the entity's table.
func (e EntityType) IDColumn() string {
	if e == EntityTypeTrack {
		return "titleid"
	}
	return "artistid"
}

// IsValidIdentifier reports whether name is a non-empty SQL identifier.
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
func QualifiedTable(schema string, entity EntityType) (string, error) {
	if !IsValidIdentifier(schema) {
		return "", fmt.Errorf("invalid schema name: %s", schema)
	}
	if !IsValidIdentifier(string(entity)) {
		return "", fmt.Errorf("invalid table name: %s", entity)
	}
	return fmt.Sprintf("%s.%s", schema, entity), nil
}
