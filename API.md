# Aeron Toolbox API-documentatie

## Inhoudsopgave

- [Overzicht](#overzicht)
- [Snel overzicht endpoints](#snel-overzicht-endpoints)
- [Authenticatie](#authenticatie)
- [Response-formaat](#response-formaat)
- [Foutmeldingen](#foutmeldingen)
- [Endpoints](#endpoints)
  - [Statuscontrole](#statuscontrole)
  - [Artiestendpoints](#artiestendpoints)
  - [Trackendpoints](#trackendpoints)
  - [Playlist-endpoints](#playlist-endpoints)
  - [Database onderhoud](#database-onderhoud)
  - [Backup-endpoints](#backup-endpoints)
  - [Bestandscontrole](#bestandscontrole)
  - [Mediabestandcontrole](#mediabestandcontrole)
  - [Notificaties](#notificaties)
- [Codevoorbeelden](#codevoorbeelden)
- [Configuratie](#configuratie)

## Overzicht

De Aeron Toolbox API biedt RESTful-endpoints voor het Aeron-radioautomatiseringssysteem. De API biedt directe databasetoegang voor afbeeldingenbeheer, mediabrowser, database-onderhoud en backup-management.

**Basis-URL:** `http://localhost:8080/api`

## Snel overzicht endpoints

| Endpoint | Methode | Beschrijving | Auth |
|----------|---------|--------------|------|
| **Algemeen** |
| `/api/health` | GET | API-status controleren | Nee |
| **Artiesten** |
| `/api/artists` | GET | Statistieken over artiesten | Ja |
| `/api/artists/{id}` | GET | Specifieke artiest ophalen | Ja |
| `/api/artists/{id}/image` | GET | Artiestafbeelding ophalen | Ja |
| `/api/artists/{id}/image` | POST | Artiestafbeelding uploaden | Ja |
| `/api/artists/{id}/image` | DELETE | Artiestafbeelding verwijderen | Ja |
| `/api/artists/bulk-delete` | DELETE | Alle artiestafbeeldingen verwijderen | Ja |
| **Tracks** |
| `/api/tracks` | GET | Statistieken over tracks | Ja |
| `/api/tracks/{id}` | GET | Specifieke track ophalen | Ja |
| `/api/tracks/{id}/image` | GET | Trackafbeelding ophalen | Ja |
| `/api/tracks/{id}/image` | POST | Trackafbeelding uploaden | Ja |
| `/api/tracks/{id}/image` | DELETE | Trackafbeelding verwijderen | Ja |
| `/api/tracks/bulk-delete` | DELETE | Alle trackafbeeldingen verwijderen | Ja |
| **Playlist** |
| `/api/playlist` | GET | Playlistblokken voor datum | Ja |
| `/api/playlist?block_id={id}` | GET | Tracks in playlistblok | Ja |
| **Database onderhoud** |
| `/api/db/maintenance/health` | GET | Database health en statistieken | Ja |
| **Bestandscontrole** |
| `/api/file-monitor/status` | GET | Status bestandscontrole | Ja |
| `/api/file-monitor/check` | POST | Handmatige bestandscontrole starten | Ja |
| **Mediabestandcontrole** |
| `/api/media/files/check` | POST | Controle op ontbrekende audiobestanden starten | Ja |
| `/api/media/files/check/status` | GET | Resultaat van de mediabestandcontrole | Ja |
| **Backups** |
| `/api/db/backup` | POST | Nieuwe backup aanmaken | Ja |
| `/api/db/backup/status` | GET | Backup status opvragen | Ja |
| `/api/db/backups` | GET | Lijst van alle backups | Ja |
| `/api/db/backups/{filename}` | GET | Specifieke backup downloaden | Ja |
| `/api/db/backups/{filename}/validate` | GET | Backup integriteit valideren | Ja |
| `/api/db/backups/{filename}` | DELETE | Backup verwijderen | Ja |
| **Notificaties** |
| `/api/notifications/test-email` | POST | Test e-mail versturen | Ja |

## Authenticatie

Wanneer authenticatie is ingeschakeld in de configuratie, vereisen alle endpoints (behalve `GET /api/health`) een API-sleutel.

**Header:** `X-API-Key: jouw-api-sleutel`

**Response bij ontbrekende autorisatie:**
```json
{
  "success": false,
  "error": "Unauthorized: invalid or missing API key"
}
```

## Algemene response-headers

JSON-responses bevatten:
- `Content-Type: application/json; charset=utf-8`

Succesvolle binaire responses, zoals afbeeldingsdownloads en backupdownloads, gebruiken het passende bestandstype in plaats van JSON. Foutresponses voor deze endpoints zijn wel JSON.

## Response-formaat

Alle JSON-responses gebruiken een consistent wrapper-formaat:
```json
{
  "success": true,
  "data": { ... }  // Bij succesvolle requests
}
```

Of bij fouten:
```json
{
  "success": false,
  "error": "error message"
}
```

> [!NOTE]
> In de JSON-voorbeelden hieronder wordt voor de leesbaarheid alleen de inhoud van het `data`-veld getoond, en bij foutresponses alleen het `error`-veld. JSON-responses gebruiken in werkelijkheid de complete wrapper (inclusief `"success"`). Succesvolle binaire responses gebruiken geen JSON-wrapper.

## Foutmeldingen

Alle fouten volgen dit formaat:
```json
{
  "success": false,
  "error": "error message"
}
```

**HTTP-statuscodes:**
- `400` Bad Request - Ongeldige invoerparameters
- `401` Unauthorized - Ongeldige of ontbrekende API-sleutel
- `404` Not Found - Bron niet gevonden
- `409` Conflict - Operatie al bezig (backup)
- `500` Internal Server Error - Serverfout

---

## Endpoints

### Statuscontrole

Controleer de status van de API.

**Endpoint:** `GET /api/health`
**Authenticatie:** Niet vereist

**Response:** `200 OK`
```json
{
  "success": true,
  "data": {
    "status": "healthy",
    "version": "dev",
    "database": "aeron",
    "database_status": "connected",
    "notifications": {
      "configured": true,
      "secret_expiry": {
        "expires_at": "2026-12-01T00:00:00Z",
        "expires_soon": false,
        "days_left": 245
      }
    },
    "file_monitor": {
      "enabled": true,
      "checks_total": 2,
      "checks_stale": 0,
      "checks_alerting": 0
    }
  }
}
```

Het `notifications`-veld is altijd aanwezig en toont:
- `configured`: Of de e-mailnotificatie is geconfigureerd
- `last_error` / `last_error_at`: Laatste fout bij het versturen van e-mail (alleen bij fouten)
- `secret_expiry`: Verloopinformatie van het Azure AD client secret (alleen indien geconfigureerd)
  - `expires_at`: Vervaldatum
  - `expires_soon`: `true` wanneer het secret binnen 30 dagen verloopt
  - `days_left`: Aantal resterende dagen
  - `error`: Foutmelding bij ophalen (bijv. onvoldoende rechten)

Het `file_monitor`-veld is alleen aanwezig wanneer de bestandscontrole is ingeschakeld en toont:
- `enabled`: Of de bestandscontrole is geconfigureerd
- `checks_total`: Totaal aantal geconfigureerde checks
- `checks_stale`: Ruwe telling van bestanden die te oud zijn of niet bereikbaar zijn (inclusief buiten `active_window`)
- `checks_alerting`: Window-aware telling; bestanden buiten hun `active_window` tellen hier niet mee

De `status`-waarden hebben een vaste prioriteitsvolgorde: `"unhealthy"` > `"degraded"` > `"healthy"`.

| Conditie | Status |
|---|---|
| `database_status` is `"disconnected"` | `"unhealthy"` (hoogste prioriteit) |
| `expires_soon` is `true` | `"degraded"` |
| `checks_alerting` groter dan `0` | `"degraded"` |
| Geen van bovenstaande | `"healthy"` |

Een database-uitval overschrijft altijd een eventuele `"degraded"`-signaal van notificaties of de bestandscontrole.

---

## Artiestendpoints

### Artieststatistieken ophalen

Bekijk statistieken over artiesten en hun afbeeldingen.

**Endpoint:** `GET /api/artists`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "total": 1250,
  "with_images": 450,
  "without_images": 800
}
```

### Artiest ophalen via ID

Bekijk artiestgegevens inclusief afbeeldingsstatus.

**Endpoint:** `GET /api/artists/{id}`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Artiest-UUID

**Response:** `200 OK`
```json
{
  "artistid": "123e4567-e89b-12d3-a456-426614174000",
  "artist": "The Beatles",
  "info": "Britse rockband uit Liverpool",
  "website": "https://www.thebeatles.com",
  "twitter": "thebeatles",
  "instagram": "thebeatles",
  "has_image": true,
  "repeat_value": 0
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "artist with ID '123e4567-e89b-12d3-a456-426614174000' not found"
}
```

### Artiestafbeelding ophalen

Bekijk de afbeelding van de artiest.

**Endpoint:** `GET /api/artists/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Artiest-UUID

**Response:** `200 OK`
- Content-Type: `image/jpeg`, `image/png` of `image/webp`
- Binaire afbeeldingsdata

**Foutresponse:** `404 Not Found`
```json
{
  "error": "artist image with ID '123e4567-e89b-12d3-a456-426614174000' not found"
}
```

### Artiestafbeelding uploaden

Een artiestafbeelding uploaden of bijwerken.

**Endpoint:** `POST /api/artists/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Artiest-UUID

**Request Body:**
```json
{
  "url": "https://voorbeeld.nl/artiest.jpg",
  "image": "base64-gecodeerde-afbeeldingsdata"
}
```
*Let op: Gebruik óf `url` óf `image`, niet beide tegelijk*

**Response:** `200 OK`
```json
{
  "artist": "The Beatles",
  "original_size": 245678,
  "optimized_size": 45678,
  "savings_percent": 81.4
}
```

**Foutresponses:**
- `400` Bad Request - Ongeldige invoer of afbeeldingsvalidatie mislukt
- `404` Not Found - Artiest niet gevonden

### Artiestafbeelding verwijderen

Het verwijderen van een artiestafbeelding.

**Endpoint:** `DELETE /api/artists/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Artiest-UUID

**Response:** `200 OK`
```json
{
  "message": "artist image deleted successfully",
  "artist_id": "123e4567-e89b-12d3-a456-426614174000"
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "artist image with ID '123e4567-e89b-12d3-a456-426614174000' not found"
}
```

### Bulkverwijdering artiestafbeeldingen

Het verwijderen van alle artiestafbeeldingen uit de database.

**Endpoint:** `DELETE /api/artists/bulk-delete`
**Authenticatie:** Vereist

**Vereiste header:**
- `X-Confirm-Bulk-Delete: DELETE ALL`

**Response:** `200 OK`
```json
{
  "deleted": 450,
  "message": "450 artist images deleted"
}
```

**Foutresponse:** `400 Bad Request`
```json
{
  "error": "Missing confirmation header: X-Confirm-Bulk-Delete"
}
```

---

## Trackendpoints

### Trackstatistieken ophalen

Bekijk statistieken over tracks en hun afbeeldingen.

**Endpoint:** `GET /api/tracks`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "total": 5000,
  "with_images": 1200,
  "without_images": 3800
}
```

### Track ophalen via ID

Bekijk trackgegevens inclusief afbeeldingsstatus.

**Endpoint:** `GET /api/tracks/{id}`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Track-UUID

**Response:** `200 OK`
```json
{
  "titleid": "456e7890-e89b-12d3-a456-426614174000",
  "tracktitle": "Hey Jude",
  "artist": "The Beatles",
  "artistid": "123e4567-e89b-12d3-a456-426614174000",
  "year": 1968,
  "knownlength": 431000,
  "introtime": 8000,
  "outrotime": 120000,
  "tempo": 75,
  "bpm": 75,
  "gender": 0,
  "language": 2,
  "mood": 1,
  "exporttype": 0,
  "repeat_value": 0,
  "rating": 5,
  "has_image": true,
  "website": "",
  "conductor": "",
  "orchestra": ""
}
```

**Veldverklaringen:**
- `knownlength`, `introtime`, `outrotime`: Duur in milliseconden
- `tempo`, `bpm`: Tempo/BPM van de track
- `gender`, `language`, `mood`: Numerieke classificatiecodes
- `rating`: Waardering (0-5)
- `repeat_value`: Herhalingswaarde voor scheduling

**Foutresponse:** `404 Not Found`
```json
{
  "error": "track with ID '456e7890-e89b-12d3-a456-426614174000' not found"
}
```

### Trackafbeelding ophalen

Bekijk de albumhoes van de track.

**Endpoint:** `GET /api/tracks/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Track-UUID

**Response:** `200 OK`
- Content-Type: `image/jpeg`, `image/png` of `image/webp`
- Binaire afbeeldingsdata

**Foutresponse:** `404 Not Found`
```json
{
  "error": "track image with ID '456e7890-e89b-12d3-a456-426614174000' not found"
}
```

### Trackafbeelding uploaden

Een albumhoes uploaden of bijwerken.

**Endpoint:** `POST /api/tracks/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Track-UUID

**Request Body:**
```json
{
  "url": "https://voorbeeld.nl/albumhoes.jpg",
  "image": "base64-gecodeerde-afbeeldingsdata"
}
```
*Let op: Gebruik óf `url` óf `image`, niet beide tegelijk*

**Response:** `200 OK`
```json
{
  "artist": "The Beatles",
  "track": "Hey Jude",
  "original_size": 345678,
  "optimized_size": 65678,
  "savings_percent": 81.0
}
```

**Foutresponses:**
- `400` Bad Request - Ongeldige invoer of afbeeldingsvalidatie mislukt
- `404` Not Found - Track niet gevonden

### Trackafbeelding verwijderen

Het verwijderen van de albumhoes van een track.

**Endpoint:** `DELETE /api/tracks/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Track-UUID

**Response:** `200 OK`
```json
{
  "message": "track image deleted successfully",
  "track_id": "456e7890-e89b-12d3-a456-426614174000"
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "track image with ID '456e7890-e89b-12d3-a456-426614174000' not found"
}
```

### Bulkverwijdering trackafbeeldingen

Het verwijderen van alle trackafbeeldingen uit de database.

**Endpoint:** `DELETE /api/tracks/bulk-delete`
**Authenticatie:** Vereist

**Vereiste header:**
- `X-Confirm-Bulk-Delete: DELETE ALL`

**Response:** `200 OK`
```json
{
  "deleted": 1200,
  "message": "1200 track images deleted"
}
```

**Foutresponse:** `400 Bad Request`
```json
{
  "error": "Missing confirmation header: X-Confirm-Bulk-Delete"
}
```

---

## Playlist-endpoints

### Playlistblokken ophalen

Bekijk alle playlistblokken voor een specifieke datum.

**Endpoint:** `GET /api/playlist`
**Authenticatie:** Vereist

**Queryparameters:**
- `date` (optioneel): Datum in YYYY-MM-DD-indeling (standaard: vandaag)

**Response:** `200 OK`
```json
[
  {
    "blockid": "block-uuid-1",
    "name": "Ochtend Show",
    "date": "2025-09-17",
    "start_time": "06:00:00",
    "end_time": "10:00:00",
    "tracks": [
      {
        "trackid": "track-uuid-1",
        "tracktitle": "Nummer Titel",
        "artistid": "artist-uuid-1",
        "artistname": "Artiest Naam",
        "start_time": "06:00:00",
        "end_time": "06:03:24",
        "duration": 204000,
        "has_track_image": true,
        "has_artist_image": false,
        "exporttype": 0,
        "mode": 2,
        "is_voicetrack": false,
        "is_commblock": false
      }
    ]
  }
]
```

### Playlisttracks per blok ophalen

Bekijk tracks voor een specifiek playlistblok.

**Endpoint:** `GET /api/playlist?block_id={block_id}`
**Authenticatie:** Vereist

**Queryparameters:**
- `block_id` (vereist): Playlistblok-UUID
- `limit` (optioneel): Maximaal aantal tracks (standaard: 1000)
- `offset` (optioneel): Offset voor paginering (standaard: 0)
- `track_image` (optioneel): Filter op trackafbeeldingsstatus (`true`/`false`/`yes`/`no`/`1`/`0`)
- `artist_image` (optioneel): Filter op artiestafbeeldingsstatus (`true`/`false`/`yes`/`no`/`1`/`0`)
- `sort` (optioneel): Sorteerveld (`start_time`, `track`, `artist`, `duration`)
- `desc` (optioneel): Sorteer aflopend indien `true`

**Response:** `200 OK`
```json
[
  {
    "trackid": "track-uuid-1",
    "tracktitle": "Nummer Titel",
    "artistid": "artist-uuid-1",
    "artistname": "Artiest Naam",
    "start_time": "06:00:00",
    "end_time": "06:03:24",
    "duration": 204000,
    "has_track_image": true,
    "has_artist_image": false,
    "exporttype": 0,
    "mode": 2,
    "is_voicetrack": false,
    "is_commblock": false
  }
]
```

---

## Database onderhoud

### Database health ophalen

Bekijk gedetailleerde databasestatistieken inclusief tabelgroottes, bloat-percentages, connectiegebruik, langlopende queries en onderhoudsaanbevelingen.

**Endpoint:** `GET /api/db/maintenance/health`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "database_name": "aeron",
  "database_version": "PostgreSQL 16.1",
  "database_size": "2.45 GB",
  "database_size_bytes": 2630451200,
  "schema_name": "aeron",
  "active_connections": 12,
  "max_connections": 100,
  "connection_usage_pct": 12.0,
  "tables": [
    {
      "name": "track",
      "row_count": 125000,
      "dead_tuples": 4500,
      "dead_tuple_pct": 3.6,
      "modifications_since_analyze": 1250,
      "total_size": "1.2 GB",
      "total_size_bytes": 1288490188,
      "table_size": "1.0 GB",
      "table_size_bytes": 1073741824,
      "index_size": "150 MB",
      "index_size_bytes": 157286400,
      "toast_size": "50 MB",
      "toast_size_bytes": 52428800,
      "last_vacuum": "2025-12-20T03:00:00Z",
      "last_autovacuum": "2025-12-21T04:15:00Z",
      "last_analyze": "2025-12-20T03:00:00Z",
      "last_autoanalyze": "2025-12-21T04:15:00Z",
      "seq_scans": 1250,
      "idx_scans": 45000,
      "needs_vacuum": true,
      "needs_analyze": false
    }
  ],
  "long_running_queries": [],
  "needs_maintenance": true,
  "recommendations": [
    "Table 'playlistitem' has 15.2% dead tuples - VACUUM recommended",
    "Table 'artist' has 12500 dead tuples - VACUUM recommended"
  ],
  "checked_at": "2025-12-22T14:30:00Z"
}
```

> **Breaking change:** het veld `dead_tuple_ratio` in de tabelresponse is hernoemd naar `dead_tuple_pct`. De waarde was altijd al een percentage (0–100), niet een ratio (0–1); de naam is gecorrigeerd om verwarring te voorkomen. Externe afnemers moeten de nieuwe veldnaam gebruiken.

### Automatische health check

De health check kan automatisch worden uitgevoerd via de ingebouwde scheduler. Bij problemen wordt een e-mailmelding verstuurd. Configureer dit in `config.json`:

```json
"maintenance": {
  "bloat_threshold": 10.0,
  "dead_tuple_threshold": 10000,
  "connection_usage_threshold_pct": 80,
  "long_query_threshold_seconds": 10,
  "scheduler": {
    "enabled": true,
    "schedule": "0 4 * * 0"
  }
}
```

**Parameters:**
- `bloat_threshold`: Percentage dead tuples waarboven een waarschuwing wordt gegeven
- `dead_tuple_threshold`: Absoluut aantal dead tuples waarboven een waarschuwing wordt gegeven
- `connection_usage_threshold_pct`: Percentage connectiegebruik waarboven een waarschuwing wordt gegeven (standaard: 80)
- `long_query_threshold_seconds`: Drempel in seconden waarboven een query als langlopend wordt beschouwd (standaard: 10)
- `scheduler.enabled`: Schakel automatische health checks in/uit
- `scheduler.schedule`: Cron-expressie (zie backup-sectie voor voorbeelden)

Bij detectie van problemen (hoge bloat, veel connecties, langlopende queries) wordt een e-mailmelding verstuurd. Wanneer alle problemen zijn opgelost, volgt een herstelmelding. De tijdzone wordt bepaald door de systeemtijdzone (instelbaar via `TZ` environment variable).

---

## Backup-endpoints

> [!WARNING]
> Backup-endpoints zijn alleen beschikbaar indien `backup.enabled: true` in de configuratie.

> [!IMPORTANT]
> **Systeemvereisten:** Bij het opstarten valideert de applicatie of `pg_dump` en `pg_restore` beschikbaar zijn. Zonder deze tools weigert de applicatie te starten. Zie de README voor installatie-instructies.

### Backup workflow

Backups worden asynchroon uitgevoerd:

1. **Backup starten:** `POST /api/db/backup` → retourneert direct `202 Accepted`
2. **Status controleren:** `GET /api/db/backup/status` → toont voortgang en eventuele fouten
3. **Backup downloaden:** `GET /api/db/backups/{filename}` → download het bestand

**Automatische validatie:**
Na het aanmaken van een backup wordt deze automatisch gevalideerd via `pg_restore --list` (controleert TOC en checksums). Alleen gevalideerde backups worden als succesvol gemarkeerd en naar S3 gesynchroniseerd.

Deze aanpak biedt voordelen:
- Request retourneert direct (geen timeout issues)
- Fouten zijn zichtbaar via het status endpoint
- Er kan slechts één backup tegelijk draaien
- Bij connectieverlies loopt backup door op de server
- Corrupte backups worden gedetecteerd vóór S3 sync

### Automatische backups

Backups kunnen automatisch worden uitgevoerd via de ingebouwde scheduler. Configureer dit in `config.json`:

```json
"backup": {
  "timeout_minutes": 30,
  "scheduler": {
    "enabled": true,
    "schedule": "0 3 * * *"
  }
}
```

**Parameters:**
- `timeout_minutes`: Maximale tijd voor pg_dump (standaard: 30 minuten)
- `pg_dump_path`: Custom pad naar pg_dump executable (leeg = automatische detectie via PATH)
- `pg_restore_path`: Custom pad naar pg_restore executable (leeg = automatische detectie via PATH)
- `enabled`: Schakel automatische backups in/uit
- `schedule`: Cron-expressie voor het backup-schema

De tijdzone voor alle geplande taken (backup én onderhoud) wordt bepaald door de systeemtijdzone. In Docker: stel `TZ=Europe/Amsterdam` in als environment variable.

**Cron-expressieformaat:** `minuut uur dag maand weekdag`

| Expressie | Betekenis |
|-----------|-----------|
| `0 3 * * *` | Elke dag om 3:00 |
| `0 */6 * * *` | Elke 6 uur |
| `0 3 * * 0` | Elke zondag om 3:00 |
| `0 3 1 * *` | 1e van elke maand om 3:00 |

### S3 synchronisatie

Backups kunnen automatisch worden gesynchroniseerd naar S3-compatibele storage (AWS S3, MinIO, Backblaze B2, DigitalOcean Spaces). Configureer dit in `config.json`:

```json
"backup": {
  "s3": {
    "enabled": true,
    "bucket": "mijn-backups",
    "region": "eu-west-1",
    "endpoint": "",
    "access_key_id": "AKIAIOSFODNN7EXAMPLE",
    "secret_access_key": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
    "path_prefix": "aeron/backups/",
    "force_path_style": false
  }
}
```

**Parameters:**
- `enabled`: Schakel S3 synchronisatie in/uit
- `bucket`: S3 bucket naam
- `region`: AWS regio (bijv. `eu-west-1`)
- `endpoint`: Custom endpoint voor S3-compatibele services (optioneel)
- `access_key_id`: AWS access key ID
- `secret_access_key`: AWS secret access key
- `path_prefix`: Prefix voor S3 keys (optioneel, bijv. `backups/`)
- `force_path_style`: Gebruik path-style URLs (vereist voor MinIO)

**Voorbeeld voor MinIO:**
```json
"s3": {
  "enabled": true,
  "bucket": "backups",
  "region": "us-east-1",
  "endpoint": "http://minio.local:9000",
  "access_key_id": "minioadmin",
  "secret_access_key": "minioadmin",
  "path_prefix": "",
  "force_path_style": true
}
```

**Gedrag:**
- Na elke succesvolle backup wordt het bestand asynchroon naar S3 geüpload
- Bij het verwijderen van lokale backups (handmatig of door retention) wordt ook de S3-kopie verwijderd
- S3-fouten blokkeren de backup niet; de status is zichtbaar via `GET /api/db/backup/status`
- Uploads gebruiken multipart voor grote bestanden

### Backup starten

Een nieuwe databasebackup starten op de achtergrond.

**Endpoint:** `POST /api/db/backup`
**Authenticatie:** Vereist

**Request Body:**
```json
{
  "compression": 9
}
```

**Parameters:**
- `compression` (optioneel): Laat weg of zet op `0` om `backup.default_compression` te gebruiken; expliciete pg_dump-compressieniveaus zijn `1-9`.

**Response:** `202 Accepted`
```json
{
  "message": "Backup started in background",
  "check": "/api/db/backup/status"
}
```

De backup wordt asynchroon uitgevoerd. Controleer `GET /api/db/backup/status` voor de voortgang.

> [!WARNING]
> Er kan slechts één backup tegelijk draaien. Een tweede aanvraag tijdens een lopende backup retourneert een fout.

**Foutresponses:**

`404 Not Found` - Backup niet ingeschakeld:
```json
{
  "error": "backup is not enabled"
}
```

`409 Conflict` - Backup al bezig:
```json
{
  "error": "backup already in progress"
}
```

### Backup status

Toont de status van de laatste backupbewerking.

**Endpoint:** `GET /api/db/backup/status`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "backup is not enabled"
}
```

**Response tijdens backup:** `200 OK`
```json
{
  "running": true,
  "started_at": "2024-01-15T03:00:00Z",
  "filename": "aeron-backup-2024-01-15-030000.dump"
}
```

**Response na succesvolle backup:** `200 OK`
```json
{
  "running": false,
  "started_at": "2024-01-15T03:00:00Z",
  "ended_at": "2024-01-15T03:00:45Z",
  "success": true,
  "filename": "aeron-backup-2024-01-15-030000.dump"
}
```

**Response na succesvolle backup met S3 sync:** `200 OK`
```json
{
  "running": false,
  "started_at": "2024-01-15T03:00:00Z",
  "ended_at": "2024-01-15T03:00:45Z",
  "success": true,
  "filename": "aeron-backup-2024-01-15-030000.dump",
  "s3_sync": {
    "synced": true
  }
}
```

**Response na mislukte backup:** `200 OK`
```json
{
  "running": false,
  "started_at": "2024-01-15T03:00:00Z",
  "ended_at": "2024-01-15T03:00:05Z",
  "success": false,
  "error": "create backup failed: backup timeout after 30m0s (configure backup.timeout_minutes)",
  "filename": "aeron-backup-2024-01-15-030000.dump"
}
```

**Response met S3 sync fout:** `200 OK`
```json
{
  "running": false,
  "started_at": "2024-01-15T03:00:00Z",
  "ended_at": "2024-01-15T03:00:45Z",
  "success": true,
  "filename": "aeron-backup-2024-01-15-030000.dump",
  "s3_sync": {
    "synced": false,
    "error": "S3 upload failed: backups/aeron-backup-2024-01-15-030000.dump: ..."
  }
}
```

**Velden:**
- `running`: Of er momenteel een backup draait
- `started_at`: Starttijd van de laatste backup
- `ended_at`: Eindtijd (alleen aanwezig na voltooiing)
- `success`: Of de backup geslaagd is (alleen aanwezig na voltooiing)
- `error`: Foutmelding (alleen aanwezig bij mislukking)
- `filename`: Bestandsnaam (kan leeg zijn bij vroege fouten)
- `s3_sync`: S3 synchronisatiestatus (alleen aanwezig indien S3 is ingeschakeld)
  - `synced`: Of de backup naar S3 is geüpload
  - `error`: Foutmelding bij sync-fout

### Lijst van backups ophalen

Bekijk een overzicht van alle beschikbare backups.

**Endpoint:** `GET /api/db/backups`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "backup is not enabled"
}
```

**Response:** `200 OK`
```json
{
  "backups": [
    {
      "filename": "aeron-backup-2025-12-22-143000.dump",
      "size_bytes": 52428800,
      "size": "50.0 MB",
      "created_at": "2025-12-22T14:30:00Z"
    },
    {
      "filename": "aeron-backup-2025-12-21-143000.dump",
      "size_bytes": 125829120,
      "size": "120.0 MB",
      "created_at": "2025-12-21T14:30:00Z"
    }
  ],
  "total_size_bytes": 178257920,
  "total_count": 2
}
```

### Specifieke backup downloaden

Een specifiek backupbestand downloaden.

**Endpoint:** `GET /api/db/backups/{filename}`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "backup is not enabled"
}
```

**Parameters:**
- `filename` (padparameter, vereist): Naam van het backupbestand

**Response:** `200 OK`
- Content-Type: `application/octet-stream`
- Content-Disposition: `attachment; filename=...`
- Binaire backup data

**Foutresponse:** `404 Not Found`
```json
{
  "error": "backup with ID 'aeron-backup-2025-12-22-143000.dump' not found"
}
```

### Backup verwijderen

Een specifiek backupbestand verwijderen.

**Endpoint:** `DELETE /api/db/backups/{filename}`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "backup is not enabled"
}
```

**Parameters:**
- `filename` (padparameter, vereist): Naam van het backupbestand

**Vereiste header:**
- `X-Confirm-Delete: {filename}` (bestandsnaam moet overeenkomen)

**Response:** `200 OK`
```json
{
  "message": "Backup deleted successfully",
  "filename": "aeron-backup-2025-12-21T14-30-00.dump"
}
```

**Foutresponse:** `400 Bad Request`
```json
{
  "error": "Confirmation header missing: X-Confirm-Delete must contain the filename"
}
```

### Backup valideren

De integriteit van een bestaand backupbestand valideren. Handig voor het controleren van backups na download of herstel van S3.

**Endpoint:** `GET /api/db/backups/{filename}/validate`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "backup is not enabled"
}
```

**Parameters:**
- `filename` (padparameter, vereist): Naam van het backupbestand

**Response:** `200 OK`
```json
{
  "filename": "aeron-backup-2025-12-22-143000.dump",
  "valid": true
}
```

**Response bij ongeldige backup:** `200 OK`
```json
{
  "filename": "aeron-backup-2025-12-22-143000.dump",
  "valid": false,
  "error": "backup validation failed: file is corrupt or unreadable: pg_restore: error: ..."
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "backup with ID 'aeron-backup-2025-12-22-143000.dump' not found"
}
```

Validatie gebeurt via `pg_restore --list` die de TOC en interne checksums controleert.

---

## Bestandscontrole

De bestandscontrole bewaakt bestanden op schijf en signaleert wanneer ze ouder zijn dan een geconfigureerde maximale leeftijd. Dit is handig om mislukte downloads of updates vanuit externe processen te detecteren, zoals nieuwsbulletins of weerberichten.

Controles draaien automatisch met een vast interval. Standaard is dat 60 seconden; dit is aan te passen via `file_monitor.interval_seconds`. Na een herstart geldt de eerste controle als een "grace run": de resultaten worden wel gemeten, maar er worden nog geen notificaties verstuurd. Zo voorkom je valse alarmen.

> [!IMPORTANT]
> **Breaking change:** het veld `interval_minutes` in de statusresponse is vervangen door `interval_seconds`. Externe afnemers moeten de nieuwe veldnaam gebruiken.

### Status van de bestandscontrole

Toont de resultaten van de meest recente bestandscontrole, plus de huidige runstatus.

**Endpoint:** `GET /api/file-monitor/status`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "file monitor is not enabled"
}
```

**Response:** `200 OK`
```json
{
  "running": false,
  "run_id": 42,
  "completed_run_id": 42,
  "started_at": "2024-01-15T10:29:55Z",
  "last_check_at": "2024-01-15T10:30:00Z",
  "interval_seconds": 60,
  "checks": [
    {
      "name": "Nieuws bulletin",
      "path": "/data/news.mp3",
      "max_age_minutes": 10,
      "file_exists": true,
      "file_age_minutes": 7.5,
      "last_modified": "2024-01-15T10:22:30Z",
      "is_stale": false,
      "in_alert": false
    },
    {
      "name": "Weer",
      "path": "/data/weather.mp3",
      "max_age_minutes": 60,
      "file_exists": true,
      "file_age_minutes": 75.2,
      "last_modified": "2024-01-15T09:15:00Z",
      "is_stale": true,
      "in_alert": true
    }
  ]
}
```

De volgende voorbeelden tonen losse items uit de `checks`-array voor specifieke situaties:

**Check-item voor een ontbrekend bestand:**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "file_exists": false,
  "is_stale": true,
  "in_alert": true,
  "error_kind": "not_found"
}
```

**Check-item bij een stat-fout (bijvoorbeeld geen rechten):**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "file_exists": null,
  "is_stale": true,
  "in_alert": true,
  "error": "stat /data/news.mp3: permission denied",
  "error_kind": "permission_denied"
}
```

**Check-item bij een algemene stat-fout (bijvoorbeeld een I/O-fout):**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "file_exists": null,
  "is_stale": true,
  "in_alert": true,
  "error": "stat /data/news.mp3: input/output error",
  "error_kind": "stat_error"
}
```

**Check-item bij een stat-time-out (bijvoorbeeld een vastgelopen NFS-mount):**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "is_stale": true,
  "in_alert": true,
  "error": "stat timeout after 5s",
  "error_kind": "stat_timeout"
}
```

**Velden op topniveau:**
- `running`: Of er op dit moment een run bezig is.
- `run_id`: Monotone server-side identifier van de huidige of meest recent gestarte run (`0` als de service nog nooit heeft gedraaid).
- `completed_run_id`: Identifier van de run waarvan de resultaten zichtbaar zijn in `checks` en `last_check_at`. Zie het pollingrecept hieronder.
- `started_at`: Starttijd van de huidige of meest recente run.
- `last_check_at`: Eindtijd van de meest recente run.
- `interval_seconds`: Geconfigureerd pollinginterval in seconden (standaard 60).
- `checks`: Array met resultaten per bestand.

**Velden per check:**
- `name`: Optionele weergavenaam uit de configuratie.
- `path`: Bestandspad op schijf.
- `max_age_minutes`: Maximaal toegestane leeftijd.
- `file_exists`: Of het bestand bestaat (`true`, `false`, of `null` bij fouten).
- `file_age_minutes`: Leeftijd in minuten (ontbreekt als het bestand ontbreekt of niet bereikbaar is).
- `last_modified`: Laatste wijzigingstijd (ontbreekt als het bestand ontbreekt of niet bereikbaar is).
- `is_stale`: Of het bestand te oud is of niet bereikbaar is.
- `in_alert`: Of voor dit bestand momenteel een alert actief is. Buiten de geconfigureerde `active_window` is dit altijd `false`, ook als `is_stale` `true` is.
- `error`: Leesbare foutmelding bij toegangsproblemen (ontbreekt bij normaal gebruik).
- `error_kind`: Classificatie van het fouttype: `""` (succes), `not_found`, `permission_denied`, `stat_timeout` of `stat_error`.

> [!NOTE]
> Het veld `file_exists` is nullable: `true` = bestand bestaat, `false` = bestand niet gevonden, `null` = onbekend (bijvoorbeeld bij een permissiefout). Gebruik het veld `error` voor details als `file_exists` `null` is.

### Handmatig een bestandscontrole starten

Start een bestandscontrole op de achtergrond. Handig tijdens configuratie of storingsonderzoek, zodat operators niet hoeven te wachten op de volgende geplande tick.

**Endpoint:** `POST /api/file-monitor/check`
**Authenticatie:** Vereist

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "file monitor is not enabled"
}
```

**Response:** `202 Accepted`
```json
{
  "message": "File monitor check started",
  "run_id": 43,
  "check": "/api/file-monitor/status"
}
```

**Error response:** `409 Conflict`
```json
{
  "error": "file monitor check already running"
}
```

De handmatige trigger en de cronjob gebruiken dezelfde single-flight gate. Daardoor geeft een handmatige call `409` terug zolang een geplande run nog bezig is, en andersom. Dubbele alert- of herstelmails kunnen dus niet ontstaan.

**Pollingrecept (server-side correlatie, zonder afhankelijkheid van systeemtijd):**

1. Lees `run_id` uit de response van `POST /check` en noem die waarde `myRunID`.
2. Poll `GET /api/file-monitor/status`.
3. De run is klaar **en de zichtbare checks zijn door jouw run geproduceerd** zodra `completed_run_id >= myRunID && running == false`.

Strikte gelijkheid (`completed_run_id == myRunID`) bevestigt dat de zichtbare `checks` exact door jouw run zijn geproduceerd. Een hogere waarde betekent dat een latere run, bijvoorbeeld via cron, jouw run heeft ingehaald. Dat is prima voor de vraag "is het systeem nu gezond?", maar verliest de exacte correlatie. Gebruik voor nauwkeurige troubleshooting daarom de strikte vergelijking en houd rekening met een mogelijke race.

### Integratie met de health-endpoint (bestandscontrole)

Als de bestandscontrole is ingeschakeld, geeft de health-endpoint (`GET /api/health`) een extra `file_monitor`-blok terug:

```json
{
  "status": "degraded",
  "version": "1.0.0",
  "database": "aeron",
  "database_status": "connected",
  "file_monitor": {
    "enabled": true,
    "checks_total": 2,
    "checks_stale": 1,
    "checks_alerting": 1
  }
}
```

- `checks_stale`: ruwe telling van bestanden die te oud zijn of niet bereikbaar zijn, inclusief bestanden buiten hun `active_window`.
- `checks_alerting`: window-aware telling; bestanden buiten hun `active_window` tellen hier niet mee. Wanneer de database verbonden is en `checks_alerting > 0`, wordt de algemene status `"degraded"` (`"unhealthy"` heeft altijd prioriteit). Een bestand dat 's nachts verouderd raakt maar alleen overdag wordt ververst, telt hier dus niet mee.

---

## Mediabestandcontrole

Waar de [bestandscontrole](#bestandscontrole) een vaste lijst configuratiebestanden bewaakt, is de mediabestandcontrole **database-gestuurd**: hij leest de playlist uit de Aeron-database en controleert of de bijbehorende audiobestanden daadwerkelijk op schijf staan. Zo signaleer je ontbrekende of verplaatste tracks vóórdat ze in de uitzending vallen.

De controle draait asynchroon: een `POST` start een run op de achtergrond en geeft direct een `run_id` terug; het resultaat lees je op met `GET .../status`.

### Hoe een databasereferentie wordt gematcht

In de Aeron-database staan audiopaden als **Windows-paden** (bijv. `O:\Audio\85\Artist - Title.wav`), terwijl de Toolbox doorgaans op Linux draait. De controle vertaalt een referentie in deze volgorde:

1. **Drive-mount (exact pad).** Met `drive_mounts` wordt een Windows-driveletter vertaald naar een hostmap (bijv. `O:` → `/mnt/aeron-o`). De volledige mapstructuur blijft behouden en het exacte pad wordt direct gecontroleerd. Dit is de snelste en meest eenduidige strategie.
2. **Bestandsnaam-index (fallback).** De mappen in `search_dirs` worden recursief geïndexeerd. Lukt het exacte pad niet, dan wordt gematcht op bestandsnaam — eerst inclusief extensie, daarna extensie-onafhankelijk (een `.wav` in de database matcht dan ook een `.flac` op schijf). Er wordt uitsluitend op de bestandsnaam (`audio.name`) gematcht, niet op losse `artist`/`title`-metadata.

> **Kort:** `drive_mounts` controleert of het bestand op de **exacte plek** uit de database staat (volledig pad, alleen de driveletter wordt vertaald); `search_dirs` controleert of een bestand met die **naam** überhaupt ergens onder de opgegeven mappen bestaat (de map uit de database wordt genegeerd).

Matchen gebeurt standaard hoofdletter-ongevoelig (`case_insensitive`), omdat de bron Windows is. Voicetracks worden standaard overgeslagen (`include_voicetracks`). Er wordt **geen** vaste `.wav`-aanname gedaan.

**Statussen per item:**
- `present` — er is precies één bestand gevonden.
- `missing` — geen bestand gevonden.
- `ambiguous` — meerdere bestanden matchen de referentie (bijv. dezelfde bestandsnaam in verschillende mappen).
- `no_reference` — het playlistitem heeft geen bruikbare referentie (bijv. de track bestaat niet meer in de database).
- `stat_error` — het bestand kon niet gecontroleerd worden (time-out, geen rechten, mount onbereikbaar).

### Mediabestandcontrole starten

Start een controle op de achtergrond voor de opgegeven scope.

**Endpoint:** `POST /api/media/files/check`
**Authenticatie:** Vereist

**Queryparameters (optioneel):**
- `date=YYYY-MM-DD` — controleer één dag. Zonder datumparameters wordt **vandaag** gecontroleerd (zoals bij `/api/playlist`).
- `from=YYYY-MM-DD&to=YYYY-MM-DD` — controleer een datumbereik. Het bereik is begrensd tot `media_file_check.max_range_days` (standaard 31) om enorme scans te voorkomen.
- `block_id={uuid}` — controleer één playlistblok (heeft voorrang op datumfilters).
- `limit={n}` — beperk het aantal te controleren items.
- `include_voicetracks=true` — neem voicetracks mee (standaard uitgesloten).

**Foutresponse indien uitgeschakeld:** `404 Not Found`
```json
{
  "error": "media file check is not enabled"
}
```

**Response:** `202 Accepted`
```json
{
  "message": "Media file check started",
  "run_id": 1,
  "check": "/api/media/files/check/status"
}
```

**Error response:** `409 Conflict`
```json
{
  "error": "media file check already running"
}
```

**Validatiefout (bijv. ongeldige datum of te groot bereik):** `400 Bad Request`
```json
{
  "error": "range: date range exceeds the maximum of 31 days"
}
```

De handmatige trigger en de geplande cronrun delen dezelfde single-flight gate; ze kunnen elkaar dus niet overlappen.

### Resultaat van de mediabestandcontrole

Toont de runstatus plus het resultaat van de meest recente run.

**Endpoint:** `GET /api/media/files/check/status`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "running": false,
  "run_id": 1,
  "completed_run_id": 1,
  "started_at": "2026-06-29T06:00:00Z",
  "result": {
    "checked_at": "2026-06-29T06:00:01Z",
    "scope": {
      "date": "2026-06-29",
      "exclude_voicetracks": true
    },
    "summary": {
      "total": 713,
      "present": 709,
      "missing": 2,
      "ambiguous": 1,
      "no_reference": 0,
      "errors": 1
    },
    "items": [
      {
        "trackid": "af042d85-55b2-4d26-9093-d1bb7278c607",
        "artist": "Robin S",
        "tracktitle": "Luv 4 Luv",
        "start_time": "2026-06-29T00:09:05",
        "block_id": "c087211a-b4ac-47c8-af26-3aa6ef40b870",
        "block": "",
        "status": "missing",
        "db_reference": "O:\\Audio\\93\\Robin S - 1993 Luv 4 Luv.wav",
        "checked_paths": [
          "/mnt/aeron-o/Audio/93/Robin S - 1993 Luv 4 Luv.wav",
          "search_dirs (by name): Robin S - 1993 Luv 4 Luv.wav"
        ],
        "matches": []
      }
    ]
  }
}
```

**Velden op topniveau:**
- `running`: Of er op dit moment een run bezig is.
- `run_id`: Monotone identifier van de huidige of meest recent gestarte run (`0` als de service nog nooit heeft gedraaid).
- `completed_run_id`: Identifier van de run waarvan het resultaat zichtbaar is in `result`.
- `started_at`: Starttijd van de huidige of meest recente run.
- `result`: Het resultaat van de meest recente voltooide run (`null` tot de eerste run klaar is).

**Velden in `result`:**
- `checked_at`: Tijdstip waarop de run de items verwerkte.
- `scope`: De toegepaste scope (`date`, `from`, `to`, `block_id`, `lookahead_days`, `limit`, `exclude_voicetracks`).
- `summary`: Tellingen per status (`total`, `present`, `missing`, `ambiguous`, `no_reference`, `errors`).
- `items`: Resultaat per playlistitem.
- `error`: Aanwezig wanneer de run zelf mislukte (bijvoorbeeld een databasefout).

**Velden per item:**
- `trackid`, `artist`, `tracktitle`, `start_time`, `block_id`, `block`: Playlist- en trackgegevens.
- `status`: Een van `present`, `missing`, `ambiguous`, `no_reference`, `stat_error`.
- `db_reference`: De referentie uit de database die gebruikt is.
- `checked_paths`: De concrete paden en zoekacties die zijn geprobeerd.
- `matches`: De gevonden bestanden op schijf (één bij `present`, meerdere bij `ambiguous`).
- `match_type`: Hoe gematcht is: `exact_path`, `filename` of `filename_noext` (ontbreekt bij geen match).
- `error`: Foutmelding bij `stat_error` (ontbreekt verder).

**Pollingrecept:** lees `run_id` uit de `POST`-response (`myRunID`), poll daarna `GET .../status` tot `completed_run_id >= myRunID && running == false`.

### Geplande controle en e-mailnotificaties

Met `media_file_check.scheduler` draait de controle automatisch op een cron-schema. De geplande run controleert standaard **vandaag**; met `media_file_check.lookahead_days` kun je vooruitkijken — bij `lookahead_days: 2` checkt de run vandaag t/m overmorgen (inclusief), zodat ontbrekende bestanden opvallen vóórdat ze worden uitgezonden. `0` (standaard) is alleen vandaag. Handmatige API-runs gebruiken hun eigen `from`/`to`-bereik en negeren deze instelling. Als e-mailnotificaties zijn geconfigureerd, stuurt een geplande run een alert wanneer er problemen (`missing`, `ambiguous` of `stat_error`) worden gevonden, en een herstelmelding zodra een volgende run weer schoon is. Handmatige API-runs versturen geen e-mail, zodat ad-hoc scopes de alert-status niet verstoren.

### Integratie met de health-endpoint (mediabestandcontrole)

Als de mediabestandcontrole is ingeschakeld, geeft `GET /api/health` een extra `media_file_check`-blok terug:

```json
{
  "status": "degraded",
  "version": "1.0.0",
  "database": "aeron",
  "database_status": "connected",
  "media_file_check": {
    "enabled": true,
    "problems": 3
  }
}
```

- `problems`: aantal `missing`-, `ambiguous`- en `stat_error`-items in de meest recente **geplande** run. Dit telt alleen mee voor de algemene status `"degraded"` wanneer de geplande controle (`scheduler.enabled`) aanstaat, zodat ad-hoc API-runs met een afwijkende scope de health niet beïnvloeden.

---

## Notificaties

### Test e-mail versturen

Verstuurt een test e-mail om de notificatieconfiguratie te valideren. Controleert achtereenvolgens of de configuratie geldig is, of authenticatie bij Microsoft Graph slaagt, en verstuurt vervolgens een testbericht.

**Endpoint:** `POST /api/notifications/test-email`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "message": "Test email sent successfully"
}
```

**Foutresponses:**

`400 Bad Request` - Configuratie ongeldig:
```json
{
  "error": "Notification configuration invalid: ..."
}
```

`502 Bad Gateway` - Authenticatie mislukt:
```json
{
  "error": "Authentication failed: ..."
}
```

`502 Bad Gateway` - Verzenden mislukt:
```json
{
  "error": "Failed to send test email: ..."
}
```

---

## Afbeeldingsverwerking

### Afbeeldingsoptimalisatie

Alle geüploade afbeeldingen worden automatisch:
1. Gevalideerd op formaat (JPEG, PNG)
2. Gecontroleerd op minimumafmetingen (optioneel, configureerbaar)
3. Geschaald naar maximumafmetingen (configureerbaar, standaard: 640×640)
4. Geconverteerd naar geoptimaliseerde JPEG
5. Alleen opgeslagen als de geoptimaliseerde versie kleiner is dan het origineel

### Ondersteunde afbeeldingsbronnen

1. **URL-download**: Geef een URL op om de afbeelding te downloaden
   - Ondersteunt HTTPS-URL's
   - Valideert URL-veiligheid
   - Download met time-out van 30 seconden

2. **Base64-upload**: Verstuur base64-gecodeerde afbeeldingsdata
   - Ondersteunt standaard base64-codering
   - Maximumgrootte beperkt door verzoeklimieten

### Afbeeldingsvalidatieregels

- **Minimumafmetingen**: Optioneel configureerbaar via `reject_smaller`
- **Maximumafmetingen**: Configureerbaar (standaard: 640×640)
- **Toegestane formaten**: JPEG, PNG
- **Beeldverhouding**: Wordt behouden tijdens schalen
- **Kwaliteit**: Configureerbare JPEG-kwaliteit (standaard: 85)

---

## Bedrijfsregels

### Afbeeldingsverwerking
- Afbeeldingen worden automatisch geoptimaliseerd voor gebruik in Aeron
- PNG-afbeeldingen worden geconverteerd naar JPEG
- Alleen de geoptimaliseerde versie wordt opgeslagen als deze kleiner is dan het origineel

### UUID-validatie
- Alle artiest- en track-ID's moeten geldige UUID's zijn (versie 4-formaat)
- Ongeldige UUID's resulteren in 400 Bad Request met Engelse foutmelding
- Voorbeeld geldig UUID: `123e4567-e89b-12d3-a456-426614174000`

### Afbeeldingsopslag
- Afbeeldingen worden opgeslagen als BYTEA in PostgreSQL
- Originele afbeeldingen worden niet bewaard
- Uitsluitend geoptimaliseerde versies worden opgeslagen

---

## Frequentiebeperking

Geen ingebouwde frequentiebeperking. Implementeer deze indien nodig op proxy- of load-balancerniveau.

---

## Gebruiksvoorbeelden

### cURL-voorbeelden

**Artiestafbeelding uploaden via URL:**
```bash
curl -X POST "http://localhost:8080/api/artists/123e4567-e89b-12d3-a456-426614174000/image" \
  -H "X-API-Key: jouw-api-sleutel" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://voorbeeld.nl/artiest.jpg"}'
```

**Trackafbeelding ophalen:**
```bash
curl -X GET "http://localhost:8080/api/tracks/456e7890-e89b-12d3-a456-426614174000/image" \
  -H "X-API-Key: jouw-api-sleutel" \
  --output track-afbeelding.jpg
```

**Alle artiestafbeeldingen verwijderen (let op: onomkeerbaar!):**
```bash
curl -X DELETE "http://localhost:8080/api/artists/bulk-delete" \
  -H "X-API-Key: jouw-api-sleutel" \
  -H "X-Confirm-Bulk-Delete: DELETE ALL"
```

**Playlist voor vandaag ophalen:**
```bash
curl -X GET "http://localhost:8080/api/playlist" \
  -H "X-API-Key: jouw-api-sleutel"
```

### Python-voorbeeld

```python
import requests
import base64

API_KEY = "jouw-api-sleutel"
BASE_URL = "http://localhost:8080/api"

headers = {"X-API-Key": API_KEY}

# Afbeelding uploaden vanuit bestand
with open("albumhoes.jpg", "rb") as f:
    image_data = base64.b64encode(f.read()).decode()

response = requests.post(
    f"{BASE_URL}/tracks/456e7890-e89b-12d3-a456-426614174000/image",
    headers=headers,
    json={"image": image_data}
)

if response.status_code == 200:
    result = response.json()
    print(f"Afbeelding geoptimaliseerd: {result['savings_percent']}% ruimtebesparing")
```

### JavaScript/Node.js-voorbeeld

```javascript
const axios = require('axios');
const fs = require('fs');

const API_KEY = 'jouw-api-sleutel';
const BASE_URL = 'http://localhost:8080/api';

// Artiestafbeelding uploaden via URL
async function uploadArtiestAfbeelding(artistId, imageUrl) {
    try {
        const response = await axios.post(
            `${BASE_URL}/artists/${artistId}/image`,
            { url: imageUrl },
            { headers: { 'X-API-Key': API_KEY } }
        );
        console.log('Upload succesvol:', response.data);
    } catch (error) {
        console.error('Upload mislukt:', error.response.data);
    }
}

// Playlisttracks ophalen met filters
async function haalPlaylistTracksOp(blockId) {
    try {
        const response = await axios.get(
            `${BASE_URL}/playlist`,
            {
                params: {
                    block_id: blockId,
                    track_image: 'false',
                    limit: 50
                },
                headers: { 'X-API-Key': API_KEY }
            }
        );
        console.log(`${response.data.length} tracks zonder afbeeldingen gevonden`);
    } catch (error) {
        console.error('Verzoek mislukt:', error.response.data);
    }
}
```

---

## Configuratie

Het gedrag van de API kan worden geconfigureerd via `config.json`:

```json
{
  "database": {
    "host": "localhost",
    "port": "5432",
    "user": "aeron",
    "password": "",
    "name": "aeron",
    "schema": "aeron",
    "sslmode": "disable",
    "max_open_conns": 25,
    "max_idle_conns": 5,
    "conn_max_lifetime_minutes": 5
  },
  "image": {
    "target_width": 640,
    "target_height": 640,
    "quality": 85,
    "reject_smaller": false,
    "max_image_download_size_bytes": 52428800
  },
  "api": {
    "enabled": true,
    "keys": ["jouw-veilige-api-sleutel-hier"],
    "request_timeout_seconds": 30
  },
  "maintenance": {
    "bloat_threshold": 10.0,
    "dead_tuple_threshold": 10000,
    "scheduler": {
      "enabled": false,
      "schedule": "0 4 * * 0"
    }
  },
  "backup": {
    "enabled": false,
    "path": "./backups",
    "retention_days": 30,
    "max_backups": 10,
    "default_compression": 9,
    "timeout_minutes": 30,
    "pg_dump_path": "",
    "pg_restore_path": "",
    "scheduler": {
      "enabled": false,
      "schedule": "0 3 * * *"
    },
    "s3": {
      "enabled": false,
      "bucket": "mijn-backups",
      "region": "eu-west-1",
      "endpoint": "",
      "access_key_id": "",
      "secret_access_key": "",
      "path_prefix": "backups/",
      "force_path_style": false
    }
  },
  "file_monitor": {
    "enabled": false,
    "interval_seconds": 60,
    "checks": [
      {
        "name": "Nieuws bulletin",
        "path": "/data/news.mp3",
        "max_age_minutes": 30,
        "stat_timeout_seconds": 5,
        "active_window": "06:00-22:00"
      },
      {
        "name": "Weer",
        "path": "/data/weather.mp3",
        "max_age_minutes": 60,
        "stat_timeout_seconds": 5
      }
    ]
  },
  "notifications": {
    "email": {
      "tenant_id": "",
      "client_id": "",
      "client_secret": "",
      "from_address": "",
      "recipients": ""
    }
  },
  "log": {
    "level": "info",
    "format": "text"
  }
}
```

**Instellingen voor bestandscontrole:**
- `file_monitor.enabled`: Schakel de bestandscontrole in.
- `file_monitor.interval_seconds`: Pollinginterval in seconden (standaard 60; `0` of weglaten = standaard).
- `file_monitor.checks`: Array met te bewaken bestanden (minimaal 1 item vereist wanneer ingeschakeld).
  - `name`: Optionele weergavenaam voor notificaties.
  - `path`: Absoluut pad naar het bestand.
  - `max_age_minutes`: Maximaal toegestane leeftijd in minuten (minimaal 1).
  - `stat_timeout_seconds`: Maximale tijd in seconden voor `os.Stat` (standaard 5; `0` of weglaten = standaard). Beschermt tegen vastgelopen NFS- of SMB-mounts.
  - `active_window`: Optioneel tijdvenster `"HH:MM-HH:MM"` waarin alerts en `GET /api/health`-degradatie actief zijn. Buiten dit venster blijft `is_stale` zichtbaar in `GET /api/file-monitor/status`, maar wordt er geen alert- of herstelmail verstuurd en blijft `GET /api/health` gezond. Een eindtijd vóór de starttijd betekent dat het venster over middernacht heen loopt, bijvoorbeeld `"22:00-06:00"`. Gelijke start- en eindtijd zijn ongeldig; laat het veld weg voor altijd actief.

Het controle-interval staat los van `max_age_minutes` en wordt geconfigureerd via `interval_seconds` (standaard 60 s).

> [!IMPORTANT]
> `active_window` wordt geïnterpreteerd in de lokale tijdzone die wordt bepaald door de `TZ`-omgevingsvariabele. Stel `TZ` in productie consequent in, zodat het venster werkt zoals de operator verwacht.

Zie [config.example.json](config.example.json) voor alle beschikbare opties.

---

## Databaseschema

De API werkt met de volgende Aeron PostgreSQL-tabellen:

```sql
CREATE TABLE {schema}.artist (
    artistid UUID PRIMARY KEY,
    artist VARCHAR NOT NULL,
    picture BYTEA
);

CREATE TABLE {schema}.track (
    titleid UUID PRIMARY KEY,
    tracktitle VARCHAR NOT NULL,
    artist VARCHAR,
    artistid UUID,
    picture BYTEA,
    exporttype INTEGER
);

CREATE TABLE {schema}.playlistitem (
    id SERIAL PRIMARY KEY,
    titleid UUID,
    startdatetime TIMESTAMP,
    blockid UUID
);

CREATE TABLE {schema}.playlistblock (
    blockid UUID PRIMARY KEY,
    name VARCHAR,
    startdatetime TIMESTAMP,
    enddatetime TIMESTAMP
);
```

## Belangrijke opmerkingen

- UUID's zijn hoofdletterongevoelig
- Het contenttype van afbeeldingen wordt automatisch gedetecteerd
- De API maakt gebruik van connection pooling voor optimale databaseprestaties
- Runtime user-facing messages, including API responses and email notifications, are in English.
