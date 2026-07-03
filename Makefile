.PHONY: help setup dev dev-bg build up down deploy logs logs-dev logs-app \
        logs-db logs-redis shell psql psql-dev redis-cli redis-cli-dev test test-coverage clean backup \
        build-cli health check-env

## ─── Help ─────────────────────────────────────────────────────────────────────

help:
	@echo ""
	@echo "  Deployable — Developer & Operations Commands"
	@echo ""
	@echo "  DEVELOPMENT"
	@echo "  make setup        Copy .env.example → .env (first time)"
	@echo "  make dev          Start full dev stack (app + postgres + redis)"
	@echo "  make dev-bg       Start dev stack in background"
	@echo ""
	@echo "  PRODUCTION"
	@echo "  make build        Build production Docker image"
	@echo "  make up           Start production stack (detached)"
	@echo "  make deploy       Pull latest + rebuild app + restart (zero-downtime)"
	@echo "  make down         Stop all containers"
	@echo ""
	@echo "  LOGS & DEBUGGING"
	@echo "  make logs         Tail all container logs"
	@echo "  make logs-dev     Tail all dev container logs"
	@echo "  make logs-app     Tail app logs only"
	@echo "  make logs-db      Tail postgres logs only"
	@echo "  make logs-redis   Tail redis logs only"
	@echo "  make health       Check /health endpoint"
	@echo ""
	@echo "  DATABASE & CACHE"
	@echo "  make psql         Open psql shell in postgres container"
	@echo "  make psql-dev     Open psql shell in dev postgres container"
	@echo "  make redis-cli    Open redis-cli in redis container"
	@echo "  make redis-cli-dev Open redis-cli in dev redis container"
	@echo "  make backup       Backup postgres to ./backups/"
	@echo ""
	@echo "  SHELL"
	@echo "  make shell        Open sh in running dev app container"
	@echo ""
	@echo "  CLI"
	@echo "  make build-cli    Build CLI binaries for all platforms"
	@echo ""
	@echo "  TESTING"
	@echo "  make test         Run Go tests inside dev container"
	@echo "  make test-coverage Run Go tests with coverage report"
	@echo "  make clean        Remove all containers, volumes, images"
	@echo ""

## ─── Setup ────────────────────────────────────────────────────────────────────

setup:
	@if [ ! -f .env ]; then \
		cp .env.example .env; \
		echo "✅  .env created — fill in ANTHROPIC_API_KEY, SECRET_KEY, POSTGRES_PASSWORD"; \
		echo "    Also set DOMAIN and APP_URL when deploying to VPS"; \
	else \
		echo "⚠️   .env already exists — skipping"; \
	fi
	@mkdir -p backups data/uploads data/tmp

## ─── Development ──────────────────────────────────────────────────────────────

dev: check-env
	@echo "🚀  Starting Deployable dev stack..."
	@echo "    Postgres:  localhost:5432"
	@echo "    Redis:     localhost:6379"
	@echo "    App:       http://localhost:8080"
	docker compose -f docker-compose.dev.yml up --build

dev-bg: check-env
	docker compose -f docker-compose.dev.yml up --build -d
	@echo "✅  Dev stack running at http://localhost:8080"

## ─── Production ───────────────────────────────────────────────────────────────

build:
	@echo "🔨  Building production image..."
	docker compose build --no-cache app

up: check-env
	@echo "🚀  Starting Deployable production stack..."
	docker compose up -d
	@echo "✅  Stack running. Check: make health"

deploy:
	@echo "🚀  Deploying latest version..."
	git pull origin main
	docker compose build --no-cache app
	docker compose up -d --no-deps app
	@echo "✅  App restarted. Caddy continues serving during rebuild."
	@sleep 5
	@make health

down:
	docker compose down
	docker compose -f docker-compose.dev.yml down

## ─── Logs ─────────────────────────────────────────────────────────────────────

logs:
	docker compose logs -f

logs-dev:
	docker compose -f docker-compose.dev.yml logs -f

logs-app:
	docker compose logs -f app

logs-db:
	docker compose logs -f postgres

logs-redis:
	docker compose logs -f redis

## ─── Database & Cache ─────────────────────────────────────────────────────────

psql:
	@echo "📦  Opening psql (\\dt = list tables, \\q = quit)..."
	@source .env && docker compose exec postgres \
		psql -U $$POSTGRES_USER -d $$POSTGRES_DB

psql-dev:
	@source .env && docker compose -f docker-compose.dev.yml exec postgres \
		psql -U $$POSTGRES_USER -d $$POSTGRES_DB

redis-cli:
	docker compose exec redis redis-cli

redis-cli-dev:
	docker compose -f docker-compose.dev.yml exec redis redis-cli

backup:
	@mkdir -p backups
	@echo "💾  Backing up Postgres..."
	@source .env && docker compose exec -T postgres \
		pg_dump -U $$POSTGRES_USER $$POSTGRES_DB \
		| gzip > backups/deployable_$$(date +%Y%m%d_%H%M%S).sql.gz
	@echo "✅  Backup saved to backups/"
	@ls -lh backups/ | tail -5
	@find backups/ -name "*.sql.gz" -mtime +7 -delete
	@echo "🗑️   Old backups (>7 days) removed"

## ─── Shell ────────────────────────────────────────────────────────────────────

shell:
	docker compose -f docker-compose.dev.yml exec app sh

## ─── Health ───────────────────────────────────────────────────────────────────

health:
	@source .env 2>/dev/null; \
	URL=$${APP_URL:-http://localhost:8080}; \
	echo "🔍  Checking $$URL/health ..."; \
	curl -sf $$URL/health | python3 -m json.tool || echo "❌  Health check failed"

## ─── Testing ──────────────────────────────────────────────────────────────────

test:
	docker compose -f docker-compose.dev.yml run --rm app \
		go test ./... -v -count=1 -timeout 60s

test-coverage:
	docker compose -f docker-compose.dev.yml run --rm app \
		go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out -o coverage.html
	@echo "✅  Coverage report: coverage.html"

## ─── CLI Build ────────────────────────────────────────────────────────────────

build-cli:
	@echo "🔨  Building CLI binaries for all platforms..."
	@mkdir -p dist
	GOOS=linux   GOARCH=amd64  go build -ldflags="-w -s" -o dist/deployable-linux-amd64   ./cmd/deployable
	GOOS=linux   GOARCH=arm64  go build -ldflags="-w -s" -o dist/deployable-linux-arm64   ./cmd/deployable
	GOOS=darwin  GOARCH=amd64  go build -ldflags="-w -s" -o dist/deployable-darwin-amd64  ./cmd/deployable
	GOOS=darwin  GOARCH=arm64  go build -ldflags="-w -s" -o dist/deployable-darwin-arm64  ./cmd/deployable
	GOOS=windows GOARCH=amd64  go build -ldflags="-w -s" -o dist/deployable-windows-amd64.exe ./cmd/deployable
	@echo "✅  Binaries in dist/:"
	@ls -lh dist/

## ─── Cleanup ──────────────────────────────────────────────────────────────────

clean:
	@echo "🧹  Removing all containers, volumes, images..."
	docker compose down -v --remove-orphans
	docker compose -f docker-compose.dev.yml down -v --remove-orphans
	docker rmi deployable-app deployable_app 2>/dev/null || true
	rm -rf tmp/ dist/ data/tmp/*
	@echo "✅  Clean complete"

## ─── Guards ───────────────────────────────────────────────────────────────────

check-env:
	@if [ ! -f .env ]; then \
		echo "❌  .env not found. Run: make setup"; \
		exit 1; \
	fi
