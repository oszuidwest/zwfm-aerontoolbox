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
| `/api/db/maintenance/vacuum` | POST | VACUUM starten (async) | Ja |
| `/api/db/maintenance/analyze` | POST | ANALYZE starten (async) | Ja |
| `/api/db/maintenance/status` | GET | Onderhoud status opvragen | Ja |
| **Bestandscontrole** |
| `/api/file-monitor/status` | GET | Status bestandscontrole | Ja |
| **Backups** |
| `/api/db/backup` | POST | Nieuwe backup aanmaken | Ja |
| `/api/db/backup/status` | GET | Backup status opvragen | Ja |
| `/api/db/backups` | GET | Lijst van alle backups | Ja |
| `/api/db/backups/{filename}` | GET | Specifieke backup downloaden | Ja |
| `/api/db/backups/{filename}/validate` | GET | Backup integriteit valideren | Ja |
| `/api/db/backups/{filename}` | DELETE | Backup verwijderen | Ja |

## Authenticatie

Wanneer authenticatie is ingeschakeld in de configuratie, vereisen alle endpoints (behalve `/health`) een API-sleutel.

**Header:** `X-API-Key: jouw-api-sleutel`

**Response bij ontbrekende autorisatie:**
```json
{
  "success": false,
  "error": "unauthorized: invalid or missing API key"
}
```

## Algemene response-headers

Alle API-responses bevatten:
- `Content-Type: application/json; charset=utf-8` (uitgezonderd afbeeldingsendpoints)

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
> In de voorbeelden hieronder wordt voor de leesbaarheid alleen de inhoud van het `data`-veld getoond, maar in werkelijkheid wordt altijd de complete wrapper geretourneerd.

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
- `409` Conflict - Operatie al bezig (backup of onderhoud)
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
    "database_status": "connected"
  }
}
```

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
  "error": "artist not found"
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
  "error": "artist image not found"
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
- `400` Bad Request - Ongeldige invoer
- `404` Not Found - Artiest niet gevonden
- `422` Unprocessable Entity - Afbeeldingsvalidatie mislukt

### Artiestafbeelding verwijderen

Het verwijderen van een artiestafbeelding.

**Endpoint:** `DELETE /api/artists/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Artiest-UUID

**Response:** `200 OK`
```json
{
  "message": "image deleted",
  "artist_id": "123e4567-e89b-12d3-a456-426614174000"
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "artist image not found"
}
```

### Bulkverwijdering artiestafbeeldingen

Het verwijderen van alle artiestafbeeldingen uit de database.

**Endpoint:** `DELETE /api/artists/bulk-delete`
**Authenticatie:** Vereist

**Vereiste header:**
- `X-Confirm-Bulk-Delete: VERWIJDER ALLES`

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
  "error": "missing confirmation header: X-Confirm-Bulk-Delete"
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
  "error": "track not found"
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
  "error": "track image not found"
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
- `400` Bad Request - Ongeldige invoer
- `404` Not Found - Track niet gevonden
- `422` Unprocessable Entity - Afbeeldingsvalidatie mislukt

### Trackafbeelding verwijderen

Het verwijderen van de albumhoes van een track.

**Endpoint:** `DELETE /api/tracks/{id}/image`
**Authenticatie:** Vereist

**Parameters:**
- `id` (padparameter, vereist): Track-UUID

**Response:** `200 OK`
```json
{
  "message": "image deleted",
  "track_id": "456e7890-e89b-12d3-a456-426614174000"
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "track image not found"
}
```

### Bulkverwijdering trackafbeeldingen

Het verwijderen van alle trackafbeeldingen uit de database.

**Endpoint:** `DELETE /api/tracks/bulk-delete`
**Authenticatie:** Vereist

**Vereiste header:**
- `X-Confirm-Bulk-Delete: VERWIJDER ALLES`

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
  "error": "missing confirmation header: X-Confirm-Bulk-Delete"
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

