# Multi-stage build for AutoSSH Tunnel Manager (Pure Go SSH Engine)
ARG REGISTRY_MIRROR=docker.io
FROM ${REGISTRY_MIRROR}/library/golang:1.25.14-alpine AS builder

WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go ./
COPY internal/ ./internal/
COPY web/ ./web/

# Build static binary with embedded assets
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o autossh-server .

# Production runtime stage (Minimal lightweight Alpine)
FROM ${REGISTRY_MIRROR}/library/alpine:3.22.0 AS base

ARG VERSION=latest

# Install minimal runtime dependencies: ca-certificates, tzdata
RUN apk add --no-cache \
    ca-certificates \
    tzdata

WORKDIR /app

# Copy built server binary and web assets
COPY --from=builder /app/autossh-server /usr/local/bin/autossh-server
COPY web/static /app/web/static
COPY web/templates /app/web/templates

RUN chmod +x /usr/local/bin/autossh-server && \
    echo "$VERSION" > /etc/autossh-version

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/autossh-server"]
