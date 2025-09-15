# Build stage - compile Go application
FROM docker.io/golang:alpine AS builder

# we'll need git to pull other go libraries
RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=v1.4.37" -o kptv-proxy .

# Final stage - Debian with full GPU support
FROM docker.io/debian:bookworm-slim

# Install graphics drivers and dependencies for both Intel and AMD hardware acceleration
RUN apt-get update && apt-get install -y \
    ca-certificates \
    mesa-va-drivers \
    mesa-vulkan-drivers \
    intel-media-va-driver \
    libva-drm2 \
    libva-x11-2 \
    vulkan-tools \
    # Add these for WSL2 GPU support:
    va-driver-all \
    && rm -rf /var/lib/apt/lists/*

# Create the render group that Debian doesn't have by default
RUN groupadd --system --gid 107 render

# Copy static ffmpeg binaries
COPY --from=docker.io/mwader/static-ffmpeg:latest /ffmpeg /usr/local/bin/
COPY --from=docker.io/mwader/static-ffmpeg:latest /ffprobe /usr/local/bin/

# Copy compiled Go application
COPY --from=builder /app/kptv-proxy /usr/local/bin/kptv-proxy

# Copy admin interface static files
COPY --from=builder /app/static /static

# Setup with proper GPU permissions
RUN mkdir -p /dev/dri && \
    addgroup --gid 1000 kptv && \
    adduser --uid 1000 --gid 1000 --disabled-password --gecos "" kptv && \
    usermod -a -G video kptv && \
    usermod -a -G render kptv && \
    chmod 755 /usr/local/bin/ffmpeg /usr/local/bin/ffprobe /usr/local/bin/kptv-proxy && \
    mkdir -p /settings && \
    chmod -R 755 /static && \
    chown -R kptv:kptv /settings && \
    chmod 755 /settings

# Verify installations
RUN /usr/local/bin/ffmpeg -version && \
    /usr/local/bin/ffprobe -version && \
    echo "Available VAAPI drivers:" && \
    find /usr/lib/x86_64-linux-gnu/dri/ -name "*.so" | head -10 && \
    echo "User groups:" && \
    groups kptv

WORKDIR /workspace
USER kptv

ENV PATH="/usr/local/bin:${PATH}"

EXPOSE 8080
CMD ["/usr/local/bin/kptv-proxy"]