Bekijk gedetailleerde databasestatistieken inclusief tabelgroottes, bloat-percentages en onderhoudsaanbevelingen.

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
  "tables": [
    {
      "name": "track",
      "row_count": 125000,
      "dead_tuples": 4500,
      "dead_tuple_ratio": 3.6,
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
  "needs_maintenance": true,
  "recommendations": [
    "Table 'playlistitem' has high dead tuple ratio (15.2%) - VACUUM recommended",
    "Table 'artist' has 12500 dead tuples - VACUUM recommended"
  ],
  "checked_at": "2025-12-22T14:30:00Z"
}
```

### VACUUM starten

VACUUM starten op tabellen om opslagruimte vrij te maken en prestaties te verbeteren. De operatie draait asynchroon op de achtergrond.

**Endpoint:** `POST /api/db/maintenance/vacuum`
**Authenticatie:** Vereist

**Request Body:**
```json
{
  "tables": ["track", "artist"],
  "analyze": true
}
```

**Parameters:**
- `tables` (optioneel): Specifieke tabellen om te vacuumen. Indien leeg, worden tabellen die onderhoud nodig hebben automatisch geselecteerd.
- `analyze` (optioneel): Indien `true`, wordt ANALYZE na VACUUM uitgevoerd.

**Response:** `202 Accepted`
```json
{
  "message": "Vacuum with analyze started",
  "check": "/api/db/maintenance/status"
}
```

**Foutresponse:** `409 Conflict`
```json
{
  "error": "maintenance operation already in progress"
}
```

### ANALYZE starten

ANALYZE starten om tabelstatistieken bij te werken voor de PostgreSQL-queryoptimizer.

**Endpoint:** `POST /api/db/maintenance/analyze`
**Authenticatie:** Vereist

**Request Body:**
```json
{
  "tables": ["track"]
}
```

**Parameters:**
- `tables` (optioneel): Specifieke tabellen om te analyzeren. Indien leeg, worden tabellen die het nodig hebben automatisch geselecteerd.

**Response:** `202 Accepted`
```json
{
  "message": "Analyze started",
  "check": "/api/db/maintenance/status"
}
```

### Onderhoud status opvragen

Controleer de voortgang en resultaten van de laatste onderhoudsoperatie.

**Endpoint:** `GET /api/db/maintenance/status`
**Authenticatie:** Vereist

**Response tijdens onderhoud:** `200 OK`
```json
{
  "running": true,
  "operation": "vacuum",
  "started_at": "2025-12-22T14:30:00Z",
  "tables_total": 5,
  "tables_done": 2,
  "current_table": "track"
}
```

**Response na voltooiing:** `200 OK`
```json
{
  "running": false,
  "operation": "vacuum",
  "started_at": "2025-12-22T14:30:00Z",
  "ended_at": "2025-12-22T14:30:45Z",
  "success": true,
  "tables_total": 5,
  "tables_done": 5,
  "last_result": {
    "tables_total": 5,
    "tables_success": 5,
    "tables_failed": 0,
    "tables_skipped": 0,
    "results": [
      {
        "table": "track",
        "success": true,
        "message": "VACUUM completed",
        "dead_tuples_before": 4500,
        "dead_tuple_ratio_before": 3.6,
        "duration": "1.25s",
        "analyzed": true
      }
    ],
    "executed_at": "2025-12-22T14:30:45Z"
  }
}
```

**Response na fout:** `200 OK`
```json
{
  "running": false,
  "operation": "vacuum",
  "started_at": "2025-12-22T14:30:00Z",
  "ended_at": "2025-12-22T14:30:05Z",
  "success": false,
  "error": "maintenance timeout after 30m0s"
}
```

### Automatisch onderhoud

Database-onderhoud kan automatisch worden uitgevoerd via de ingebouwde scheduler. Configureer dit in `config.json`:

```json
"maintenance": {
  "bloat_threshold": 10.0,
  "dead_tuple_threshold": 10000,
  "timeout_minutes": 30,
  "scheduler": {
    "enabled": true,
    "schedule": "0 4 * * 0"
  }
}
```

**Parameters:**
- `bloat_threshold`: Percentage dead tuples waarboven VACUUM wordt aanbevolen
- `dead_tuple_threshold`: Absoluut aantal dead tuples waarboven VACUUM wordt aanbevolen
- `timeout_minutes`: Maximale tijd voor onderhoudsoperaties (standaard: 30)
- `scheduler.enabled`: Schakel automatisch onderhoud in/uit
- `scheduler.schedule`: Cron-expressie (zie backup-sectie voor voorbeelden)

De scheduler draait VACUUM ANALYZE op tabellen die aan de threshold-criteria voldoen. De tijdzone wordt bepaald door de systeemtijdzone (instelbaar via `TZ` environment variable).

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
- `compression` (optioneel): Compressieniveau 0-9 (standaard: 9)

**Response:** `202 Accepted`
```json
{
  "message": "backup started",
  "check": "/api/db/backup/status"
}
```

De backup wordt asynchroon uitgevoerd. Controleer `GET /api/db/backup/status` voor de voortgang.

> [!WARNING]
> Er kan slechts één backup tegelijk draaien. Een tweede aanvraag tijdens een lopende backup retourneert een fout.

**Foutresponses:**

`400 Bad Request` - Backup niet ingeschakeld:
```json
{
  "error": "backup functionaliteit is niet ingeschakeld"
}
```

`500 Internal Server Error` - Backup al bezig:
```json
{
  "error": "backup already in progress"
}
```

### Backup status

Toont de status van de laatste backupbewerking.

**Endpoint:** `GET /api/db/backup/status`
**Authenticatie:** Vereist

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
  "error": "backup timeout na 30m0s (configureer backup.timeout_minutes)",
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

**Parameters:**
- `filename` (padparameter, vereist): Naam van het backupbestand

**Response:** `200 OK`
- Content-Type: `application/octet-stream`
- Content-Disposition: `attachment; filename=...`
- Binaire backup data

**Foutresponse:** `404 Not Found`
```json
{
  "error": "backupbestand niet gevonden"
}
```

### Backup verwijderen

Een specifiek backupbestand verwijderen.

**Endpoint:** `DELETE /api/db/backups/{filename}`
**Authenticatie:** Vereist

**Parameters:**
- `filename` (padparameter, vereist): Naam van het backupbestand

**Vereiste header:**
- `X-Confirm-Delete: {filename}` (bestandsnaam moet overeenkomen)

**Response:** `200 OK`
```json
{
  "message": "backup deleted",
  "filename": "aeron-backup-2025-12-21T14-30-00.dump"
}
```

**Foutresponse:** `400 Bad Request`
```json
{
  "error": "confirmation header missing: X-Confirm-Delete must contain the filename"
}
```

### Backup valideren

De integriteit van een bestaand backupbestand valideren. Handig voor het controleren van backups na download of herstel van S3.

**Endpoint:** `GET /api/db/backups/{filename}/validate`
**Authenticatie:** Vereist

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
  "error": "backup validatie: bestand is corrupt of onleesbaar: pg_restore: error: ..."
}
```

**Foutresponse:** `404 Not Found`
```json
{
  "error": "backupbestand niet gevonden"
}
```

Validatie gebeurt via `pg_restore --list` die de TOC en interne checksums controleert.

---

## Bestandscontrole

De bestandscontrole (file monitor) bewaakt bestanden op schijf en signaleert wanneer ze ouder zijn dan een geconfigureerde maximumleeftijd. Dit is nuttig voor het detecteren van falende downloads of updates van externe processen (bijv. nieuwsbulletins, weerbericht-MP3's).

De controle draait automatisch op een interval dat is afgeleid van de kleinste `max_age_minutes` uit alle geconfigureerde checks. Na een herstart wordt de eerste controle als "grace run" beschouwd: resultaten worden geobserveerd maar er worden geen meldingen verstuurd, om valse alarmen te voorkomen.

### Status bestandscontrole

Toont de resultaten van de meest recente bestandscontrole.

**Endpoint:** `GET /api/file-monitor/status`
**Authenticatie:** Vereist

**Response:** `200 OK`
```json
{
  "last_check_at": "2024-01-15T10:30:00Z",
  "interval_minutes": 10,
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

De volgende voorbeelden tonen individuele items uit de `checks`-array voor specifieke situaties:

**Check-item bij ontbrekend bestand:**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "file_exists": false,
  "is_stale": true,
  "in_alert": true
}
```

**Check-item bij fout (bijv. geen toegang):**
```json
{
  "name": "Nieuws bulletin",
  "path": "/data/news.mp3",
  "max_age_minutes": 10,
  "file_exists": null,
  "is_stale": true,
  "in_alert": true,
  "error": "stat /data/news.mp3: permission denied"
}
```

**Velden:**
- `last_check_at`: Tijdstip van de laatste controle
- `interval_minutes`: Automatisch berekend controle-interval (kleinste `max_age_minutes`)
- `checks`: Array met resultaten per bestand
  - `name`: Optionele weergavenaam (uit configuratie)
  - `path`: Bestandspad op schijf
  - `max_age_minutes`: Maximaal toegestane leeftijd
  - `file_exists`: Of het bestand bestaat (`true`, `false`, of `null` bij fouten)
  - `file_age_minutes`: Leeftijd in minuten (afwezig als bestand niet bestaat)
  - `last_modified`: Laatste wijzigingstijd (afwezig als bestand niet bestaat)
  - `is_stale`: Of het bestand te oud of onbereikbaar is
  - `in_alert`: Of er momenteel een alert actief is voor dit bestand
  - `error`: Foutmelding bij toegangsproblemen (afwezig bij normaal gebruik)

> [!NOTE]
> Het veld `file_exists` is nullable: `true` = bestand bestaat, `false` = bestand niet gevonden, `null` = onbekend (bijv. bij een permissiefout). Controleer het `error`-veld voor details wanneer `file_exists` `null` is.

### Integratie met health-endpoint

Wanneer de bestandscontrole is ingeschakeld, toont het health-endpoint (`GET /api/health`) een extra `file_monitor`-veld:

```json
{
  "status": "degraded",
  "version": "1.0.0",
  "database": "aeron",
  "database_status": "connected",
  "file_monitor": {
    "enabled": true,
    "checks_total": 2,
    "checks_stale": 1
  }
}
```

De overall status wordt `"degraded"` wanneer een of meer bestanden verouderd zijn.

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
- Ongeldige UUID's resulteren in 400 Bad Request met Nederlandse foutmelding
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
  -H "X-Confirm-Bulk-Delete: VERWIJDER ALLES"
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
    "timeout_minutes": 30,
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
    "checks": [
      {
        "name": "Nieuws bulletin",
        "path": "/data/news.mp3",
        "max_age_minutes": 30
      },
      {
        "name": "Weer",
        "path": "/data/weather.mp3",
        "max_age_minutes": 60
      }
    ]
  },
  "log": {
    "level": "info",
    "format": "text"
  }
}
```

**Bestandscontrole-instellingen:**
- `file_monitor.enabled`: Schakel de bestandscontrole in
- `file_monitor.checks`: Array van te bewaken bestanden (minstens 1 vereist wanneer ingeschakeld)
  - `name`: Optionele weergavenaam voor meldingen
  - `path`: Absoluut pad naar het bestand
  - `max_age_minutes`: Maximale leeftijd in minuten (minimaal 1)

Het controle-interval wordt automatisch afgeleid van de kleinste `max_age_minutes` waarde. Er is geen apart schedule nodig.

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
- Foutmeldingen worden in het Engels geretourneerd