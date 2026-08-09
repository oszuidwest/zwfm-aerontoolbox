# Aeron Toolbox API

HTTP-API voor beheer en onderhoud van Aeron. Alle paden hieronder zijn volledig; de standaardserver draait op `http://localhost:8080`.

## Conventies

### Authenticatie

Als `api.enabled` aanstaat, vereisen alle `/api`-endpoints deze header:

```http
X-API-Key: jouw-api-sleutel
```

`GET /health` blijft publiek. Gebruik per omgeving een unieke sleutel met minimaal 32 bytes entropie, bijvoorbeeld gegenereerd met `openssl rand -base64 32`.

### Rate limiting

`api.rate_limit_enabled` schakelt een fixed-window-limiet in voor `/api`. Geldige API-sleutels krijgen elk een eigen budget; overige verzoeken worden gegroepeerd op het directe peer-adres (`RemoteAddr`). Adressen uit proxyheaders worden niet gebruikt.

Een overschrijding retourneert `429 Too Many Requests`, een `Retry-After`-header en de normale JSON-foutresponse. `GET /health` valt buiten de limiter.

### JSON-responses

Succesvolle JSON-response:

```json
{
  "success": true,
  "data": {}
}
```

Foutresponse:

```json
{
  "success": false,
  "error": "error message"
}
```

De voorbeelden in dit document tonen de volledige response. Afbeeldingen en back-updownloads zijn binair en gebruiken geen JSON-wrapper. Een publieke readinessfout is de enige foutresponse die naast `error` ook `data` bevat.

Onveilige cross-originverzoeken vanuit browsers worden geblokkeerd. Zo'n blokkade kan vóór de API-handler een `403 Forbidden` in platte tekst retourneren.

### Statuscodes

| Code | Betekenis |
|---|---|
| `200` | Verzoek geslaagd |
| `202` | Achtergrondtaak gestart |
| `400` | Ongeldige invoer of ontbrekende bevestigingsheader |
| `401` | Ontbrekende of ongeldige API-sleutel |
| `403` | Geblokkeerd cross-origin verzoek |
| `404` | Endpoint, record of bestand niet gevonden, of functie uitgeschakeld |
| `408` | Uploadbody niet op tijd gelezen |
| `409` | Dezelfde achtergrondtaak draait al |
| `413` | Requestbody te groot |
| `429` | Rate limit overschreden |
| `500` | Interne bewerking mislukt |
| `502` | Authenticatie bij of verzending via de mailprovider mislukt |
| `503` | Publieke readinesscontrole kan de database niet bereiken |

Bij synchrone `5xx`-fouten worden interne details gelogd en alleen veilige fouttekst teruggestuurd. Runtimeberichten en veldwaarden zijn Engelstalig.

### Achtergrondtaken pollen

Bestandscontroles retourneren een `run_id`. Poll het opgegeven statusendpoint totdat:

```text
completed_run_id >= run_id && running == false
```

Bij `completed_run_id == run_id` hoort het zichtbare resultaat exact bij de gestarte run. Een hogere waarde betekent dat een latere geplande run het resultaat inmiddels heeft vervangen.

Een back-up retourneert geen `run_id`; poll daar `running` tot de waarde `false` is.

## Endpointoverzicht

