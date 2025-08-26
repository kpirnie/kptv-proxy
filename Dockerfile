# Build stage - compile Go application
FROM docker.io/golang:alpine AS builder

RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=v1.0.57" -o kptv-proxy .

# Final stage - your working ffmpeg setup + Go app + Admin interface
FROM docker.io/alpine:latest

# Copy static ffmpeg binaries (your working approach)
#COPY --from=docker.io/mwader/static-ffmpeg:latest /ffmpeg /usr/local/bin/
COPY --from=docker.io/mwader/static-ffmpeg:latest /ffprobe /usr/local/bin/

# Copy compiled Go application
COPY --from=builder /app/kptv-proxy /usr/local/bin/kptv-proxy

# Copy admin interface static files
COPY --from=builder /app/static /static

# Setup (adapted from your container)
RUN mkdir -p /dev/dri && \
    chmod 777 /dev/dri && \
    addgroup -g 1000 kptv && \
    adduser -u 1000 -G kptv -D kptv && \
    #chmod 755 /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/kptv-proxy && \
    chmod 755 /usr/local/bin/ffprobe /usr/local/bin/kptv-proxy && \
    # Make static files readable and settings writable by kptv user
    mkdir -p /settings && \
    chmod -R 755 /static && \
    chown -R kptv:kptv /settings && \
    chmod 755 /settings && \
    # Install CA certificates for HTTPS
    apk add --no-cache ca-certificates && \
    # Verify ffmpeg works
    /usr/local/bin/ffprobe -version
    #/usr/local/bin/ffmpeg -version && /usr/local/bin/ffprobe -version

WORKDIR /workspace
USER kptv

ENV PATH="/usr/local/bin:${PATH}"

EXPOSE 8080
CMD ["/usr/local/bin/kptv-proxy"]