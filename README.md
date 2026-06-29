# Aeron Toolbox

Het radioautomatiseringssysteem Aeron mist tooling voor beheer en onderhoud. Aeron Toolbox vult dat gat: een toolkit waarmee je via HTTP afbeeldingen beheert, media doorzoekt, de database onderhoudt, backups maakt en bestanden bewaakt.

> [!WARNING]
> Dit is een **onofficiële** tool, niet ontwikkeld door of in samenwerking met Nextwave Broadcast. Gebruik op eigen risico. Maak altijd een backup voordat je begint.

## Wat kan het?

- **Afbeeldingen:** upload en optimaliseer albumhoezen en artiestfoto's
- **Media:** doorzoek artiesten, tracks en playlists met metadata
- **Onderhoud:** bewaak de gezondheid van de database met automatische meldingen bij problemen
- **Backups:** maak, valideer en download databasebackups (optioneel naar S3)
- **Bestandscontrole:** controleer of bestanden actueel zijn, met meldingen bij verouderde of ontbrekende bestanden
- **Mediabestanden:** controleer database-gestuurd of de audio uit de playlist daadwerkelijk op schijf staat

## Snel starten

### Docker (aanbevolen)

```bash
# Download configuratie
wget https://raw.githubusercontent.com/oszuidwest/zwfm-aerontoolbox/main/config.example.json -O config.json
wget https://raw.githubusercontent.com/oszuidwest/zwfm-aerontoolbox/main/docker-compose.example.yml -O docker-compose.yml

# Pas config.json aan naar jouw situatie, dan:
docker compose up -d
```

Of direct met `docker run`:

```bash
docker run -d -p 8080:8080 \
  -e TZ=Europe/Amsterdam \
  -v $(pwd)/config.json:/app/config.json:ro \
  -v $(pwd)/backups:/backups \
  --name zwfm-aerontoolbox \
  --restart unless-stopped \
  ghcr.io/oszuidwest/zwfm-aerontoolbox:latest
```

> [!NOTE]
> De `TZ` omgevingsvariabele bepaalt de tijdzone voor geplande taken (backups en health checks).

### Binary

Download een kant-en-klare Linux- of macOS-binary via de [releases-pagina](https://github.com/oszuidwest/zwfm-aerontoolbox/releases).

### Vanaf broncode

```bash
git clone https://github.com/oszuidwest/zwfm-aerontoolbox.git
cd zwfm-aerontoolbox
cp config.example.json config.json
go build -o zwfm-aerontoolbox .
./zwfm-aerontoolbox -config=config.json -port=8080
```

Vereist: Go 1.26+

## Configuratie

Kopieer [`config.example.json`](config.example.json) naar `config.json`. De belangrijkste secties:

| Sectie | Wat configureer je? |
|--------|---------------------|
| `database` | PostgreSQL-verbinding (host, poort, inloggegevens, schema) |
| `image` | Doelafmetingen en JPEG-kwaliteit voor geüploade afbeeldingen |
| `api` | API-sleutels voor authenticatie |
| `maintenance` | Drempelwaarden en automatische scheduler voor database health checks |
| `backup` | Pad naar backups, retentie, scheduler en optionele S3-sync |
| `file_monitor` | Signaleert verouderde of ontbrekende bestanden op schijf |
| `media_file_check` | Controleert database-gestuurd of playlist-audio op schijf staat (exacte `drive_mounts` + `search_dirs` als fallback) |
| `notifications` | E-mailmeldingen via Microsoft Graph API |
| `metrics` | Optionele Prometheus-compatible metrics-endpoint |
| `log` | Logniveau (`debug`, `info`, `warn`, `error`) en formaat (`text`, `json`) |

### Backupfunctionaliteit

Voor backups heb je `pg_dump` en `pg_restore` nodig op het systeem:

```bash
# Debian/Ubuntu
apt-get install postgresql-client

# Alpine (Docker)
apk add postgresql16-client

# macOS
brew install libpq
```

De applicatie valideert bij het opstarten of deze tools beschikbaar zijn wanneer `backup.enabled: true`.

### Automatische health checks

Database health checks kunnen automatisch worden uitgevoerd. Bij problemen (hoge bloat, veel connecties, langlopende queries) wordt een e-mailmelding verstuurd:

```json
"maintenance": {
  "connection_usage_threshold_pct": 80,
  "long_query_threshold_seconds": 10,
  "scheduler": {
    "enabled": true,
    "schedule": "0 4 * * 0"
  }
}
```

Dit controleert elke zondag om 04:00 de database en stuurt een melding bij problemen. Zie [API.md](API.md) voor details.

### E-mailnotificaties

Ontvang e-mailmeldingen bij mislukte backups, S3-synchronisatie, database health checks en verouderde of ontbrekende bestanden. Vereist een Azure AD app-registratie met `Mail.Send` permissie.

```json
"notifications": {
  "email": {
    "tenant_id": "je-azure-tenant-id",
    "client_id": "je-app-client-id",
    "client_secret": "je-client-secret",
    "from_address": "noreply@jouwdomein.nl",
    "recipients": "admin@jouwdomein.nl,beheer@jouwdomein.nl"
  }
}
```

De applicatie stuurt:
- **Foutmeldingen:** bij mislukte backup, S3-sync, database health check of verouderde/ontbrekende bestanden
- **Herstelmeldingen:** wanneer een eerder gemeld probleem is opgelost of een bestand weer actueel is

Test de configuratie via `POST /api/notifications/test-email`.

### Metrics

Prometheus-compatible metrics staan standaard uit. Zet ze expliciet aan wanneer
de endpoint bereikbaar mag zijn voor je monitoring-netwerk:

```json
"metrics": {
  "enabled": true,
  "path": "/metrics"
}
```

Voorbeeld scrape-configuratie:

```yaml
scrape_configs:
  - job_name: "aeron-toolbox"
    metrics_path: "/metrics"
    static_configs:
      - targets: ["localhost:8080"]
```

De endpoint bevat geen secrets, bestandsnamen of ruwe foutmeldingen. Bruikbare
alerts zijn bijvoorbeeld:
- `aeron_toolbox_database_connected == 0`
- `aeron_toolbox_backup_enabled == 1 and aeron_toolbox_backup_last_completed == 1 and aeron_toolbox_backup_last_success == 0`
- `aeron_toolbox_file_monitor_checks_alerting > 0`
- `aeron_toolbox_media_file_check_problems > 0`
- `aeron_toolbox_notifications_last_error == 1`

## Voorbeelden

```bash
# Health check
curl http://localhost:8080/api/health

# Metrics ophalen wanneer metrics.enabled=true
curl http://localhost:8080/metrics

# Artiestafbeelding uploaden (via URL)
curl -X POST http://localhost:8080/api/artists/{id}/image \
  -H "X-API-Key: jouw-api-sleutel" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/artist.jpg"}'

# Database backup starten (retourneert 202 Accepted, draait async)
curl -X POST http://localhost:8080/api/db/backup \
  -H "X-API-Key: jouw-api-sleutel"
```

Zie [API.md](API.md) voor de volledige API-documentatie.

## Licentie

MIT. Zie [LICENSE](LICENSE).