| Methode | Pad | Doel |
|---|---|---|
| `GET` | `/health` | Publieke readinesscontrole |
| `GET` | `/api/health` | Gedetailleerde operationele status |
| `GET` | `/api/artists` | Afbeeldingsstatistieken van artiesten |
| `GET` | `/api/artists/{id}` | Artiest ophalen |
| `GET` | `/api/artists/{id}/image` | Artiestafbeelding downloaden |
| `POST` | `/api/artists/{id}/image` | Artiestafbeelding opslaan |
| `DELETE` | `/api/artists/{id}/image` | Artiestafbeelding verwijderen |
| `DELETE` | `/api/artists/bulk-delete` | Alle artiestafbeeldingen verwijderen |
| `GET` | `/api/tracks` | Afbeeldingsstatistieken van tracks |
| `GET` | `/api/tracks/{id}` | Track ophalen |
| `GET` | `/api/tracks/{id}/image` | Trackafbeelding downloaden |
| `POST` | `/api/tracks/{id}/image` | Trackafbeelding opslaan |
| `DELETE` | `/api/tracks/{id}/image` | Trackafbeelding verwijderen |
| `DELETE` | `/api/tracks/bulk-delete` | Alle trackafbeeldingen verwijderen |
| `GET` | `/api/playlist` | Playlistblokken of tracks ophalen |
| `GET` | `/api/db/maintenance/health` | Databasestatus ophalen |
| `POST` | `/api/db/backup` | Back-up starten |
| `GET` | `/api/db/backup/status` | Back-upstatus ophalen |
| `GET` | `/api/db/backups` | Back-ups tonen |
| `GET` | `/api/db/backups/{filename}` | Back-up downloaden |
| `GET` | `/api/db/backups/{filename}/validate` | Back-up valideren |
| `DELETE` | `/api/db/backups/{filename}` | Back-up verwijderen |
| `GET` | `/api/file-monitor/status` | Status van vaste bestanden ophalen |
| `POST` | `/api/file-monitor/check` | Controle van vaste bestanden starten |
| `POST` | `/api/media/files/check` | Controle van playlist-audio starten |
| `GET` | `/api/media/files/check/status` | Resultaat van playlist-audiocontrole ophalen |
| `POST` | `/api/notifications/test-email` | Testmail versturen |

`HEAD` wordt voor alle `GET`-routes als een `GET` zonder responsebody afgehandeld.

## Status

### `GET /health`

Publieke readinesscontrole voor containers en load balancers. Gebruik dit endpoint niet als liveness-restarttrigger: een database-uitval levert `503` op, maar een procesrestart herstelt de database niet.

Gezond, `200 OK`:

```json
{
  "success": true,
  "data": {
    "status": "healthy"
  }
}
```

Database onbereikbaar, `503 Service Unavailable`:

```json
{
  "success": false,
  "data": {
    "status": "unhealthy"
  },
  "error": "Service unavailable"
}
```

### `GET /api/health`

Retourneert altijd `200 OK` wanneer de statusresponse kan worden opgebouwd. Lees `status` en `database_status` voor het operationele oordeel.

```json
{
  "success": true,
  "data": {
    "status": "degraded",
    "version": "1.0.0",
    "database": "aeron",
    "database_status": "connected",
    "notifications": {
      "configured": true,
      "secret_expiry": {
        "expires_at": "2026-12-01T00:00:00Z",
        "expires_soon": false,
        "days_left": 113
      }
    },
    "file_monitor": {
      "enabled": true,
      "checks_total": 2,
      "checks_stale": 1,
      "checks_alerting": 1
    },
    "media_file_check": {
      "enabled": true,
      "problems": 0
    }
  }
}
```

| Veld | Betekenis |
|---|---|
| `status` | `healthy`, `degraded` of `unhealthy` |
| `version` | Buildversie |
| `database` | Geconfigureerde databasenaam |
| `database_status` | `connected` of `disconnected` |
| `notifications` | Altijd aanwezig; configuratie, laatste verzendfout en eventuele vervaldatum van het clientsecret |
| `file_monitor` | Alleen aanwezig als `file_monitor.enabled` aanstaat |
| `media_file_check` | Alleen aanwezig als `media_file_check.enabled` aanstaat |

Statusprioriteit: een verbroken databaseverbinding is `unhealthy`. Een bijna verlopen mailsecret, `checks_alerting > 0` of problemen in de laatste geplande mediafilecontrole maken een verbonden systeem `degraded`. Dat laatste signaal telt alleen mee als de scheduler voor die controle aanstaat.

`checks_stale` telt alle verouderde of onbereikbare bestanden. `checks_alerting` houdt rekening met `active_window`. `media_file_check.problems` telt fouten uit de laatste geplande run; handmatige runs beïnvloeden deze healthstatus niet.

## Artiesten en tracks

In deze sectie staat `{type}` voor `artists` of `tracks`. `{id}` moet een hoofdletterongevoelige UUID v4 zijn.

### Statistieken

```http
GET /api/{type}
```

```json
{
  "success": true,
  "data": {
    "total": 1250,
    "with_images": 450,
    "without_images": 800
  }
}
```

### Details

```http
GET /api/{type}/{id}
```

Artiestvelden:

| Veld | Betekenis |
|---|---|
| `artistid` | Artiest-UUID |
| `artist` | Naam |
| `info` | Vrije beschrijving |
| `website`, `twitter`, `instagram` | Online verwijzingen |
| `has_image` | Of een afbeelding is opgeslagen |
| `repeat_value` | Herhalingswaarde voor planning |

