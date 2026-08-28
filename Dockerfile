# Multi-stage build for AutoSSH Tunnel Manager (Pure Go SSH Engine)
ARG REGISTRY_MIRROR=docker.io
FROM ${REGISTRY_MIRROR}/library/golang:1.24-alpine AS builder

ARG GOPROXY
WORKDIR /app

# Cache dependencies
COPY go.mod go.sum ./
RUN if [ -n "$GOPROXY" ]; then export GOPROXY="$GOPROXY"; fi && \
    go mod download

# Copy source code
COPY main.go ./
COPY internal/ ./internal/

# Build static binary
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o autossh-server .

# Production runtime stage (Minimal lightweight Alpine)
FROM ${REGISTRY_MIRROR}/library/alpine:3.22.0 AS base

ARG VERSION=latest

# Install minimal runtime dependencies: su-exec, ca-certificates, tzdata
RUN apk add --no-cache \
    su-exec \
    ca-certificates \
    tzdata

# Create non-root user and group
RUN addgroup -g 1000 mygroup && \
    adduser -D -u 1000 -G mygroup myuser

# Setup directories
WORKDIR /app
RUN mkdir -p /etc/autossh/config /tmp/autossh-logs /home/myuser/.ssh && \
    chown -R myuser:mygroup /app /etc/autossh /tmp/autossh-logs /home/myuser

# Copy built server binary and web assets
COPY --from=builder /app/autossh-server /usr/local/bin/autossh-server
COPY web/static /app/web/static
COPY web/templates /app/web/templates
COPY entrypoint.sh /entrypoint.sh

RUN chmod +x /usr/local/bin/autossh-server \
    /entrypoint.sh

RUN echo "$VERSION" > /etc/autossh-version

EXPOSE 8080

ENTRYPOINT ["/entrypoint.sh"]
CMD ["/usr/local/bin/autossh-server"]
