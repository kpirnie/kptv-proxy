# Build stage - use alpine for smaller builder
FROM docker.io/golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=v10102025.01" -trimpath -o kptv-proxy .

# Final stage - keep all GPU drivers
FROM docker.io/debian:bookworm-slim

# Install all GPU drivers + curl in one layer
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    mesa-va-drivers \
    mesa-vulkan-drivers \
    intel-media-va-driver \
    libva-drm2 \
    libva-x11-2 \
    vulkan-tools \
    va-driver-all \
    curl \
    && rm -rf /var/lib/apt/lists/* \
    && apt-get clean

RUN groupadd --system --gid 107 render

COPY --from=docker.io/mwader/static-ffmpeg:latest /ffmpeg /usr/local/bin/
COPY --from=docker.io/mwader/static-ffmpeg:latest /ffprobe /usr/local/bin/
COPY --from=builder /app/kptv-proxy /usr/local/bin/kptv-proxy
COPY --from=builder /app/static /static

RUN mkdir -p /dev/dri && \
    addgroup --gid 1000 kptv && \
    adduser --uid 1000 --gid 1000 --disabled-password --gecos "" kptv && \
    usermod -a -G video kptv && \
    usermod -a -G render kptv && \
    chmod 755 /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/kptv-proxy && \
    mkdir -p /settings && \
    chmod -R 755 /static && \
    chown -R kptv:kptv /settings && \
    chmod 775 /settings

WORKDIR /workspace
USER kptv

ENV PATH="/usr/local/bin:${PATH}"

EXPOSE 8080
CMD ["/usr/local/bin/kptv-proxy"]