Trackvelden:

| Veld | Betekenis |
|---|---|
| `titleid` | Track-UUID |
| `tracktitle`, `artist`, `artistid` | Titel- en artiestgegevens |
| `year` | Jaar uit Aeron |
| `knownlength`, `introtime`, `outrotime` | Tijden in milliseconden |
| `tempo`, `bpm` | Tempowaarden |
| `gender`, `language`, `mood` | Numerieke Aeron-classificaties |
| `exporttype`, `repeat_value`, `rating` | Planning- en exportwaarden |
| `has_image` | Of een afbeelding is opgeslagen |
| `website`, `conductor`, `orchestra` | Aanvullende metadata |

Een onbekend ID retourneert `404`. Een ID dat geen UUID v4 is retourneert `400`.

### Afbeelding downloaden

```http
GET /api/{type}/{id}/image
```

De response is binair met een gedetecteerd `Content-Type`, bijvoorbeeld `image/jpeg`, `image/png` of `image/webp`, en een `Content-Length`. Een ontbrekende afbeelding retourneert een JSON-fout met `404`.

### Afbeelding uploaden

```http
POST /api/{type}/{id}/image
Content-Type: application/json
```

Geef exact één bron op:

```json
{"url":"https://voorbeeld.nl/afbeelding.jpg"}
```

of:

```json
{"image":"<base64-gecodeerde JPEG- of PNG-data>"}
```

Trackresponse:

```json
{
  "success": true,
  "data": {
    "artist": "The Beatles",
    "track": "Hey Jude",
    "original_size": 345678,
    "optimized_size": 65678,
    "savings_percent": 81.0
  }
}
```

Bij een artiest ontbreekt het veld `track`. Uploadfouten gebruiken onder meer `400` voor ongeldige invoer, `404` voor een onbekend ID, `408` voor een leestime-out en `413` voor een te grote JSON-body.

Ondersteund zijn JPEG en PNG; een URL-bron moet HTTP of HTTPS gebruiken. Een afbeelding boven de doelafmetingen wordt met behoud van verhouding verkleind. De JPEG-gecodeerde versie wordt alleen opgeslagen als die kleiner is; anders blijven de oorspronkelijke bytes behouden. `reject_smaller` kan afbeeldingen onder de doelafmetingen weigeren.

### Afbeelding verwijderen

```http
DELETE /api/{type}/{id}/image
```

De response bevat `message` en `artist_id` of `track_id`. Een onbekend ID retourneert `404`; een bestaand record zonder afbeelding blijft een geslaagde verwijdering.

### Alle afbeeldingen verwijderen

```http
DELETE /api/artists/bulk-delete
DELETE /api/tracks/bulk-delete
X-Confirm-Bulk-Delete: DELETE ALL
```

De bevestigingsheader moet exact overeenkomen. De response bevat `deleted` en `message`; zonder bevestiging volgt `400`.

## Playlist

### `GET /api/playlist`

Zonder `block_id` retourneert dit endpoint de playlistblokken voor één datum, inclusief hun tracks.

| Parameter | Gedrag |
|---|---|
| `date` | `YYYY-MM-DD`; standaard de huidige datum van PostgreSQL |

Een blok bevat `blockid`, `name`, `date`, `start_time`, `end_time` en `tracks`.

### `GET /api/playlist?block_id={uuid}`

Met `block_id` retourneert hetzelfde endpoint alleen de tracks van dat blok.

| Parameter | Gedrag |
|---|---|
| `block_id` | Playlistblok-UUID; schakelt de blokmodus in |
| `limit` | Positief maximum; zonder waarde geldt geen limiet |
| `offset` | Niet-negatieve offset; wordt alleen gebruikt samen met `limit` |
| `track_image` | `true`, `false`, `yes`, `no`, `1` of `0` |
| `artist_image` | `true`, `false`, `yes`, `no`, `1` of `0` |
| `sort` | `start_time` (standaard), `track`, `artist` of `duration` |
| `desc` | Alleen de waarde `true` sorteert aflopend |

Een playlistitem bevat:

| Veld | Betekenis |
|---|---|
| `trackid`, `tracktitle` | Trackgegevens |
| `artistid`, `artistname` | Artiestgegevens |
| `start_time`, `end_time` | Tijd als `HH:MM:SS` |
| `duration` | Duur in milliseconden |
| `has_track_image`, `has_artist_image` | Afbeeldingsstatus |
| `exporttype`, `mode` | Aeron-waarden |
| `is_voicetrack`, `is_commblock` | Itemclassificatie |

