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

# Run the Tailwind build script to generate render.css
RUN npm run build 

# ----------------------------------------
# STAGE 2: Go Backend Builder
# ----------------------------------------
FROM golang:1.26-alpine AS backend-builder
WORKDIR /app

# Install Go dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the entire project context from your local computer
COPY . .

# *** THE FIX IS HERE ***
# Overwrite the blank local files with the freshly compiled Tailwind CSS and HTML 
# from Stage 1 BEFORE we run the Go compiler, so //go:embed grabs the styled files!
COPY --from=frontend-builder /app/internal/router/static/render.css ./internal/router/static/render.css
COPY --from=frontend-builder /app/internal/router/templates/ ./internal/router/templates/

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /red-engine ./cmd/red/main.go

# ----------------------------------------
# STAGE 3: Final Runtime Container
# ----------------------------------------
FROM alpine:3.19
WORKDIR /app

# Add tzdata for accurate log timestamps, and su-exec for privilege dropping
RUN apk --no-cache add ca-certificates git openssh tzdata su-exec

# Create the non-root user with a fixed UID/GID of 1000
RUN addgroup -g 1000 redgroup && \
    adduser -u 1000 -G redgroup -s /bin/sh -D reduser

# Copy ONLY the compiled Go binary (it now has the styles embedded inside it!)
COPY --from=backend-builder /red-engine ./red-engine

# Copy the new startup script and make it executable
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create the data directory explicitly before changing ownership
RUN mkdir -p /app/data
RUN chown -R reduser:redgroup /app

EXPOSE 8080
VOLUME ["/app/data"]

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD ["./red-engine", "-config", "/app/config.json"]