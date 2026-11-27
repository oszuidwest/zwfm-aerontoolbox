# Aeron Toolbox API

HTTP-API voor beheer en onderhoud van Aeron. Paden zijn volledig. De server luistert standaard op poort `8080` (`http://localhost:8080`).

## Conventies

### Authenticatie

Als `api.enabled` aanstaat, vragen alle `/api`-endpoints deze header:

```http
X-API-Key: jouw-api-sleutel
```

`GET /health` blijft publiek. Gebruik per omgeving een eigen sleutel met minstens 32 bytes entropie. Zie [README ▸ Authenticatie](README.md#authenticatie) voor de standaardinstelling, het maken van sleutels en de risico's van authenticatie uitzetten.

### Rate limiting

`api.rate_limit_enabled` zet een fixed-window-limiet op `/api`. Elke geldige API-sleutel heeft een eigen budget. Andere verzoeken tellen per direct peer-adres (`RemoteAddr`); proxyheaders worden genegeerd.

Boven de limiet volgt `429 Too Many Requests` met een `Retry-After`-header en de gewone JSON-fout. `GET /health` valt buiten de limiet.

### JSON-responses

Succes:

```json
{
  "success": true,
  "data": {}
}
```

Fout:

```json
{
  "success": false,
  "error": "error message"
}
```

De voorbeelden tonen de volledige response. Afbeeldingen en back-updownloads zijn binair, zonder JSON-wrapper. Alleen de fout van `GET /health` bevat naast `error` ook `data`.

Onveilige cross-originverzoeken uit een browser krijgen `403 Forbidden` in platte tekst, nog vóór de API-handler.

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

Bij een synchrone `5xx`-fout gaan interne details naar de log en krijgt de client alleen veilige fouttekst. Runtimeberichten en veldwaarden zijn Engels.

### Achtergrondtaken pollen

Bestandscontroles en back-ups geven een `run_id` terug. Poll het opgegeven statusendpoint tot:

```text
completed_run_id >= run_id && running == false
```

Bij `completed_run_id == run_id` hoort het resultaat bij jouw run. Een hogere waarde betekent dat een latere run, bijvoorbeeld een geplande, het al heeft vervangen.

Handmatige en geplande back-ups delen één teller. Start er na jouw back-up een geplande, dan krijgt die een hogere `run_id`. De tellers staan alleen in het geheugen en beginnen na een herstart weer bij `0`.

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

`HEAD` werkt op alle `GET`-routes en geeft dezelfde response zonder body.

## Status

### `GET /health`

Publieke readinesscontrole voor containers en load balancers. Gebruik het niet als liveness-check die het proces herstart: bij database-uitval geeft het `503`, en een herstart lost dat niet op.

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

Geeft altijd `200 OK` als de status kan worden opgebouwd. Het oordeel staat in `status` en `database_status`.

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

Zonder databaseverbinding is `status` `unhealthy`. Met verbinding wordt het `degraded` bij een bijna verlopen mailsecret, bij `checks_alerting > 0` of bij problemen in de laatste geplande mediacontrole. Dat laatste telt alleen als de scheduler van die controle aanstaat.

`checks_stale` telt alle verouderde of onbereikbare bestanden; `checks_alerting` houdt rekening met `active_window`. `media_file_check.problems` is de som van `missing`, `ambiguous` en `errors` in de laatste geplande run, of `1` als die run zelf mislukte.

## Artiesten en tracks

Hier staat `{type}` voor `artists` of `tracks`. `{id}` is een UUID v4, hoofdletterongevoelig.

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

Een onbekend ID geeft `404`, een ID dat geen UUID v4 is `400`.

### Afbeelding downloaden

```http
GET /api/{type}/{id}/image
```

De response is binair, met `Content-Length` en een gedetecteerd `Content-Type` zoals `image/jpeg`, `image/png` of `image/webp`. Zonder afbeelding volgt een JSON-fout met `404`.

### Afbeelding uploaden

```http
POST /api/{type}/{id}/image
Content-Type: application/json
```

Geef precies één bron op:

```json
{"url":"https://voorbeeld.nl/afbeelding.jpg"}
```

of:

```json
{"image":"<base64-gecodeerde JPEG- of PNG-data>"}
```

Response voor een track:

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

Bij een artiest ontbreekt `track`. Fouten zijn onder meer `400` (ongeldige invoer), `404` (onbekend ID), `408` (body niet op tijd gelezen) en `413` (JSON-body te groot).

Alleen JPEG en PNG worden geaccepteerd. Een URL moet HTTP of HTTPS gebruiken. Een afbeelding van precies de doelafmetingen wordt ongewijzigd opgeslagen. Een grotere afbeelding wordt eerst verkleind met behoud van verhouding. Daarna wordt de afbeelding als JPEG gecodeerd. Is die JPEG niet kleiner dan het origineel, dan worden de oorspronkelijke bytes opgeslagen. Met `image.reject_smaller` worden afbeeldingen kleiner dan de doelafmetingen geweigerd.

### Afbeelding verwijderen

```http
DELETE /api/{type}/{id}/image
```

De response bevat `message` en `artist_id` of `track_id`. Een onbekend ID geeft `404`. Een record zonder afbeelding geeft gewoon een geslaagde response.

### Alle afbeeldingen verwijderen

```http
DELETE /api/artists/bulk-delete
DELETE /api/tracks/bulk-delete
X-Confirm-Bulk-Delete: DELETE ALL
```

De header moet precies `DELETE ALL` zijn, anders volgt `400`. De response bevat `deleted` en `message`.

## Playlist

### `GET /api/playlist`

Zonder `block_id` geeft dit endpoint de playlistblokken van één datum, met hun tracks.

| Parameter | Gedrag |
|---|---|
| `date` | `YYYY-MM-DD`; standaard de huidige datum van PostgreSQL |

Een blok bevat `blockid`, `name`, `date`, `start_time`, `end_time` en `tracks`.

### `GET /api/playlist?block_id={uuid}`

Met `block_id` geeft het alleen de tracks van dat blok.

| Parameter | Gedrag |
|---|---|
| `block_id` | Playlistblok-UUID; zet de blokmodus aan |
| `limit` | Positief maximum; zonder waarde geldt geen limiet |
| `offset` | Niet-negatief; werkt alleen samen met `limit` |
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

In beide modi is `data` een array, zonder apart pagineringsobject.

## Databaseonderhoud

### `GET /api/db/maintenance/health`

Geeft database-, tabel-, connectie- en querystatistieken.

| Veld | Betekenis |
|---|---|
| `database_name`, `database_version`, `schema_name` | Database-identificatie |
| `database_size`, `database_size_bytes` | Totale grootte, leesbaar en in bytes |
| `active_connections`, `max_connections`, `connection_usage_pct` | Connectiegebruik |
| `frozen_xid_age`, `autovacuum_freeze_max_age` | Transactie-ID-leeftijd van de database en de grens waarboven autovacuum een anti-wraparound-VACUUM start; boven die grens volgt een aanbeveling |
| `tables` | Status per tabel |
| `long_running_queries` | Sessies waarvan de transactie langer loopt dan de ingestelde tijdsdrempel |
| `needs_maintenance` | Of minimaal één tabel `VACUUM` of `ANALYZE` nodig heeft |
| `recommendations` | Acties voor de beheerder; `No issues detected` als er geen zijn |
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

Een item in `long_running_queries` bevat `pid`, `duration`, `query`, `state`, `application_name`, `user`, `started_at` en `xmin_age`. `started_at` is de start van de transactie, of van de query als er geen transactie is; `duration` telt vanaf dat moment. `query` is leeg tenzij `maintenance.expose_long_running_query_text` aanstaat. `xmin_age` is groter dan 0 als de sessie de xmin-horizon tegenhoudt; VACUUM kan dan geen dode rijen opruimen die nieuwer zijn dan haar snapshot. De sessie met de hoogste `xmin_age` staat bovenaan `recommendations`. Sessies van andere databasegebruikers staan alleen in de lijst als de toolboxgebruiker superuser is, `pg_read_all_stats` heeft of de rechten van de rol van die sessie heeft.

## Back-ups

Deze endpoints bestaan alleen als `backup.enabled` aanstaat; anders volgt `404`. De applicatie start dan niet zonder `pg_dump` en `pg_restore`; zie de [README](README.md#andere-installatiemethoden) voor installatie en versies.

Een nieuwe back-up geldt pas als geslaagd na controle met `pg_restore --list`. Er draait maximaal één back-up tegelijk. S3-synchronisatie start pas na een geslaagde back-up en validatie.

### `POST /api/db/backup`

Start een back-up op de achtergrond. De body is optioneel:

```json
{
  "compression": 9
}
```

`compression` is `1` tot en met `9`; weglaten of `0` gebruikt `backup.default_compression`.

`202 Accepted`:

```json
{
  "success": true,
  "data": {
    "message": "Backup started in background",
    "run_id": 7,
    "check": "/api/db/backup/status"
  }
}
```

Een ongeldige compressiewaarde geeft `400`, een tweede gelijktijdige aanvraag `409`.

### `GET /api/db/backup/status`

| Veld | Betekenis |
|---|---|
| `running` | Of een back-up draait |
| `run_id` | Laatst gestarte back-up; `0` vóór de eerste |
| `completed_run_id` | Laatst afgeronde back-up; `0` vóór de eerste |
| `started_at`, `ended_at` | Start- en eventuele eindtijd |
| `success` | Uitkomst; betekenisvol zodra `ended_at` er is |
| `error` | Aanwezig na een mislukking |
| `filename` | Aanwezig zodra een bestandsnaam is toegekend |
| `s3_sync.synced` | Of de S3-upload voltooid is |
| `s3_sync.error` | Aanwezig na een mislukte S3-upload |

Vóór de eerste run en tijdens een run kan `success` `false` zijn zonder `error`. Met S3 aan kan `synced: false` zonder `error` betekenen dat de upload nog loopt.

### `GET /api/db/backups`

Geeft `backups` (items met `filename`, `size_bytes`, `size` en `created_at`), `total_size_bytes` en `total_count`.

### `GET /api/db/backups/{filename}`

Downloadt een lokaal back-upbestand met `Content-Type: application/octet-stream` en `Content-Disposition: attachment; filename=...`.

Alleen namen volgens `aeron-backup-*.dump` worden geaccepteerd. Een ongeldige naam geeft `400`, een onbekend bestand `404`.

### `GET /api/db/backups/{filename}/validate`

Controleert het bestand met `pg_restore --list`. Ook een ongeldig archief geeft `200 OK`:

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

`error` ontbreekt als `valid` `true` is.

### `DELETE /api/db/backups/{filename}`

Vereist een bevestigingsheader met precies de bestandsnaam:

```http
X-Confirm-Delete: aeron-backup-2026-08-10-030000.dump
```

De response bevat `message` en `filename`. Zonder juiste bevestiging volgt `400`. Met S3 aan start ook de verwijdering van de externe kopie, op de achtergrond.

## Bestandsbewaking

Deze endpoints bestaan alleen als `file_monitor.enabled` aanstaat; anders volgt `404`.

### `POST /api/file-monitor/check`

Start een controle van alle geconfigureerde bestanden. Een handmatige en een geplande controle draaien nooit tegelijk.

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

`202` betekent gestart, `409` dat er al een controle draait.

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
| `error` | Technische bestandsfout, als die er is |
| `error_kind` | `not_found`, `permission_denied`, `stat_timeout` of `stat_error`; ontbreekt bij succes |

De eerste controle na een herstart meet wel, maar verstuurt nog geen meldingen. Buiten `active_window` blijft `is_stale` zichtbaar; alleen `in_alert`, e-mail en de healthstatus worden dan onderdrukt.

## Aanwezigheidscontrole van playlist-audio

Deze endpoints bestaan alleen als `media_file_check.enabled` aanstaat; anders volgt `404`.

De controle leest audioreferenties uit de playlist. `drive_mounts` vertaalt eerst een Windows-driveletter naar een exact hostpad. Bestaat dat pad niet, dan wordt gezocht in een bestandsnaamindex van `search_dirs`, eerst met en dan zonder extensie. `$RECYCLE.BIN`, `RECYCLER` en `System Volume Information` worden overgeslagen. Een fout bij het controleren van het exacte pad geeft `stat_error`, zonder verder te zoeken. Matching is standaard hoofdletterongevoelig.

### `POST /api/media/files/check`

Start een controle op de achtergrond. De scope is `block_id`, anders `date`, anders `from`/`to`, anders vandaag.

| Parameter | Gedrag |
|---|---|
| `block_id` | Eén playlistblok, als UUID |
| `date` | Eén datum als `YYYY-MM-DD` |
| `from`, `to` | Inclusief datumbereik; één open grens is toegestaan |
| `limit` | Niet-negatief maximum; `0` of weglaten betekent geen limiet |
| `include_voicetracks` | Alleen `true` neemt voicetracks mee |

Met beide grenzen mag het bereik, inclusief, niet groter zijn dan `media_file_check.max_range_days`.

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

`202` betekent gestart, `400` ongeldige parameters, `409` dat er al een controle draait.

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

Geplande runs kunnen met `lookahead_days` vooruitkijken en meldingen versturen. Handmatige runs versturen geen e-mail en tellen niet mee in `GET /api/health`.

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

Cron-schema's en `active_window` gebruiken de lokale tijdzone van het proces. Zet in Docker daarom `TZ`, bijvoorbeeld `TZ=Europe/Amsterdam`.

`active_window` is een lijst vensters, gescheiden door komma's, zoals `06:00-22:00` of `mon-fri 06:25-21:00, sat-sun 07:25-18:00`. Dagen voor een venster beperken het tot die dagen: `mon` tot en met `sun`, los of als reeks (`mon-fri` is maandag tot en met vrijdag). Een reeks mag over het weekend lopen, zoals `fri-mon`. Een venster zonder dagen geldt elke dag. Een tijdstip valt binnen `active_window` als minstens één venster past; vensters overschrijven elkaar niet. De begintijd hoort bij het venster, de eindtijd niet. Een venster over middernacht, zoals `fri 22:00-02:00`, hoort bij de begindag en loopt dus tot zaterdag 02:00. Een gelijke begin- en eindtijd met dagen is 24 uur vanaf de begintijd: `sat-sun 00:00-00:00` is de hele zaterdag en zondag. Zonder dagen is dat een fout; laat het veld dan leeg.