De response is in beide modi een array in `data`; er wordt geen apart pagineringsobject toegevoegd.

## Databaseonderhoud

### `GET /api/db/maintenance/health`

Retourneert database-, tabel-, connectie- en querystatistieken.

| Veld | Betekenis |
|---|---|
| `database_name`, `database_version`, `schema_name` | Database-identificatie |
| `database_size`, `database_size_bytes` | Totale grootte, leesbaar en in bytes |
| `active_connections`, `max_connections`, `connection_usage_pct` | Connectiegebruik |
| `tables` | Status per tabel |
| `long_running_queries` | Queries boven de ingestelde tijdsdrempel |
| `needs_maintenance` | Of minimaal één tabel `VACUUM` of `ANALYZE` nodig heeft |
| `recommendations` | Operatoracties; `No issues detected` als de lijst anders leeg zou zijn |
| `checked_at` | Tijdstip van de controle |

Tabelvelden:

| Veld | Betekenis |
|---|---|
| `name`, `row_count` | Tabelnaam en geschat aantal rijen |
| `dead_tuples`, `dead_tuple_pct` | Dode rijen, absoluut en als percentage |
| `modifications_since_analyze` | Wijzigingen sinds de laatste analyse |
| `total_size`, `total_size_bytes` | Totale tabelgrootte |
| `table_size`, `table_size_bytes` | Datagrootte |
| `index_size`, `index_size_bytes` | Indexgrootte |
| `toast_size`, `toast_size_bytes` | TOAST-grootte |
| `last_vacuum`, `last_autovacuum` | Laatste vacuümtijden; kunnen `null` zijn |
| `last_analyze`, `last_autoanalyze` | Laatste analysetijden; kunnen `null` zijn |
| `seq_scans`, `idx_scans` | Scanstatistieken |
| `needs_vacuum`, `needs_analyze` | Afgeleide onderhoudssignalen |

Een item in `long_running_queries` bevat `pid`, `duration`, `query` en `state`. `query` is leeg tenzij `maintenance.expose_long_running_query_text` aanstaat.

## Back-ups

Deze endpoints bestaan alleen als `backup.enabled` aanstaat; anders volgt `404`. De applicatie vereist dan bij het opstarten `pg_dump` en `pg_restore`.

Een gemaakte back-up wordt vóór succes gecontroleerd met `pg_restore --list`. Er kan één back-up tegelijk draaien. S3-synchronisatie begint pas na een geslaagde back-up en validatie.

### `POST /api/db/backup`

Start een back-up op de achtergrond. De body is optioneel:

```json
{
  "compression": 9
}
```

`compression` accepteert `1` tot en met `9`; weglaten of `0` gebruikt `backup.default_compression`.

`202 Accepted`:

```json
{
  "success": true,
  "data": {
    "message": "Backup started in background",
    "check": "/api/db/backup/status"
  }
}
```

Een ongeldige compressiewaarde retourneert `400`; een tweede gelijktijdige aanvraag `409`.

### `GET /api/db/backup/status`

| Veld | Betekenis |
|---|---|
| `running` | Of een back-up draait |
| `started_at`, `ended_at` | Start- en eventuele eindtijd |
| `success` | Uitkomst van de back-up; betekenisvol zodra `ended_at` aanwezig is |
| `error` | Aanwezig na een mislukking |
| `filename` | Aanwezig zodra een bestandsnaam is toegekend |
| `s3_sync.synced` | Of de S3-upload voltooid is |
| `s3_sync.error` | Aanwezig na een mislukte S3-upload |

Tijdens een eerste of lopende run kan `success` `false` zijn zonder `error`. Bij ingeschakelde S3 kan `synced: false` zonder `error` betekenen dat de upload nog loopt.

### `GET /api/db/backups`

Retourneert:

- `backups`: items met `filename`, `size_bytes`, `size` en `created_at`;
- `total_size_bytes`;
- `total_count`.

### `GET /api/db/backups/{filename}`

Downloadt een lokaal back-upbestand met `Content-Type: application/octet-stream` en `Content-Disposition: attachment; filename=...`.

