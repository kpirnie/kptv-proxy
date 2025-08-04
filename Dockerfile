# Build stage
FROM docker.io/golang:alpine AS builder

RUN apk add --no-cache git
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X main.Version=v0.1.48" -o kptv-proxy .

# Final stage - ultra-small with just the binary and certs
FROM scratch

# Import certificates from alpine (for HTTPS)
COPY --from=docker.io/alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the static binary
COPY --from=builder /app/kptv-proxy /kptv-proxy

# Since we're using scratch, we can't create users (no useradd)
# But we can specify the user numerically
USER 1000:1000

EXPOSE 8080
CMD ["/kptv-proxy"]