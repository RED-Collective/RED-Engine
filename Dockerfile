# ----------------------------------------
# STAGE 1: Node.js / Tailwind CSS Builder
# ----------------------------------------
FROM node:20-alpine AS frontend-builder
WORKDIR /app/internal/router

# Install frontend dependencies
COPY internal/router/package*.json ./
RUN npm install

# Copy templates and static source files for Tailwind to parse
COPY internal/router/templates/ ./templates/
COPY internal/router/static/ ./static/

# Run the Tailwind build script
RUN npm run build 

# ----------------------------------------
# STAGE 2: Go Backend Builder
# ----------------------------------------
# Retaining your specific Go version
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app

# Install Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project context
COPY . .

# Build the binary retaining your original build flags
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /red-engine ./cmd/red/main.go

# ----------------------------------------
# STAGE 3: Final Runtime Container
# ----------------------------------------
# Pin to your specific Alpine version rather than 'latest'
FROM alpine:3.19
WORKDIR /app

# Add tzdata for accurate log timestamps, and su-exec for privilege dropping
RUN apk --no-cache add ca-certificates git openssh tzdata su-exec

# Create the non-root user with a fixed UID/GID of 1000
RUN addgroup -g 1000 redgroup && \
    adduser -u 1000 -G redgroup -s /bin/sh -D reduser

# Copy the compiled Go binary
COPY --from=backend-builder /red-engine ./red-engine

# Copy frontend assets (CSS/HTML) from the Node builder
COPY --from=frontend-builder /app/internal/router/templates/ ./internal/router/templates/
COPY --from=frontend-builder /app/internal/router/static/ ./internal/router/static/

# Copy the new startup script and make it executable
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create the data directory explicitly before changing ownership
RUN mkdir -p /app/data

# Ensure the user owns the application directory initially
RUN chown -R reduser:redgroup /app

# The container must start as root to execute entrypoint.sh, 
# which will dynamically fix the Podman volume permissions and THEN drop to reduser.
EXPOSE 8080
VOLUME ["/app/data"]

# Route the startup through the self-healing script
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./red-engine", "-config", "/app/config.json"]