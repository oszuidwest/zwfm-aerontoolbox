package notify

import "strings"

func formatEmailError(msg string) string {
	switch {
	case msg == "":
		return ""
	case msg == "backup functionality is not enabled":
		return "backupfunctionaliteit is niet ingeschakeld"
	case msg == "backup already in progress":
		return "er loopt al een backup"
	case msg == "backup cancelled":
		return "backup geannuleerd"
	case msg == "file monitor check already running":
		return "bestandscontrole is al bezig"
	case msg == "notification dropped: service is closed":
		return "notificatie niet verzonden: service is gesloten"
	case msg == "no recipients specified":
		return "geen ontvangers opgegeven"
	case msg == "no valid recipients after filtering":
		return "geen geldige ontvangers gevonden"
	case msg == "from address (shared mailbox) is required":
		return "afzenderadres (shared mailbox) is verplicht"
	case msg == "at least one valid recipient is required":
		return "minimaal een geldige ontvanger is verplicht"
	case msg == "authentication failed: invalid credentials":
		return "authenticatie mislukt: ongeldige credentials"
	case msg == "Graph API not configured":
		return "Graph API is niet geconfigureerd"
	case msg == "no valid (non-expired) credentials found":
		return "geen geldige, niet-verlopen credentials gevonden"
	}

	replacements := []struct {
		old string
		new string
	}{
		{"create backup failed: ", "backup maken mislukt: "},
		{"backup validation failed: ", "backupvalidatie mislukt: "},
		{"delete backup failed: ", "backup verwijderen mislukt: "},
		{"S3 upload failed: ", "S3-upload mislukt: "},
		{"S3 delete failed: ", "S3-verwijdering mislukt: "},
		{"get database size failed: ", "databasegrootte ophalen mislukt: "},
		{"get table statistics failed: ", "tabelstatistieken ophalen mislukt: "},
		{"get connection usage failed: ", "connectiegebruik ophalen mislukt: "},
		{"get long-running queries failed: ", "langlopende queries ophalen mislukt: "},
		{"file is corrupt or unreadable", "bestand is corrupt of onleesbaar"},
		{"backup timeout after ", "backup-timeout na "},
		{"configure backup.timeout_minutes", "configureer backup.timeout_minutes"},
		{"backup file not found after creation", "backupbestand niet gevonden na aanmaken"},
		{"stat timeout after ", "stat-timeout na "},
		{"permission denied", "geen rechten"},
		{"input/output error", "I/O-fout"},
		{"request cancelled", "request geannuleerd"},
		{"request failed", "request mislukt"},
		{"authentication failed", "authenticatie mislukt"},
		{"validation request failed", "validatierequest mislukt"},
		{"validation failed with status", "validatie mislukt met status"},
		{" not found", " niet gevonden"},
		{"max retries exceeded", "maximum aantal retries overschreden"},
		{"open file", "bestand openen"},
		{"close file", "bestand sluiten"},
		{"create token source", "token source aanmaken"},
		{"acquire token", "token ophalen"},
		{"create request", "request aanmaken"},
		{"parse response", "response parsen"},
		{"API returned", "API retourneerde"},
	}

	out := msg
	for _, r := range replacements {
		out = strings.ReplaceAll(out, r.old, r.new)
	}
	return out
}
