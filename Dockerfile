# Multi-stage build for jira-ai-issue-solver
# Build stage
FROM golang:1.23-alpine AS builder

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the application with optimizations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -a -installsuffix cgo \
    -o jira-ai-issue-solver \
    .

# Runtime stage
FROM alpine:3.19

# Install necessary packages for the runtime
RUN apk update && \
    apk add --no-cache \
    ca-certificates \
    curl \
    git \
    nodejs \
    npm \
    && rm -rf /var/cache/apk/*

# Install AI CLI tools
RUN npm install -g @google/gemini-cli @anthropic-ai/claude-code

# Create non-root user for security
RUN addgroup -g 1001 appgroup && \
    adduser -D -s /bin/sh -u 1001 -G appgroup appuser

# Set working directory
WORKDIR /app

# Copy the binary from builder stage
COPY --from=builder /app/jira-ai-issue-solver .

# Note: No configuration files are copied to avoid secrets in image
# Configuration should be provided via environment variables at runtime

# Create necessary directories
RUN mkdir -p /app/temp && \
    chown -R appuser:appgroup /app

# Switch to non-root user
USER appuser

# Expose the port the app runs on
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:8080/health || exit 1

# Set the entrypoint
ENTRYPOINT ["./jira-ai-issue-solver"]

# Default command (uses environment variables by default)
CMD ["./jira-ai-issue-solver"] 