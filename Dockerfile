# Build stage
FROM golang:1.27.0-alpine3.24 AS builder

# Install the CA bundle needed for module downloads.
RUN apk add --no-cache ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build arguments for cross-compilation
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

# Build the binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o zwfm-aerontoolbox .

# Runtime stage
FROM alpine:3.24.1

LABEL org.opencontainers.image.source="https://github.com/oszuidwest/zwfm-aerontoolbox"
LABEL org.opencontainers.image.description="Headless REST API toolbox for the Aeron radio automation system"
LABEL org.opencontainers.image.licenses="MIT"

# pg_dump must be at least as new as the PostgreSQL server it backs up.
RUN apk --no-cache upgrade && \
    apk --no-cache add ca-certificates tzdata postgresql17-client

# Create non-root user
RUN addgroup -g 1000 aeron && \
    adduser -u 1000 -G aeron -s /sbin/nologin -D -H aeron

# Set working directory
WORKDIR /app

# Create backup directory
RUN install -d -o aeron -g aeron -m 0755 /backups

# Copy binary from builder
COPY --from=builder --chown=0:0 --chmod=0555 /app/zwfm-aerontoolbox /app/zwfm-aerontoolbox

# Runtime config is mounted at /app/config.json; never bake local config into the image.

# Switch to non-root user
USER 1000:1000

# Expose API port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["wget", "-q", "-T", "3", "--spider", "http://127.0.0.1:8080/health"]

# Start API server by default
ENTRYPOINT ["/app/zwfm-aerontoolbox"]
CMD ["-port=8080"]
