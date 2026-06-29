package util

import "fmt"

// FormatArtistTitle renders an "artist - title" label, falling back to whichever
// of the two is present (title preferred) and to "" when both are empty.
func FormatArtistTitle(artist, title string) string {
	switch {
	case artist != "" && title != "":
		return artist + " - " + title
	case title != "":
		return title
	default:
		return artist
	}
}

// FormatBytes converts bytes to a human-readable string with binary prefixes.
func FormatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
