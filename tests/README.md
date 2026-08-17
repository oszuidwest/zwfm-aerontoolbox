# Aeron Toolbox Tests

This directory contains test fixtures and data for the Aeron Toolbox project.

## Structure

```
tests/
├── fixtures/              # Test data
│   └── mock_data.sql      # Mock database data (artists, tracks, playlist)
├── docker-compose.test.yml # Test database (and optional MinIO) setup
└── README.md              # This file
```

## Test Execution

All tests are executed through GitHub Actions. See `.github/workflows/comprehensive-test.yml` for the complete test suite.

## Local Testing (Optional)

If you want to test locally, you can:

### 1. Start Test Database

```bash
cd tests
docker compose -f docker-compose.test.yml up -d
```

This starts a PostgreSQL container on port 5433 with mock data. The MinIO
container (used by the S3 test suite) is behind a compose profile and only
starts when you add `--profile s3`.

### 2. Create a test config and run the application

```bash
# Create test config (see config.example.json, use port 5433)
cp config.example.json test_config.json
# Edit test_config.json: set port to "5433"

go build -o zwfm-aerontoolbox .
./zwfm-aerontoolbox -config=test_config.json -port=8080
```

## Test Data

The `fixtures/mock_data.sql` file contains:
- 1000 artists
  - 50 artists with a dummy PNG image (deterministic: the 50 lowest artist IDs)
  - 950 artists without images
- 1100 tracks with realistic data
  - 50 tracks with a dummy PNG image (deterministic: the 50 lowest title IDs)
  - 1050 tracks without images
- Playlist items covering the mocked broadcast day

## CI/CD Integration

GitHub Actions automatically:
1. Sets up a test database
2. Loads mock data
3. Runs all integration tests
4. Reports results

## Writing New Tests

1. Add test data to `fixtures/mock_data.sql`
2. Update the GitHub Actions workflow in `.github/workflows/comprehensive-test.yml`

## Test Configuration

The test configuration uses:
- **Database**: PostgreSQL 17
- **Port**: 5433 (both CI and local; the container maps 5433 -> 5432)
- **API Keys**: test-api-key-12345, another-test-key-67890

## System Requirements for Local Testing

When testing backup functionality locally, you need:
- **PostgreSQL client tools**: `pg_dump` and `pg_restore`

```bash
# Debian/Ubuntu
apt-get install postgresql-client

# macOS
brew install libpq

# The GitHub Actions workflow installs these automatically
```

The application validates these tools at startup when `backup.enabled: true`. Without them, it will refuse to start.
