
.PHONY: dev air vite build run tidy clean demo test

dev:
	@./red-dev.sh

air:
	DEV_MODE=true air -c .air.dev.toml

vite:
	npx vite

build:
	npx vite build
	go build -ldflags "-X github.com/RED-Collective/red-engine/internal/router.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)" -o ./red ./cmd/red

run: build
	./red

demo:
	@./scripts/two-node.sh

test:
	go test ./... -race -cover

tidy:
	go mod tidy

clean:
	rm -rf tmp internal/router/static/dist
