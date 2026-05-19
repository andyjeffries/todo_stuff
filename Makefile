.PHONY: build run dev tidy clean css css-watch test screenshots bump-patch bump-minor bump-major help

# Auto-load .env (gitignored). Same KEY=value syntax as docker-compose's
# env_file. Plain values only — no `export`, no shell interpolation. Lets
# `make dev` / `make run` pick up local-only secrets without hard-coding.
ifneq (,$(wildcard .env))
    include .env
    export
endif

BINARY := bin/todostuff
PKG    := ./cmd/todostuff
PORT   ?= 8636

help:
	@echo "Targets:"
	@echo "  build        - Build $(BINARY)"
	@echo "  run          - Build and run the server (PORT=$(PORT))"
	@echo "  dev          - Run with \`go run\` (no binary)"
	@echo "  css          - Build Tailwind CSS once"
	@echo "  css-watch    - Build Tailwind CSS in watch mode"
	@echo "  tidy         - go mod tidy"
	@echo "  test         - go test ./..."
	@echo "  screenshots  - Regenerate docs/screenshots/ from a seeded instance"
	@echo "  bump-patch   - Tag and push vX.Y.Z+1"
	@echo "  bump-minor   - Tag and push vX.Y+1.0"
	@echo "  bump-major   - Tag and push vX+1.0.0"
	@echo "  clean        - Remove build artefacts"

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

screenshots:
	./scripts/screenshots.sh

_bump-check:
	@git diff --quiet && git diff --cached --quiet || { echo "Uncommitted changes — commit first"; exit 1; }

bump-patch: _bump-check
	@LATEST=$$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1); \
	LATEST=$${LATEST:-v0.0.0}; \
	MAJOR=$$(echo $$LATEST | cut -d. -f1 | tr -d v); \
	MINOR=$$(echo $$LATEST | cut -d. -f2); \
	PATCH=$$(echo $$LATEST | cut -d. -f3); \
	NEW="v$$MAJOR.$$MINOR.$$((PATCH+1))"; \
	echo "$$LATEST → $$NEW"; \
	git tag $$NEW && git push origin $$NEW && echo "GitHub Actions will build and publish to GHCR."

bump-minor: _bump-check
	@LATEST=$$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1); \
	LATEST=$${LATEST:-v0.0.0}; \
	MAJOR=$$(echo $$LATEST | cut -d. -f1 | tr -d v); \
	MINOR=$$(echo $$LATEST | cut -d. -f2); \
	NEW="v$$MAJOR.$$((MINOR+1)).0"; \
	echo "$$LATEST → $$NEW"; \
	git tag $$NEW && git push origin $$NEW && echo "GitHub Actions will build and publish to GHCR."

bump-major: _bump-check
	@LATEST=$$(git tag --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -1); \
	LATEST=$${LATEST:-v0.0.0}; \
	MAJOR=$$(echo $$LATEST | cut -d. -f1 | tr -d v); \
	NEW="v$$((MAJOR+1)).0.0"; \
	echo "$$LATEST → $$NEW"; \
	git tag $$NEW && git push origin $$NEW && echo "GitHub Actions will build and publish to GHCR."

clean:
	rm -rf bin web/static/css/app.css
