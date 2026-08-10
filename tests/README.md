# Aeron Toolbox Tests

This directory contains test fixtures and data for the Aeron Toolbox project.

## Structure

```
tests/
├── fixtures/              # Test data
│   └── mock_data.sql      # Mock database data (artists, tracks, playlist)
├── docker-compose.test.yml # Test database setup
├── Dockerfile.testdb      # Test database image
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

This starts a PostgreSQL container on port 5433 with mock data.

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
- 80 popular artists (international and Dutch)
  - 10 artists with dummy JPEG images (Queen, ABBA, Madonna, etc.)
  - 70 artists without images
- 130+ tracks with realistic data
  - 10 tracks with dummy JPEG images (Bohemian Rhapsody, Dancing Queen, etc.)
  - 120+ tracks without images

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
- **Database**: PostgreSQL 18
- **Port**: 5432 (CI) or 5433 (local)
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