Alleen beheerde namen met het patroon `aeron-backup-*.dump` worden geaccepteerd. Een ongeldige naam retourneert `400`; een onbekend bestand `404`.

### `GET /api/db/backups/{filename}/validate`

Controleert het bestand met `pg_restore --list` en retourneert `200 OK` met:

```json
{
  "success": true,
  "data": {
    "filename": "aeron-backup-2026-08-10-030000.dump",
    "valid": false,
    "error": "backup validation failed: ..."
  }
}
```

`error` ontbreekt wanneer `valid` `true` is. Een ongeldig archief is dus een geslaagde API-call met `valid: false`.

### `DELETE /api/db/backups/{filename}`

Vereist een bevestigingsheader met exact dezelfde bestandsnaam:

```http
X-Confirm-Delete: aeron-backup-2026-08-10-030000.dump
```

De response bevat `message` en `filename`. Zonder juiste bevestiging volgt `400`. Bij ingeschakelde S3 wordt de verwijdering van de externe kopie op de achtergrond gestart.

## Bestandsbewaking

Deze endpoints bestaan alleen als `file_monitor.enabled` aanstaat; anders volgt `404`.

### `POST /api/file-monitor/check`

Start een controle van alle geconfigureerde bestanden. Handmatige en geplande controles kunnen niet tegelijk draaien.

```json
{
  "success": true,
  "data": {
    "message": "File monitor check started",
    "run_id": 43,
    "check": "/api/file-monitor/status"
  }
}
```

Status `202` betekent gestart; `409` betekent dat al een controle draait.

### `GET /api/file-monitor/status`

```json
{
  "success": true,
  "data": {
    "running": false,
    "run_id": 43,
    "completed_run_id": 43,
    "started_at": "2026-08-10T10:29:55Z",
    "last_check_at": "2026-08-10T10:30:00Z",
    "interval_seconds": 60,
    "checks": [
      {
        "name": "Nieuwsbulletin",
        "path": "/data/news.mp3",
        "max_age_minutes": 30,
        "file_exists": true,
        "file_age_minutes": 7.5,
        "last_modified": "2026-08-10T10:22:30Z",
        "is_stale": false,
        "in_alert": false
      }
    ]
  }
}
```

| Veld | Betekenis |
|---|---|
| `running` | Of nu een run draait |
| `run_id` | Laatst gestarte run; `0` vóór de eerste run |
| `completed_run_id` | Run die de zichtbare `checks` heeft geproduceerd |
| `started_at`, `last_check_at` | Starttijd van de laatste run en referentietijd van de laatste controle; ontbreken vóór de eerste run |
| `interval_seconds` | Geconfigureerd automatisch controle-interval |
| `checks` | Resultaat per bestand |

Checkvelden:

| Veld | Betekenis |
|---|---|
| `name` | Optionele naam uit de configuratie |
| `path`, `max_age_minutes` | Gecontroleerd pad en leeftijdsgrens |
| `file_exists` | `true`, `false`, of `null` wanneer bestaan niet kon worden vastgesteld |
| `file_age_minutes`, `last_modified` | Alleen aanwezig bij een bereikbaar bestand |
| `is_stale` | Bestand is te oud, ontbreekt of kon niet worden gecontroleerd |
| `in_alert` | Alert is actief; buiten `active_window` altijd `false` |
| `error` | Technische bestandsfout, indien van toepassing |
| `error_kind` | `not_found`, `permission_denied`, `stat_timeout` of `stat_error`; ontbreekt bij succes |

De eerste controle na een herstart meet wel, maar verstuurt nog geen meldingen. `is_stale` blijft buiten een `active_window` zichtbaar; alleen `in_alert`, e-mail en de algemene healthstatus worden daar onderdrukt.

## Aanwezigheidscontrole van playlist-audio

Deze endpoints bestaan alleen als `media_file_check.enabled` aanstaat; anders volgt `404`.

De controle leest audioreferenties uit de playlist. `drive_mounts` vertaalt eerst een Windows-driveletter naar een exact hostpad. Als dat pad niet bestaat, doorzoekt `search_dirs` een bestandsnaamindex, eerst met en daarna zonder extensie. Een fout bij het controleren van het exacte pad levert `stat_error` op zonder fallback. Matching is standaard hoofdletterongevoelig.

### `POST /api/media/files/check`

Start een controle op de achtergrond. Scopeprioriteit: `block_id`, daarna `date`, daarna `from`/`to`, anders vandaag.

