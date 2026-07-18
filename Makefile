
.PHONY: dev air vite build build-frontend build-backend run tidy clean demo test

dev:
	@./red-dev.sh

air:
	DEV_MODE=true air -c .air.dev.toml

vite:
	cd internal/router/red-engine-frontend && npx vite

build-frontend:
	cd internal/router/red-engine-frontend && npm install && npm run build

build-backend:
	go build -ldflags "-X github.com/RED-Collective/red-engine/internal/router.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o ./red ./cmd/red

build: build-frontend build-backend

run: build
	./red

demo:
	@./scripts/two-node.sh

test:
	go test ./... -race -cover

tidy:
	go mod tidy

clean:
	rm -rf tmp internal/router/static/dist FRONTEND_BUILD
