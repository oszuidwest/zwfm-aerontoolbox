# Aeron Toolbox

Aeron Toolbox is een onofficiële HTTP-API voor beheer en onderhoud van Aeron: afbeeldingen, media, databasecontroles, back-ups en bestandsbewaking.

> [!WARNING]
> Niet ontwikkeld door of in samenwerking met Nextwave Broadcast. Maak vooraf een back-up en gebruik de software op eigen risico.

## Functionaliteit

- Albumhoezen en artiestfoto's uploaden en optimaliseren
- Artiesten, tracks en playlists met metadata opvragen
- De databasestatus controleren en problemen melden
- Databaseback-ups maken, valideren, downloaden en naar S3 synchroniseren
- Ontbrekende of verouderde bestanden signaleren
- Controleren of geplande audio op schijf staat

## Snel starten

Vereist: toegang tot de PostgreSQL-database van Aeron.

```bash
wget https://raw.githubusercontent.com/oszuidwest/zwfm-aerontoolbox/main/config.example.json -O config.json
wget https://raw.githubusercontent.com/oszuidwest/zwfm-aerontoolbox/main/docker-compose.example.yml -O docker-compose.yml

# Pas config.json aan en start de container.
docker compose up -d
```

De meegeleverde Docker-configuratie gebruikt `Europe/Amsterdam` voor geplande taken. Voeg voor `file_monitor` en `media_file_check` de betrokken hostmappen als volumes toe aan `docker-compose.yml`.

### Andere installatiemethoden

- Download een Linux- of macOS-binary via [GitHub Releases](https://github.com/oszuidwest/zwfm-aerontoolbox/releases).
- Zelf bouwen vereist Go 1.26 of nieuwer:

```bash
git clone https://github.com/oszuidwest/zwfm-aerontoolbox.git
cd zwfm-aerontoolbox
cp config.example.json config.json
go build -o zwfm-aerontoolbox .
./zwfm-aerontoolbox -config=config.json -port=8080
```

Voor back-ups hebben installaties buiten Docker ook `pg_dump` en `pg_restore` nodig.

## Configuratie

Kopieer [`config.example.json`](config.example.json) naar `config.json` en pas deze secties aan:

| Sectie | Doel |
|---|---|
| `database` | PostgreSQL-verbinding en schema |
| `image` | Afmetingen, kwaliteit en limieten voor afbeeldingen |
| `api` | Authenticatie, time-outs en rate limiting |
| `maintenance` | Databasecontroles en planning |
| `backup` | Opslag, retentie, planning en S3-synchronisatie |
| `file_monitor` | Vaste bestanden bewaken |
| `media_file_check` | Playlist-audio op schijf controleren |
| `notifications` | E-mailmeldingen via Microsoft Graph |
| `log` | Logniveau en uitvoerformaat |

Gebruik in productie unieke API-sleutels met minimaal 32 bytes entropie, bijvoorbeeld gegenereerd met `openssl rand -base64 32`. `config.json` bevat geheimen en hoort niet in versiebeheer.

Zie [API.md](API.md) voor details over instellingen en endpoints.

## Voorbeelden

Publieke statuscontrole:

```bash
curl http://localhost:8080/health
```

Artiestafbeelding uploaden via een URL:

```bash
curl -X POST http://localhost:8080/api/artists/{id}/image \
  -H "X-API-Key: jouw-api-sleutel" \
  -H "Content-Type: application/json" \
  -d '{"url":"https://example.com/artist.jpg"}'
```

## Licentie

MIT. Zie [LICENSE](LICENSE).
