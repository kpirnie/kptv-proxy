# Build stage - use alpine for smaller builder
FROM docker.io/golang:1.26.2-alpine AS builder

RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w \
    -X 'kptv-proxy/app.Version=v$(date -u +%Y%m%d%H.%M)' \
    -X 'kptv-proxy/app.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)' \
    -X 'kptv-proxy/app.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)'" \
    -trimpath -o kptv-proxy .

# Final stage - keep all GPU drivers
FROM docker.io/debian:trixie-slim

# Install curl and necessities curl in one layer
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/* \
    && rm -rf /usr/share/doc /usr/share/man \
    && find /usr/share/locale -mindepth 1 -maxdepth 1 ! -name 'en' ! -name 'en_US' -exec rm -rf {} + 2>/dev/null || true

# Create groups in one layer
RUN groupadd --system --gid 107 render \
    && groupadd --gid 1000 kptv \
    && useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash kptv \
    && usermod -a -G video,render kptv \
    && mkdir -p /dev/dri /settings /static \
    && chown -R kptv:kptv /settings \
    && chmod 775 /settings

COPY --from=builder /app/kptv-proxy /usr/local/bin/kptv-proxy
COPY --from=builder /app/static/*.html /static/
COPY --from=builder /app/static/admin.css /static/
COPY --from=builder /app/static/admin.js /static/
COPY loading.ts /static/

WORKDIR /workspace
USER kptv

ENV PATH="/usr/local/bin:${PATH}" GOGC=100

EXPOSE 8080
CMD ["/usr/local/bin/kptv-proxy"]