# Aeron Toolbox-tests

Deze map bevat testdata en de testdatabase voor Aeron Toolbox.

## Structuur

```
tests/
├── fixtures/              # Testdata
│   ├── mock_data.sql      # Mockdata voor de database (artiesten, tracks, playlist)
│   └── s3_config.json     # S3-inloggegevens voor SeaweedFS
├── docker-compose.test.yml # Testdatabase (en optioneel SeaweedFS)
└── README.md              # Dit bestand
```

## Tests in GitHub Actions

Alle tests draaien in `.github/workflows/comprehensive-test.yml`, bij elke pull request naar `main`, voor elke release (`release.yml`) en handmatig. De workflow heeft drie jobs:

1. `build` bouwt de applicatie.
2. `lint` roept de gedeelde workflow `go-ci.yml` aan uit `oszuidwest/.github-templates`. Die draait `go test -race -shuffle=on -v ./...`, `go vet`, een formatcontrole, golangci-lint, deadcode en govulncheck.
3. `integration` start per suite de testdatabase met de mockdata en test de draaiende applicatie via HTTP. De suites zijn `api-basic`, `artists`, `tracks`, `maintenance`, `backup`, `system-validation`, `s3` en `edge-cases`.

## Lokaal testen

### 1. Testdatabase starten

```bash
cd tests
docker compose -f docker-compose.test.yml up -d
```

Dit start een PostgreSQL-container op poort 5433 met de mockdata. De SeaweedFS-container (voor de suite `s3`) zit achter een compose-profiel en start alleen met `--profile s3`.

### 2. Testconfiguratie maken en de applicatie starten

```bash
cd ..
# Maak een testconfiguratie (zie config.example.json)
cp config.example.json test_config.json
# Pas test_config.json aan: database.port "5433", database.name "aeron_db",
# database.password "aeron123" en minstens één sleutel in api.keys

go build -o zwfm-aerontoolbox .
./zwfm-aerontoolbox -config=test_config.json -port=8080
```

## Testdata

`fixtures/mock_data.sql` bevat:
- 1000 artiesten
  - 50 met een dummy-PNG (vast: de 50 laagste artist-ID's)
  - 950 zonder afbeelding
- 1100 tracks
  - 50 met een dummy-PNG (vast: de 50 laagste title-ID's)
  - 1050 zonder afbeelding
- Playlistitems voor de gesimuleerde uitzenddag

## Nieuwe tests schrijven

1. Voeg testdata toe aan `fixtures/mock_data.sql`.
2. Pas de workflow `.github/workflows/comprehensive-test.yml` aan.

## Testconfiguratie

De tests gebruiken PostgreSQL 17 op poort 5433, in CI en lokaal. De container koppelt 5433 aan 5432. De API-sleutels zijn `test-api-key-12345` en `another-test-key-67890`.

## Benodigdheden voor lokaal testen

Voor de back-uptests heb je `pg_dump` en `pg_restore` nodig. Zie de versie-eisen in de [README](../README.md).

```bash
# Debian/Ubuntu
apt-get install postgresql-client

# macOS
brew install libpq

# De GitHub Actions-workflow installeert deze tools zelf
```

Met `backup.enabled: true` controleert de applicatie bij het opstarten of deze tools bestaan. Ontbreken ze, dan start de applicatie niet.
