.PHONY: build run dev tidy clean css css-watch test help

# Auto-load .env (gitignored). Same KEY=value syntax as docker-compose's
# env_file. Plain values only — no `export`, no shell interpolation. Lets
# `make dev` / `make run` pick up local-only secrets without hard-coding.
ifneq (,$(wildcard .env))
    include .env
    export
endif

BINARY := bin/todostuff
PKG    := ./cmd/todostuff
PORT   ?= 8080

help:
	@echo "Targets:"
	@echo "  build      - Build $(BINARY)"
	@echo "  run        - Build and run the server (PORT=$(PORT))"
	@echo "  dev        - Run with `go run` (no binary)"
	@echo "  css        - Build Tailwind CSS once"
	@echo "  css-watch  - Build Tailwind CSS in watch mode"
	@echo "  tidy       - go mod tidy"
	@echo "  test       - go test ./..."
	@echo "  clean      - Remove build artefacts"

build: css
	@mkdir -p bin
	go build -o $(BINARY) $(PKG)

run: build
	PORT=$(PORT) ./$(BINARY)

dev: css
	PORT=$(PORT) go run $(PKG)

css:
	@if [ ! -d node_modules ]; then npm install; fi
	npx @tailwindcss/cli -i web/static/css/input.css -o web/static/css/app.css --minify

css-watch:
	@if [ ! -d node_modules ]; then npm install; fi
	npx @tailwindcss/cli -i web/static/css/input.css -o web/static/css/app.css --watch

tidy:
	go mod tidy

test:
	go test ./...

clean:
	rm -rf bin web/static/css/app.css
