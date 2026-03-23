# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -ldflags="-w -s" \
    -o jira-ai-issue-solver .

# Runtime stage
FROM alpine:3.21

# podman-remote-static: client-only binary that talks to the host's
# podman via CONTAINER_HOST socket. Avoids the full podman package
# which requires newuidmap and tries to initialize local rootless
# storage even in remote mode.
ARG PODMAN_VERSION=5.5.1
ARG TARGETARCH
RUN wget -qO- "https://github.com/containers/podman/releases/download/v${PODMAN_VERSION}/podman-remote-static-linux_${TARGETARCH}.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=1 "bin/podman-remote-static-linux_${TARGETARCH}" \
    && mv "/usr/local/bin/podman-remote-static-linux_${TARGETARCH}" /usr/local/bin/podman \
    && chmod +x /usr/local/bin/podman

RUN apk add --no-cache \
    ca-certificates \
    git \
    openssh-client

RUN addgroup -g 1001 appgroup && \
    adduser -u 1001 -G appgroup -s /bin/sh -D appuser

WORKDIR /app
COPY --from=builder /app/jira-ai-issue-solver .
RUN mkdir -p /app/temp /var/lib/ai-bot/workspaces && \
    chown -R appuser:appgroup /app /var/lib/ai-bot

USER appuser
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD ["wget", "-qO/dev/null", "http://localhost:8080/health"]

ENTRYPOINT ["./jira-ai-issue-solver"]
