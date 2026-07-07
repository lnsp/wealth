IMAGE  := ghcr.io/lnsp/wealth
TAG    := $(shell git rev-parse --short HEAD)

## ── Development ──────────────────────────────────────────

.PHONY: up down logs test lint

up:                    ## Start all services
	BUILD_COMMIT=$$(git rev-parse --short HEAD) BUILD_TIME=$$(date -u +%Y-%m-%dT%H:%M:%SZ) docker compose up -d --build

down:                  ## Stop all services
	docker compose down

logs:                  ## Tail service logs
	docker compose logs -f

test:                  ## Run Go and frontend tests
	go test ./...
	cd frontend && npm test

lint:                  ## Lint frontend
	cd frontend && npm run lint

## ── Database ─────────────────────────────────────────────

.PHONY: sqlc migrate

sqlc:                  ## Regenerate sqlc code
	sqlc generate

migrate:               ## Run pending migrations
	goose -dir migrations postgres "$$DATABASE_URL" up

## ── Container images ─────────────────────────────────────

.PHONY: build push

build:                 ## Build image for the current platform
	docker build --build-arg BUILD_COMMIT=$(TAG) --build-arg BUILD_TIME=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
		-t $(IMAGE):$(TAG) -t $(IMAGE):latest .

push:                  ## Build and push multi-arch image via docker buildx
	sudo docker buildx build --platform linux/amd64,linux/arm64 \
		--build-arg BUILD_COMMIT=$(TAG) --build-arg "BUILD_TIME=$$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
		-t $(IMAGE):$(TAG) -t $(IMAGE):latest --push .

## ── Helpers ──────────────────────────────────────────────

.PHONY: help
help:                  ## Show this help
	@grep -E '^[a-z][a-zA-Z_-]+:.*##' $(MAKEFILE_LIST) | awk -F ':.*## ' '{printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
