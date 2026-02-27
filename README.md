# Aeron Toolbox

Het radioautomatiseringssysteem Aeron mist een REST API. Aeron Toolbox vult een deel van dat gat: een headless API waarmee je via HTTP afbeeldingen beheert, media browst, de database onderhoudt en backups maakt.

> [!WARNING]
> Dit is een **onofficiële** tool, niet ontwikkeld door of in samenwerking met Cellnex Broadcast Partners. Gebruik op eigen risico. Maak altijd een backup voordat je begint.

## Wat kan het?

- **Afbeeldingen:** upload en optimaliseer albumhoezen en artiestfoto's
- **Media:** browse artiesten, tracks en playlists met metadata
- **Onderhoud:** monitor gezondheid van de database, automatische of handmatige VACUUM/ANALYZE
- **Backups:** maak, valideer en download databasebackups (optioneel naar S3)

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
> De `TZ` environment variable bepaalt de tijdzone voor geplande taken (backups en onderhoud).

### Binary

Download een kant-en-klare binary voor jouw platform via de [releases-pagina](https://github.com/oszuidwest/zwfm-aerontoolbox/releases).

### Vanaf broncode

```bash
git clone https://github.com/oszuidwest/zwfm-aerontoolbox.git
cd zwfm-aerontoolbox
cp config.example.json config.json
go build -o zwfm-aerontoolbox .
./zwfm-aerontoolbox -config=config.json -port=8080
```

Vereist: Go 1.25+

## Configuratie

Kopieer [`config.example.json`](config.example.json) naar `config.json`. De belangrijkste secties:

| Sectie | Wat configureer je? |
|--------|---------------------|
| `database` | PostgreSQL-verbinding (host, poort, credentials, schema) |
| `image` | Doelafmetingen en JPEG-kwaliteit voor geüploade afbeeldingen |
| `api` | API-sleutels voor authenticatie |
| `maintenance` | Thresholds en automatische scheduler voor databaseonderhoud |
| `backup` | Pad naar backups, retentie, scheduler en optionele S3-sync |
| `notifications` | E-mailmeldingen via Microsoft Graph API |
| `log` | Logniveau (`debug`, `info`, `warn`, `error`) en format (`text`, `json`) |

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

### Automatisch onderhoud

Database-onderhoud (VACUUM/ANALYZE) kan automatisch worden uitgevoerd:

```json
"maintenance": {
  "scheduler": {
    "enabled": true,
    "schedule": "0 4 * * 0"
  }
}
```

Dit draait elke zondag om 04:00 VACUUM ANALYZE op tabellen die het nodig hebben. Zie [API.md](API.md) voor details.

### E-mailnotificaties

Ontvang e-mailmeldingen bij mislukte backups, S3-synchronisatie of database-onderhoud. Vereist een Azure AD app-registratie met `Mail.Send` permissie.

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
- **Foutmeldingen:** bij mislukte backup, S3-sync of onderhoud
- **Herstelmeldingen:** wanneer een eerder mislukte operatie weer slaagt

Test de configuratie via `POST /api/notifications/test-email`.

## Voorbeelden

```bash
# Health check
curl http://localhost:8080/api/health

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
