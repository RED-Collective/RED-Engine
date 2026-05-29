# ----------------------------------------
# STAGE 1: Go Backend Builder
# ----------------------------------------
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app

# Install Git (needed for go mod download if any dependency needs it)
RUN apk add --no-cache git

# Install Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project
COPY . .

# Build the binary (CGO disabled for static binary)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /red-engine ./cmd/red/main.go

# ----------------------------------------
# STAGE 2: Final Runtime Container
# ----------------------------------------
FROM alpine:3.19
WORKDIR /app

# Install runtime dependencies (Git for cloning repos, ca-certificates for HTTPS)
RUN apk --no-cache add ca-certificates git openssh tzdata su-exec

# Create non-root user (UID 1000)
RUN addgroup -g 1000 redgroup && \
    adduser -u 1000 -G redgroup -s /bin/sh -D reduser

# Copy binary from builder
COPY --from=backend-builder /red-engine ./red-engine

# Copy entrypoint script (if you have one)
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create data directory and set ownership
RUN mkdir -p /app/data && chown -R reduser:redgroup /app

EXPOSE 8080
VOLUME ["/app/data"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./red-engine", "-config", "/app/config.json"]