| Parameter | Gedrag |
|---|---|
| `block_id` | Eén playlistblok; moet een UUID zijn |
| `date` | Eén datum als `YYYY-MM-DD` |
| `from`, `to` | Inclusief datumbereik; één open grens is toegestaan |
| `limit` | Niet-negatief maximum; `0` of weglaten betekent geen limiet |
| `include_voicetracks` | Alleen `true` neemt voicetracks mee |

Als beide bereikgrenzen staan, mag het inclusieve bereik niet groter zijn dan `media_file_check.max_range_days`.

```json
{
  "success": true,
  "data": {
    "message": "Media file check started",
    "run_id": 12,
    "check": "/api/media/files/check/status"
  }
}
```

Status `202` betekent gestart; ongeldige parameters geven `400` en een al lopende controle `409`.

### `GET /api/media/files/check/status`

| Veld | Betekenis |
|---|---|
| `running` | Of nu een run draait |
| `run_id` | Laatst gestarte run; `0` vóór de eerste run |
| `completed_run_id` | Run die `result` heeft geproduceerd |
| `started_at` | Starttijd; ontbreekt vóór de eerste run |
| `result` | Laatste voltooide resultaat; `null` vóór de eerste voltooide run |

`result` bevat:

| Veld | Betekenis |
|---|---|
| `checked_at` | Verwerkingstijdstip |
| `scope` | Kan de effectieve `date`, `from`, `to`, `block_id`, `lookahead_days`, `limit` en `exclude_voicetracks` bevatten |
| `summary` | `total`, `present`, `missing`, `ambiguous`, `no_reference` en `errors` |
| `items` | Resultaat per playlistitem |
| `error` | Fout van de hele run, bijvoorbeeld bij ophalen of indexeren |

Itemvelden:

| Veld | Betekenis |
|---|---|
| `trackid`, `artist`, `tracktitle` | Trackgegevens |
| `start_time`, `block_id`, `block` | Playlistgegevens |
| `status` | `present`, `missing`, `ambiguous`, `no_reference` of `stat_error` |
| `db_reference` | Gebruikte Aeron-referentie |
| `checked_paths` | Geprobeerde paden en zoekacties |
| `matches` | Gevonden bestanden |
| `match_type` | `exact_path`, `filename` of `filename_noext`; ontbreekt zonder match |
| `error` | Aanwezig bij `stat_error` |

| Status | Betekenis |
|---|---|
| `present` | Precies één bestand gevonden |
| `missing` | Geen bestand gevonden |
| `ambiguous` | Meerdere bestanden gevonden |
| `no_reference` | Geen bruikbare audioreferentie in Aeron |
| `stat_error` | Pad of zoekindex kon niet betrouwbaar worden gecontroleerd |

Geplande runs kunnen met `lookahead_days` vooruitkijken en meldingen versturen. Handmatige runs versturen geen e-mail en beïnvloeden `GET /api/health` niet.

## Notificaties

### `POST /api/notifications/test-email`

Valideert de Microsoft Graph-configuratie en verstuurt een testmail.

`200 OK`:

```json
{
  "success": true,
  "data": {
    "message": "Test email sent successfully"
  }
}
```

Mogelijke fouten:

| Code | `error` |
|---|---|
| `400` | `Notification configuration invalid: ...` |
| `502` | `Authentication with mail provider failed` |
| `502` | `Failed to send test email` |

## Configuratie

[`config.example.json`](config.example.json) bevat alle opties. API-relevant zijn vooral:

| Sectie | Bepaalt |
|---|---|
| `api` | Authenticatie, rate limiting, time-outs en uploadlimiet |
| `image` | Afmetingen, JPEG-kwaliteit, minimumgrootte en download-/pixellimieten |
| `maintenance` | Drempels, querytekst en geplande databasecontrole |
| `backup` | Beschikbaarheid, opslag, retentie, compressie, planning en S3 |
| `file_monitor` | Bestanden, leeftijdsgrenzen, actieve tijdvensters en interval |
| `media_file_check` | Mounts, zoekmappen, matching, bereik en planning |
| `notifications` | Microsoft Graph-afzender en ontvangers |

Cron-schema's en `active_window` gebruiken de lokale tijdzone van het proces. Stel in Docker daarom `TZ`, bijvoorbeeld `TZ=Europe/Amsterdam`.
