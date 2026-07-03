# CLAUDE.md — Deployable
> "From vibe to production. Know before you ship."

---

## What This File Is

This is the complete agent instruction file for Deployable. Read ARCHITECTURE.md first, then read every section of this file before writing a single line of code. This file takes the project from an empty directory to a fully working, containerized, VPS-deployable deployment readiness platform. Follow the build phases strictly. Do not implement anything outside the current phase scope.

---

## Project Summary

Deployable is a DevOps tooling platform that accepts a codebase via three input methods (zip upload, GitHub URL paste, CLI), analyzes it using deterministic file inspection + Claude API semantic analysis, and produces a Deployment Readiness Report with security findings, resource estimates, platform recommendations, and generated deployment files.

**Stack:** Go 1.22 + HTMX 2.x + Tailwind CSS CDN + PostgreSQL 16 + Redis 7 + Caddy + Docker Compose + Makefile

**Deployment target:** Single VPS (Ubuntu 24 LTS), all services as Docker containers.

---

## Complete Project Structure

Create every file and directory. Empty placeholder files are fine — they will be filled per phase.

```
deployable/
├── CLAUDE.md
├── ARCHITECTURE.md
├── Makefile
├── Caddyfile
├── Dockerfile
├── Dockerfile.dev
├── docker-compose.yml
├── docker-compose.dev.yml
├── .env.example
├── .env                           # gitignored
├── .air.toml
├── .gitignore
├── .dockerignore
├── go.mod
├── go.sum
│
├── cmd/
│   ├── server/
│   │   └── main.go               # Web server entry point
│   └── deployable/
│       └── main.go               # CLI binary entry point
│
├── migrations/
│   ├── 000001_create_users.up.sql
│   ├── 000001_create_users.down.sql
│   ├── 000002_create_sessions.up.sql
│   ├── 000002_create_sessions.down.sql
│   ├── 000003_create_analysis_jobs.up.sql
│   ├── 000003_create_analysis_jobs.down.sql
│   ├── 000004_create_reports.up.sql
│   ├── 000004_create_reports.down.sql
│   └── 000005_create_rate_events.up.sql
│   └── 000005_create_rate_events.down.sql
│
├── internal/
│   ├── analyzer/
│   │   ├── walker.go             # File tree walking, manifest building
│   │   ├── detector.go           # Stack/framework/DB detection
│   │   ├── secrets.go            # Regex-based secret scanning
│   │   ├── env.go                # Env var extraction from source files
│   │   ├── infra.go              # Infrastructure file checks
│   │   └── pipeline.go           # Orchestrates all analyzer steps
│   │
│   ├── claude/
│   │   └── client.go             # Claude API HTTP client + prompt builder
│   │
│   ├── github/
│   │   └── client.go             # GitHub API client (fetch repo, download zip)
│   │
│   ├── generator/
│   │   ├── dockerfile.go         # Dockerfile template generation
│   │   ├── compose.go            # docker-compose.yml generation
│   │   ├── env_example.go        # .env.example generation
│   │   ├── ci.go                 # GitHub Actions workflow generation
│   │   ├── platform.go           # fly.toml / render.yaml generation
│   │   └── deployment_guide.go   # DEPLOYMENT.md generation
│   │
│   ├── renderer/
│   │   └── terminal.go           # CLI colored terminal output (lipgloss)
│   │
│   └── client/
│       └── api.go                # CLI → API HTTP client
│
├── db/
│   └── db.go                     # Postgres pool + migration runner
│
├── cache/
│   └── redis.go                  # Redis client + helper methods
│
├── models/
│   ├── user.go
│   ├── session.go
│   ├── job.go
│   └── report.go
│
├── handlers/
│   ├── auth.go                   # Register, login, logout, GitHub OAuth
│   ├── analyze.go                # All three input methods + status polling
│   ├── report.go                 # Report view + download
│   ├── dashboard.go              # User dashboard (saved reports)
│   └── api.go                    # REST API for CLI (/api/v1/...)
│
├── middleware/
│   ├── auth.go                   # Session validation
│   ├── ratelimit.go              # Redis-backed rate limiting
│   └── logger.go
│
├── templates/
│   ├── base.html
│   ├── landing.html
│   ├── 404.html
│   ├── error.html
│   ├── auth/
│   │   ├── login.html
│   │   └── register.html
│   ├── analyze/
│   │   ├── index.html            # Upload/URL/CLI input page
│   │   ├── processing.html       # Progress page (HTMX polling)
│   │   └── status.html           # HTMX partial: progress steps
│   ├── report/
│   │   ├── index.html            # Full report page
│   │   ├── score_badge.html      # HTMX partial: readiness score
│   │   ├── security.html         # HTMX partial: security findings
│   │   ├── resources.html        # HTMX partial: resource estimates
│   │   ├── platforms.html        # HTMX partial: platform recommendations
│   │   └── files.html            # HTMX partial: generated files preview
│   └── dashboard/
│       └── index.html            # Saved reports list
│
├── static/                       # Embedded via go:embed
│   ├── favicon.ico
│   └── css/
│       └── app.css               # Minimal custom CSS (Tailwind handles most)
│
└── scripts/
    ├── install.sh                # CLI install script (curl | bash)
    └── backup.sh                 # Postgres backup script
```

---

## Docker Setup

### `Dockerfile` (production — multi-stage)

```dockerfile
# Stage 1: Build both binaries
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build web server
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -X main.version=$(git describe --tags --always)" \
    -o /out/server ./cmd/server

# Build CLI binary (linux/amd64 — for container use)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /out/deployable ./cmd/deployable

# Stage 2: Minimal runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /out/server .
COPY --from=builder /out/deployable /usr/local/bin/deployable
COPY --from=builder /app/templates ./templates
COPY --from=builder /app/static ./static
COPY --from=builder /app/migrations ./migrations

RUN mkdir -p /app/data/uploads /app/data/tmp

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

CMD ["./server"]
```

### `Dockerfile.dev`

```dockerfile
FROM golang:1.22-alpine

RUN apk add --no-cache git ca-certificates curl

RUN go install github.com/cosmtrek/air@latest

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

EXPOSE 8080
CMD ["air", "-c", ".air.toml"]
```

### `docker-compose.yml` (production)

```yaml
version: "3.9"

services:
  caddy:
    image: caddy:2-alpine
    container_name: deployable_caddy
    restart: unless-stopped
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - caddy_data:/data
      - caddy_config:/config
      - /var/log/caddy:/var/log/caddy
    networks:
      - deployable_network
    depends_on:
      - app

  app:
    build:
      context: .
      dockerfile: Dockerfile
    image: deployable_app:latest
    container_name: deployable_app
    restart: unless-stopped
    env_file: .env
    volumes:
      - deployable_uploads:/app/data/uploads
      - deployable_tmp:/app/data/tmp
    networks:
      - deployable_network
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 15s

  postgres:
    image: postgres:16-alpine
    container_name: deployable_postgres
    restart: unless-stopped
    environment:
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - deployable_pgdata:/var/lib/postgresql/data
    networks:
      - deployable_network
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: deployable_redis
    restart: unless-stopped
    command: redis-server --appendonly yes --maxmemory 256mb --maxmemory-policy allkeys-lru
    volumes:
      - deployable_redis:/data
    networks:
      - deployable_network
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

networks:
  deployable_network:
    driver: bridge

volumes:
  deployable_pgdata:
  deployable_redis:
  deployable_uploads:
  deployable_tmp:
  caddy_data:
  caddy_config:
```

### `docker-compose.dev.yml` (development)

```yaml
version: "3.9"

services:
  app:
    build:
      context: .
      dockerfile: Dockerfile.dev
    container_name: deployable_app_dev
    ports:
      - "8080:8080"
    env_file: .env
    volumes:
      - .:/app
      - deployable_uploads_dev:/app/data/uploads
      - go_mod_cache:/go/pkg/mod
    networks:
      - deployable_network
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy

  postgres:
    image: postgres:16-alpine
    container_name: deployable_postgres_dev
    restart: unless-stopped
    ports:
      - "5432:5432"
    environment:
      POSTGRES_DB: ${POSTGRES_DB}
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - deployable_pgdata_dev:/var/lib/postgresql/data
    networks:
      - deployable_network
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    container_name: deployable_redis_dev
    restart: unless-stopped
    ports:
      - "6379:6379"
    command: redis-server --appendonly yes
    volumes:
      - deployable_redis_dev:/data
    networks:
      - deployable_network
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

networks:
  deployable_network:
    driver: bridge

volumes:
  deployable_pgdata_dev:
  deployable_redis_dev:
  deployable_uploads_dev:
  go_mod_cache:
```

### `.air.toml`

```toml
root = "."
tmp_dir = "tmp"

[build]
  cmd = "go build -o ./tmp/server ./cmd/server"
  bin = "./tmp/server"
  include_ext = ["go", "html", "css", "sql"]
  exclude_dir = ["tmp", "data", "static", "migrations", "cmd/deployable"]
  delay = 500

[log]
  time = true

[color]
  main = "cyan"
  watcher = "blue"
  build = "yellow"
  runner = "green"

[misc]
  clean_on_exit = true
```

---

## Makefile

```makefile
.PHONY: help setup dev dev-bg build up down deploy logs logs-app \
        logs-db logs-redis shell psql redis-cli test clean backup \
        build-cli release health

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
	@echo "  make logs-app     Tail app logs only"
	@echo "  make logs-db      Tail postgres logs only"
	@echo "  make logs-redis   Tail redis logs only"
	@echo "  make health       Check /health endpoint"
	@echo ""
	@echo "  DATABASE & CACHE"
	@echo "  make psql         Open psql shell in postgres container"
	@echo "  make redis-cli    Open redis-cli in redis container"
	@echo "  make backup       Backup postgres to ./backups/"
	@echo ""
	@echo "  SHELL"
	@echo "  make shell        Open sh in running dev app container"
	@echo ""
	@echo "  CLI"
	@echo "  make build-cli    Build CLI binaries for all platforms"
	@echo "  make release      Tag + push + trigger GitHub Actions release"
	@echo ""
	@echo "  TESTING"
	@echo "  make test         Run Go tests inside dev container"
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
```

---

## Environment Variables

### `.env.example`

```env
# ── Server ────────────────────────────────────────────────────────────────────
PORT=8080
APP_URL=http://localhost:8080
DOMAIN=localhost                    # your VPS domain e.g. deployable.yourdomain.com
APP_ENV=development                 # development | production

# ── Security ──────────────────────────────────────────────────────────────────
# Generate: openssl rand -hex 32
SECRET_KEY=replace-with-64-char-hex-string
# Generate: openssl rand -hex 32 (for encrypting GitHub OAuth tokens at rest)
ENCRYPTION_KEY=replace-with-64-char-hex-string

# ── Database ──────────────────────────────────────────────────────────────────
POSTGRES_HOST=postgres
POSTGRES_PORT=5432
POSTGRES_DB=deployable
POSTGRES_USER=deployable
POSTGRES_PASSWORD=replace-with-strong-password
DATABASE_URL=postgres://deployable:replace-with-strong-password@postgres:5432/deployable?sslmode=disable

# ── Redis ─────────────────────────────────────────────────────────────────────
REDIS_URL=redis://redis:6379/0

# ── Claude API ────────────────────────────────────────────────────────────────
ANTHROPIC_API_KEY=sk-ant-...
ANTHROPIC_MODEL=claude-sonnet-4-6

# ── GitHub OAuth (for private repo access) ────────────────────────────────────
# Create at: https://github.com/settings/applications/new
# Callback URL: https://yourdomain.com/auth/github/callback
GITHUB_CLIENT_ID=
GITHUB_CLIENT_SECRET=

# ── File Handling ─────────────────────────────────────────────────────────────
UPLOADS_DIR=/app/data/uploads
TMP_DIR=/app/data/tmp
MAX_UPLOAD_BYTES=52428800           # 50MB

# ── Rate Limiting ─────────────────────────────────────────────────────────────
RATE_LIMIT_ANON=5                   # analyses per hour (anonymous)
RATE_LIMIT_FREE=20                  # analyses per hour (logged in free)
RATE_LIMIT_PRO=100                  # analyses per hour (pro)

# ── Analysis ──────────────────────────────────────────────────────────────────
MAX_CONCURRENT_ANALYSES=10          # semaphore limit
ANALYSIS_TIMEOUT_SECONDS=180        # max time per analysis
CACHE_TTL_HOURS=24                  # report cache TTL
ANON_REPORT_TTL_DAYS=7             # anonymous reports expire after N days

# ── CLI ───────────────────────────────────────────────────────────────────────
CLI_API_BASE_URL=https://yourdomain.com/api/v1
```

### `.gitignore`

```gitignore
.env
data/
tmp/
dist/
backups/
coverage.out
coverage.html
*.db
__debug_bin
```

### `.dockerignore`

```dockerignore
.env
data/
tmp/
dist/
backups/
.git/
.air.toml
docker-compose*.yml
Makefile
*.md
coverage.*
```

---

## `go.mod`

```
module deployable

go 1.22

require (
    github.com/go-chi/chi/v5 v5.0.12
    github.com/golang-migrate/migrate/v4 v4.17.1
    github.com/google/uuid v1.6.0
    github.com/jackc/pgx/v5 v5.6.0
    github.com/joho/godotenv v1.5.1
    github.com/redis/go-redis/v9 v9.5.1
    github.com/charmbracelet/lipgloss v0.11.0
    github.com/spf13/cobra v1.8.0
    golang.org/x/crypto v0.23.0
    golang.org/x/oauth2 v0.20.0
)
```

---

## Migration Files (Complete SQL)

### `000001_create_users.up.sql`
```sql
CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT UNIQUE,
    name          TEXT,
    password_hash TEXT,
    github_id     TEXT UNIQUE,
    github_login  TEXT,
    github_token  TEXT,
    api_key_hash  TEXT UNIQUE,
    plan          TEXT NOT NULL DEFAULT 'free',
    analyses_count INTEGER NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_users_email ON users(email);
CREATE INDEX idx_users_github_id ON users(github_id);
CREATE INDEX idx_users_api_key_hash ON users(api_key_hash);
```

### `000002_create_sessions.up.sql`
```sql
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
```

### `000003_create_analysis_jobs.up.sql`
```sql
CREATE TYPE job_input_type AS ENUM ('zip', 'github', 'cli');
CREATE TYPE job_status AS ENUM ('pending', 'running', 'complete', 'failed');

CREATE TABLE analysis_jobs (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
    input_type    job_input_type NOT NULL,
    input_ref     TEXT,
    status        job_status NOT NULL DEFAULT 'pending',
    current_step  INTEGER NOT NULL DEFAULT 0,
    total_steps   INTEGER NOT NULL DEFAULT 6,
    step_message  TEXT,
    error_msg     TEXT,
    ip_address    TEXT,
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_jobs_user_id ON analysis_jobs(user_id);
CREATE INDEX idx_jobs_status ON analysis_jobs(status);
CREATE INDEX idx_jobs_created_at ON analysis_jobs(created_at);
```

### `000004_create_reports.up.sql`
```sql
CREATE TABLE reports (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id           UUID NOT NULL REFERENCES analysis_jobs(id) ON DELETE CASCADE,
    user_id          UUID REFERENCES users(id) ON DELETE SET NULL,
    slug             TEXT UNIQUE NOT NULL,
    is_public        BOOLEAN NOT NULL DEFAULT TRUE,

    -- Detected stack
    language         TEXT,
    language_version TEXT,
    framework        TEXT,
    databases        TEXT[],
    services         TEXT[],

    -- Scores (0-100)
    readiness_score  INTEGER NOT NULL DEFAULT 0,
    complexity_score INTEGER NOT NULL DEFAULT 0,
    security_score   INTEGER NOT NULL DEFAULT 0,

    -- Full analysis stored as JSONB
    deterministic_findings  JSONB NOT NULL DEFAULT '{}',
    semantic_analysis       JSONB NOT NULL DEFAULT '{}',

    -- Resource estimates
    min_ram_mb       INTEGER,
    rec_ram_mb       INTEGER,
    min_cpu          NUMERIC(3,1),
    storage_gb       INTEGER,
    est_rps          INTEGER,
    resource_reasoning TEXT,

    -- Platform recommendations (JSONB array of PlatformRec objects)
    platforms        JSONB NOT NULL DEFAULT '[]',

    -- Generated deployment files (JSONB map: filename → content)
    generated_files  JSONB NOT NULL DEFAULT '{}',

    -- Deduplication
    content_hash     TEXT,

    -- Expiry (NULL = permanent for logged-in users)
    expires_at       TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_reports_slug ON reports(slug);
CREATE INDEX idx_reports_user_id ON reports(user_id);
CREATE INDEX idx_reports_content_hash ON reports(content_hash);
CREATE INDEX idx_reports_created_at ON reports(created_at);
```

### `000005_create_rate_events.up.sql`
```sql
CREATE TABLE rate_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identifier TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT 'analysis',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_rate_events_identifier ON rate_events(identifier, created_at);
```

All `.down.sql` files: `DROP TABLE IF EXISTS {table_name}; DROP TYPE IF EXISTS {type_name};`

---

## `cmd/server/main.go` — Full Implementation

```go
package main

import (
    "context"
    "embed"
    "html/template"
    "log"
    "net/http"
    "os"
    "os/signal"
    "strings"
    "syscall"
    "time"

    "github.com/go-chi/chi/v5"
    chimiddleware "github.com/go-chi/chi/v5/middleware"
    "github.com/joho/godotenv"

    "deployable/cache"
    "deployable/db"
    "deployable/handlers"
    "deployable/middleware"
)

//go:embed templates static
var embeddedFiles embed.FS

var version = "dev"

func main() {
    _ = godotenv.Load()

    // Validate required env vars — fail loudly
    required := []string{
        "DATABASE_URL", "REDIS_URL", "SECRET_KEY",
        "ENCRYPTION_KEY", "ANTHROPIC_API_KEY",
    }
    for _, key := range required {
        if strings.TrimSpace(os.Getenv(key)) == "" {
            log.Fatalf("FATAL: required env var %s is not set", key)
        }
    }

    ctx := context.Background()

    // Connect Postgres + run migrations
    pool, err := db.Connect(ctx)
    if err != nil {
        log.Fatalf("Database setup failed: %v", err)
    }
    defer pool.Close()

    // Connect Redis
    rdb, err := cache.Connect(os.Getenv("REDIS_URL"))
    if err != nil {
        log.Fatalf("Redis setup failed: %v", err)
    }
    defer rdb.Close()

    // Ensure data directories
    for _, dir := range []string{
        os.Getenv("UPLOADS_DIR"),
        os.Getenv("TMP_DIR"),
    } {
        if dir != "" {
            if err := os.MkdirAll(dir, 0755); err != nil {
                log.Fatalf("Failed to create dir %s: %v", dir, err)
            }
        }
    }

    // Parse templates
    tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(
        embeddedFiles, "templates/**/*.html", "templates/*.html",
    )
    if err != nil {
        log.Fatalf("Template parse failed: %v", err)
    }

    // Init handlers
    deps := handlers.Deps{
        Pool:    pool,
        Redis:   rdb,
        Tmpl:    tmpl,
        Version: version,
    }

    // Router
    r := chi.NewRouter()
    r.Use(chimiddleware.RealIP)
    r.Use(chimiddleware.Logger)
    r.Use(chimiddleware.Recoverer)
    r.Use(chimiddleware.Timeout(180 * time.Second))
    r.Use(chimiddleware.StripSlashes)

    // Static files (embedded)
    r.Handle("/static/*", http.FileServer(http.FS(embeddedFiles)))

    // Health check
    r.Get("/health", handlers.HealthHandler(pool, rdb, version))

    // Public routes
    r.Get("/", handlers.LandingHandler(deps))
    r.Get("/login", handlers.LoginPage(deps))
    r.Post("/login", handlers.Login(deps))
    r.Get("/register", handlers.RegisterPage(deps))
    r.Post("/register", handlers.Register(deps))
    r.Post("/logout", handlers.Logout(deps))

    // GitHub OAuth
    r.Get("/auth/github", handlers.GitHubOAuthStart(deps))
    r.Get("/auth/github/callback", handlers.GitHubOAuthCallback(deps))

    // Public report view
    r.Get("/report/{slug}", handlers.ReportView(deps))
    r.Get("/report/{slug}/download", handlers.ReportDownload(deps))

    // Analysis (rate limited)
    r.Group(func(r chi.Router) {
        r.Use(middleware.RateLimit(rdb))
        r.Get("/analyze", handlers.AnalyzePage(deps))
        r.Post("/analyze/zip", handlers.AnalyzeZip(deps))
        r.Post("/analyze/github", handlers.AnalyzeGitHub(deps))
        r.Get("/analyze/{jobID}/status", handlers.AnalyzeStatus(deps))
        r.Get("/analyze/{jobID}/processing", handlers.ProcessingPage(deps))
    })

    // Protected routes
    r.Group(func(r chi.Router) {
        r.Use(middleware.RequireAuth(pool, rdb))
        r.Get("/dashboard", handlers.Dashboard(deps))
        r.Delete("/report/{slug}", handlers.DeleteReport(deps))
    })

    // REST API for CLI
    r.Route("/api/v1", func(r chi.Router) {
        r.Use(middleware.RequireAPIKey(pool, rdb))
        r.Use(middleware.RateLimit(rdb))
        r.Post("/analyze", handlers.APIAnalyze(deps))
        r.Get("/analyze/{jobID}", handlers.APIAnalyzeStatus(deps))
        r.Get("/report/{slug}", handlers.APIReport(deps))
    })

    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    srv := &http.Server{
        Addr:         ":" + port,
        Handler:      r,
        ReadTimeout:  30 * time.Second,
        WriteTimeout: 200 * time.Second,
        IdleTimeout:  120 * time.Second,
    }

    go func() {
        log.Printf("Deployable v%s running on :%s", version, port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }()

    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Println("Shutting down gracefully...")
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    srv.Shutdown(shutdownCtx)
    log.Println("Shutdown complete")
}

func templateFuncs() template.FuncMap {
    return template.FuncMap{
        "lower":       strings.ToLower,
        "upper":       strings.ToUpper,
        "join":        strings.Join,
        "add":         func(a, b int) int { return a + b },
        "sub":         func(a, b int) int { return a - b },
        "mul":         func(a, b int) int { return a * b },
        "scoreColor": func(score int) string {
            switch {
            case score >= 80:
                return "text-green-400"
            case score >= 60:
                return "text-yellow-400"
            case score >= 40:
                return "text-orange-400"
            default:
                return "text-red-400"
            }
        },
        "severityColor": func(sev string) string {
            switch sev {
            case "critical":
                return "bg-red-900 text-red-200 border-red-700"
            case "high":
                return "bg-orange-900 text-orange-200 border-orange-700"
            case "medium":
                return "bg-yellow-900 text-yellow-200 border-yellow-700"
            default:
                return "bg-gray-800 text-gray-300 border-gray-600"
            }
        },
        "timeAgo": timeAgo,
        "safeURL": func(s string) template.URL { return template.URL(s) },
    }
}
```

---

## Analysis Pipeline — Implementation Detail

### `internal/analyzer/pipeline.go`

```go
package analyzer

import (
    "context"
    "fmt"
    "path/filepath"
)

// Result is the complete output of the analysis pipeline.
type Result struct {
    // Layer 1 — deterministic
    Manifest       FileManifest
    StackInfo      StackInfo
    InfraChecks    InfraChecks
    EnvVars        []string
    SecretFindings []SecretFinding
    ContentHash    string

    // Layer 2 — semantic (Claude)
    Semantic       *SemanticReport

    // Layer 3 — generated files
    GeneratedFiles map[string]string // filename → content
}

// Run executes the full analysis pipeline on a directory.
// dir: path to extracted/cloned project root
// claudeClient: nil means skip semantic analysis (offline mode)
func Run(ctx context.Context, dir string, claudeClient ClaudeClient) (*Result, error) {
    result := &Result{}

    // Step 1: Walk files, build manifest
    manifest, err := WalkFiles(dir)
    if err != nil {
        return nil, fmt.Errorf("file walk failed: %w", err)
    }
    result.Manifest = manifest
    result.ContentHash = manifest.ContentHash()

    // Step 2: Stack detection
    result.StackInfo = DetectStack(manifest)

    // Step 3: Infrastructure checks
    result.InfraChecks = CheckInfra(manifest, dir)

    // Step 4: Env var extraction
    result.EnvVars = ExtractEnvVars(manifest, dir)

    // Step 5: Secret scanning
    result.SecretFindings = ScanSecrets(manifest, dir)

    // Step 6: Claude semantic analysis
    if claudeClient != nil {
        context := BuildClaudeContext(result)
        semantic, err := claudeClient.Analyze(ctx, context)
        if err != nil {
            // Semantic failure is non-fatal — return partial result
            result.Semantic = &SemanticReport{
                ReadinessSummary: "Semantic analysis unavailable: " + err.Error(),
                ReadinessScore:   estimateScoreFromDeterministic(result),
            }
        } else {
            result.Semantic = semantic
        }
    }

    // Step 7: Generate deployment files
    result.GeneratedFiles = GenerateFiles(result)

    return result, nil
}
```

### `internal/analyzer/detector.go` — Stack Detection

Detect language by checking for these files in order of confidence:

```go
type StackInfo struct {
    Language        string   // "Go", "Node.js", "Python", "Ruby", "Rust", "Java", "PHP"
    LanguageVersion string   // "1.22", "20", "3.11", etc.
    Framework       string   // "Chi", "Express", "FastAPI", "Django", "Rails", etc.
    Databases       []string // ["PostgreSQL", "Redis", "MongoDB"]
    HasDocker       bool
    HasDockerCompose bool
    HasGitignore    bool
    HasEnvExample   bool
    HasCIConfig     bool
    HasHealthCheck  bool
    HasLockFile     bool
    HasTests        bool
    AppPort         string   // detected port
    EntryPoint      string   // main.go, index.js, app.py, etc.
}

// Detection priority (first match wins):
// go.mod          → Go
// package.json    → Node.js (check for next, express, fastify in deps)
// requirements.txt / pyproject.toml → Python (check for fastapi, django, flask)
// Gemfile         → Ruby (check for rails)
// Cargo.toml      → Rust (check for actix-web, axum)
// pom.xml         → Java (check for spring)
// composer.json   → PHP (check for laravel)
// *.csproj        → C#/.NET

// Database detection (scan imports AND docker-compose services):
// "postgres", "pgx", "psycopg", "pg" → PostgreSQL
// "redis", "go-redis", "ioredis"     → Redis
// "mongo", "mongoose", "pymongo"     → MongoDB
// "mysql", "mariadb"                 → MySQL
// "sqlite3"                          → SQLite (flag: not for production)
```

### `internal/analyzer/secrets.go` — Secret Scanning

```go
// Secret patterns to scan for in all non-binary files:
var secretPatterns = []SecretPattern{
    {Name: "Anthropic API Key",    Pattern: `sk-ant-[a-zA-Z0-9\-_]{40,}`,        Severity: "critical"},
    {Name: "OpenAI API Key",       Pattern: `sk-[a-zA-Z0-9]{48}`,                 Severity: "critical"},
    {Name: "GitHub Token",         Pattern: `ghp_[a-zA-Z0-9]{36}`,               Severity: "critical"},
    {Name: "GitHub OAuth",         Pattern: `gho_[a-zA-Z0-9]{36}`,               Severity: "critical"},
    {Name: "AWS Access Key",       Pattern: `AKIA[0-9A-Z]{16}`,                   Severity: "critical"},
    {Name: "Postgres URL",         Pattern: `postgres://[^:]+:[^@]+@`,            Severity: "high"},
    {Name: "MySQL URL",            Pattern: `mysql://[^:]+:[^@]+@`,               Severity: "high"},
    {Name: "Redis URL with pass",  Pattern: `redis://:[^@]+@`,                    Severity: "high"},
    {Name: "Private Key",          Pattern: `-----BEGIN (RSA |EC )?PRIVATE KEY`,  Severity: "critical"},
    {Name: "Bearer Token",         Pattern: `Bearer [a-zA-Z0-9\-_\.]{32,}`,      Severity: "medium"},
    {Name: "Basic Auth",           Pattern: `Authorization: Basic [a-zA-Z0-9+/=]{20,}`, Severity: "medium"},
    {Name: "JWT Secret hardcoded", Pattern: `jwt[_-]?secret\s*[=:]\s*["'][^"']{8,}`,    Severity: "high"},
    {Name: "Password in code",     Pattern: `password\s*[=:]\s*["'][^"']{6,}["']`,      Severity: "high"},
}

// Skip these files/dirs — too noisy or irrelevant:
var skipPaths = []string{
    "node_modules", "vendor", ".git", "__pycache__",
    "*.min.js", "*.map", "dist/", "build/", ".next/",
}

// Always flag: .env file present in manifest and NOT in .gitignore
```

---

## Claude API Integration

### `internal/claude/client.go`

```go
package claude

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "os"
    "time"
)

type Client struct {
    apiKey  string
    model   string
    baseURL string
    http    *http.Client
}

func NewClient() *Client {
    return &Client{
        apiKey:  os.Getenv("ANTHROPIC_API_KEY"),
        model:   os.Getenv("ANTHROPIC_MODEL"),
        baseURL: "https://api.anthropic.com/v1",
        http:    &http.Client{Timeout: 120 * time.Second},
    }
}

// Analyze sends deterministic findings + key file snippets to Claude
// and receives a structured SemanticReport.
func (c *Client) Analyze(ctx context.Context, input *AnalysisContext) (*analyzer.SemanticReport, error) {
    prompt := buildPrompt(input)

    reqBody := map[string]any{
        "model":      c.model,
        "max_tokens": 4096,
        "system":     systemPrompt(),
        "messages": []map[string]any{
            {"role": "user", "content": prompt},
        },
    }

    body, _ := json.Marshal(reqBody)
    req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/messages", bytes.NewReader(body))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("x-api-key", c.apiKey)
    req.Header.Set("anthropic-version", "2023-06-01")

    resp, err := c.http.Do(req)
    if err != nil {
        return nil, fmt.Errorf("claude API call failed: %w", err)
    }
    defer resp.Body.Close()

    respBytes, _ := io.ReadAll(resp.Body)
    if resp.StatusCode != 200 {
        return nil, fmt.Errorf("claude API error %d: %s", resp.StatusCode, respBytes)
    }

    var envelope struct {
        Content []struct {
            Text string `json:"text"`
        } `json:"content"`
    }
    json.Unmarshal(respBytes, &envelope)

    if len(envelope.Content) == 0 {
        return nil, fmt.Errorf("empty response from Claude")
    }

    var report analyzer.SemanticReport
    if err := json.Unmarshal([]byte(envelope.Content[0].Text), &report); err != nil {
        return nil, fmt.Errorf("failed to parse Claude JSON: %w\nraw: %s", err, envelope.Content[0].Text)
    }

    return &report, nil
}

func systemPrompt() string {
    return `You are a senior DevOps engineer and cloud architect. You analyze codebases and provide structured deployment readiness assessments. You return only valid JSON matching the exact schema requested. No explanations, no markdown, no code blocks — only the JSON object.`
}

func buildPrompt(ctx *AnalysisContext) string {
    // Build a structured prompt containing:
    // 1. Stack summary from deterministic analysis
    // 2. Key file contents (main entry, routes, db, Dockerfile if exists) — max 1500 chars each
    // 3. List of all env vars referenced
    // 4. All secret findings from regex scan
    // 5. Infrastructure check results (what exists/doesn't exist)
    // 6. The complete JSON schema to fill in
    //
    // The schema to return is the SemanticReport struct as JSON schema.
    // See ARCHITECTURE.md section 5 for the full schema definition.
    //
    // Key fields the prompt must instruct Claude to fill:
    // - auth_assessment: describe which routes have/lack auth middleware
    // - security_risks: array of {description, severity, file, line, fix}
    // - critical_gaps: must-fix before deploying
    // - warnings: should fix
    // - suggestions: nice to have
    // - min_ram_mb, rec_ram_mb, min_cpu, storage_gb, est_rps
    // - resource_reasoning: plain English explanation
    // - readiness_score: 0-100
    // - readiness_summary: 2-3 sentences
    // - platforms: ranked array of PlatformRec
    return buildStructuredPrompt(ctx)
}
```

---

## CLI Implementation

### `cmd/deployable/main.go`

```go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "deployable/internal/client"
    "deployable/internal/analyzer"
    "deployable/internal/renderer"
)

var (
    apiKey     string
    outputFmt  string
    offline    bool
    apiBaseURL string
    noColor    bool
)

func main() {
    root := &cobra.Command{
        Use:   "deployable [path]",
        Short: "Deployment readiness checker for your codebase",
        Long: `Deployable analyzes your codebase and tells you what's missing
before you deploy to production. It checks for security issues,
missing config files, and recommends the best hosting platform.

Examples:
  deployable .                    # analyze current directory
  deployable ./my-app             # analyze specific directory
  deployable . --offline          # run without internet (limited)
  deployable . --output json      # JSON output for CI/CD pipelines
  deployable . --api-key KEY      # use with your Deployable account`,
        Args: cobra.MaximumNArgs(1),
        RunE: runAnalysis,
    }

    root.Flags().StringVar(&apiKey, "api-key", os.Getenv("DEPLOYABLE_API_KEY"), "Deployable API key (or set DEPLOYABLE_API_KEY)")
    root.Flags().StringVar(&outputFmt, "output", "text", "Output format: text | json")
    root.Flags().BoolVar(&offline, "offline", false, "Run offline (skips semantic analysis, no shareable URL)")
    root.Flags().StringVar(&apiBaseURL, "api-url", client.DefaultAPIURL, "Deployable API base URL")
    root.Flags().BoolVar(&noColor, "no-color", false, "Disable colored output")

    root.AddCommand(authCmd())
    root.AddCommand(versionCmd())

    if err := root.Execute(); err != nil {
        os.Exit(1)
    }
}

func runAnalysis(cmd *cobra.Command, args []string) error {
    dir := "."
    if len(args) > 0 {
        dir = args[0]
    }

    // Validate directory
    if _, err := os.Stat(dir); err != nil {
        return fmt.Errorf("directory not found: %s", dir)
    }

    r := renderer.New(!noColor)
    r.PrintHeader()

    if offline {
        // Run deterministic analysis only — no API call
        r.PrintStep("Running offline analysis...")
        result, err := analyzer.Run(cmd.Context(), dir, nil)
        if err != nil {
            return err
        }
        r.PrintResult(result, "")
        return nil
    }

    // Post to Deployable API
    c := client.New(apiBaseURL, apiKey)

    r.PrintStep("Uploading project files...")
    jobID, err := c.SubmitDirectory(cmd.Context(), dir)
    if err != nil {
        // Fall back to offline if API unavailable
        r.PrintWarning("API unavailable, running offline analysis...")
        result, err := analyzer.Run(cmd.Context(), dir, nil)
        if err != nil {
            return err
        }
        r.PrintResult(result, "")
        return nil
    }

    r.PrintStep("Analyzing with Deployable...")
    report, reportURL, err := c.PollForResult(cmd.Context(), jobID)
    if err != nil {
        return err
    }

    if outputFmt == "json" {
        return r.PrintJSON(report)
    }

    r.PrintResult(report, reportURL)
    return nil
}
```

---

## Report Page — UI Design

### `templates/report/index.html`

The report page is the most important UI surface. Design for clarity and actionability.

Structure:
```
┌─────────────────────────────────────────────────────────────┐
│  Deployable Report                           [Share] [Download] │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────┐  ┌──────────────────────────────────────────┐ │
│  │  Score  │  │  my-app · Go 1.22 · PostgreSQL + Redis   │ │
│  │   72    │  │  Analyzed 2 minutes ago · 847 files      │ │
│  │  /100   │  └──────────────────────────────────────────┘ │
│  └─────────┘                                               │
│  "Your app is mostly ready. Fix 2 critical issues first."   │
│                                                              │
├──────── 🔴 Critical Issues (2) ─────────────────────────────┤
│  • Hardcoded Postgres password in db/db.go line 14          │
│  • No authentication on /api/admin routes                   │
│                                                              │
├──────── ⚠️  Warnings (4) ───────────────────────────────────┤
│  • No Dockerfile found → generated one below                │
│  • DATABASE_URL missing from .env.example                   │
│  • No health check endpoint                                 │
│  • .env file not in .gitignore                              │
│                                                              │
├──────── 💡 Suggestions (3) ─────────────────────────────────┤
├──────── 📦 Resource Estimates ──────────────────────────────┤
│  Minimum: 512MB RAM · 0.5 vCPU · 5GB storage               │
│  Recommended: 1GB RAM · 1 vCPU · 20GB storage               │
│  "This Go app with Postgres will be comfortable on a..."    │
│                                                              │
├──────── 🚀 Deploy To ───────────────────────────────────────┤
│  1. Render    $7/month  ████████████  Best fit for this app │
│  2. Fly.io    $5/month  ███████████   Excellent for Go      │
│  3. Railway   $5/month  █████████     Quick setup           │
│     [View deployment steps for Render ▼]                    │
│                                                              │
├──────── 📄 Generated Files ─────────────────────────────────┤
│  [Download all as .zip]                                     │
│  Dockerfile    docker-compose.yml    .env.example           │
│  render.yaml   .github/workflows/deploy.yml   DEPLOYMENT.md │
│  [Preview: Dockerfile ▼] ← HTMX expand                     │
└─────────────────────────────────────────────────────────────┘
```

All collapsible sections use HTMX:
```html
<button hx-get="/report/{slug}/section/platforms"
        hx-target="#platforms-content"
        hx-swap="innerHTML"
        hx-trigger="click once">
  View deployment steps for Render ▼
</button>
<div id="platforms-content"></div>
```

Score badge color:
- 80-100: green (#22c55e)
- 60-79: yellow (#eab308)
- 40-59: orange (#f97316)
- 0-39: red (#ef4444)

---

## The Analyze Page — Three Input Methods

### `templates/analyze/index.html`

```html
<!-- Three tabs: ZIP | GITHUB | CLI -->
<div class="tabs">
  <button hx-get="/analyze?tab=zip" hx-target="#tab-content" class="tab active">
    📦 Upload ZIP
  </button>
  <button hx-get="/analyze?tab=github" hx-target="#tab-content" class="tab">
    🐙 GitHub URL
  </button>
  <button hx-get="/analyze?tab=cli" hx-target="#tab-content" class="tab">
    💻 CLI
  </button>
</div>

<div id="tab-content">
  <!-- ZIP TAB (default) -->
  <div id="zip-panel">
    <div class="drop-zone"
         hx-post="/analyze/zip"
         hx-encoding="multipart/form-data"
         hx-trigger="drop"
         hx-swap="none"
         hx-indicator="#upload-progress">
      <p>Drag & drop your project folder as a ZIP</p>
      <p class="text-sm text-gray-400">or</p>
      <input type="file" accept=".zip" name="file"
             hx-post="/analyze/zip"
             hx-encoding="multipart/form-data"
             hx-trigger="change"
             hx-swap="none">
      <p class="text-xs text-gray-500">Max 50MB · No account required</p>
    </div>
    <div id="upload-progress" class="htmx-indicator">Uploading...</div>
  </div>

  <!-- GITHUB TAB -->
  <form hx-post="/analyze/github" hx-swap="none">
    <input type="text" name="url"
           placeholder="github.com/username/repository"
           pattern="^(https?://)?github\.com/[^/]+/[^/]+"
           required>
    <p class="hint">Public repos: no account needed.
       Private repos: <a href="/auth/github">Connect GitHub</a></p>
    <button type="submit">Analyze Repository</button>
  </form>

  <!-- CLI TAB -->
  <div id="cli-panel">
    <div class="install-options">
      <!-- macOS -->
      <div class="platform">
        <span>macOS</span>
        <code>curl -sSL https://deployable.dev/install.sh | bash</code>
        <button onclick="copyToClipboard(this)">Copy</button>
      </div>
      <!-- Linux -->
      <div class="platform">
        <span>Linux</span>
        <code>curl -sSL https://deployable.dev/install.sh | bash</code>
        <button onclick="copyToClipboard(this)">Copy</button>
      </div>
      <!-- Windows -->
      <div class="platform">
        <span>Windows</span>
        <code>winget install deployable</code>
        <button onclick="copyToClipboard(this)">Copy</button>
      </div>
    </div>
    <div class="usage">
      <p>Then run in your project directory:</p>
      <code>deployable .</code>
    </div>
    <div class="api-key-section">
      <p>For a shareable report URL, add your API key:</p>
      {{if .User}}
        <code>deployable . --api-key {{.User.APIKey}}</code>
      {{else}}
        <p><a href="/register">Create a free account</a> to get an API key</p>
      {{end}}
    </div>
  </div>
</div>
```

---

## Processing Page — HTMX Live Progress

### `templates/analyze/processing.html`

```html
<div id="status-container"
     hx-get="/analyze/{{.JobID}}/status"
     hx-trigger="every 2s"
     hx-swap="innerHTML"
     hx-target="#status-container">

  <!-- Steps (updated via HTMX polling) -->
  <div class="steps">
    {{range $i, $step := .Steps}}
    <div class="step {{if lt $i $.CurrentStep}}complete{{else if eq $i $.CurrentStep}}active{{end}}">
      <span class="step-icon">
        {{if lt $i $.CurrentStep}}✓{{else if eq $i $.CurrentStep}}⟳{{else}}○{{end}}
      </span>
      <span>{{$step}}</span>
    </div>
    {{end}}
  </div>

  <p class="step-message">{{.StepMessage}}</p>
</div>
```

Steps displayed:
1. "Reading project files"
2. "Detecting stack and framework"
3. "Scanning for security issues"
4. "Analyzing with AI"
5. "Estimating resource requirements"
6. "Generating deployment files"

When complete, the status handler returns `HX-Redirect: /report/{slug}` header.

---

## `internal/generator/` — File Generation

Each generator function takes `*analyzer.Result` and returns a string (file content).

### Dockerfile Generator Logic

```go
func GenerateDockerfile(result *analyzer.Result) string {
    switch result.StackInfo.Language {
    case "Go":
        return goDockerfile(result.StackInfo.LanguageVersion)
    case "Node.js":
        return nodeDockerfile(result.StackInfo.LanguageVersion, result.StackInfo.Framework)
    case "Python":
        return pythonDockerfile(result.StackInfo.LanguageVersion, result.StackInfo.Framework)
    case "Ruby":
        return rubyDockerfile(result.StackInfo.LanguageVersion)
    default:
        return genericDockerfile()
    }
}

// Go Dockerfile template:
func goDockerfile(version string) string {
    return fmt.Sprintf(`FROM golang:%s-alpine AS builder
RUN apk add --no-cache git ca-certificates
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/server .
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=10s CMD wget -qO- http://localhost:8080/health || exit 1
CMD ["./server"]`, version)
}
```

### .env.example Generator

```go
func GenerateEnvExample(envVars []string, stackInfo analyzer.StackInfo) string {
    // For each env var found in codebase:
    // - Add a commented description based on var name
    // - Group by category (database, auth, api keys, app config)
    // Standard descriptions for common var names:
    descriptions := map[string]string{
        "DATABASE_URL":      "Postgres connection string e.g. postgres://user:pass@host:5432/db",
        "REDIS_URL":         "Redis connection string e.g. redis://localhost:6379/0",
        "PORT":              "HTTP port to listen on",
        "SECRET_KEY":        "Random secret for sessions/cookies. Generate: openssl rand -hex 32",
        "JWT_SECRET":        "JWT signing secret. Generate: openssl rand -hex 32",
        "API_KEY":           "API key for external service",
        // ... etc
    }
}
```

---

## Phase Build Order

### Phase 1 — Foundation (Week 1)
```
Deliverables:
  - All Docker files (Dockerfile, dev, compose prod+dev)
  - Makefile with all commands
  - .env.example
  - go.mod with all dependencies
  - All 5 migration SQL files
  - db/db.go (pgxpool + golang-migrate)
  - cache/redis.go (go-redis client + helper methods)
  - models/ (all 4 model files with DB methods)
  - middleware/ (auth, ratelimit, logger)
  - cmd/server/main.go (full bootstrap)
  - Auth handlers (register, login, logout)
  - All auth templates + base.html + landing.html

Success criteria:
  make dev starts all 4 containers cleanly
  /health returns {"status":"ok","postgres":"ok","redis":"ok"}
  Register → login → session cookie → logout works
  make psql shows all 5 tables
  make redis-cli PING returns PONG
```

### Phase 2 — Analysis Engine (Week 1-2)
```
Deliverables:
  - internal/analyzer/ (all 6 files)
  - internal/claude/client.go
  - handlers/analyze.go (zip upload + status polling)
  - templates/analyze/index.html (zip tab only)
  - templates/analyze/processing.html (HTMX polling)
  - templates/analyze/status.html (partial)
  - handlers/report.go (basic report view)
  - templates/report/index.html (full report page)

Success criteria:
  Upload a real zip file of any Go project
  Processing page shows live steps
  Report page renders with:
    - Readiness score
    - Stack detection correct
    - Secret findings (if any)
    - Infrastructure gaps
    - Claude semantic analysis results
    - Resource estimates
    - Platform recommendations
```

### Phase 3 — GitHub Input (Week 2)
```
Deliverables:
  - internal/github/client.go
  - GitHub OAuth handler (auth.go additions)
  - handlers/analyze.go GitHub handler
  - Analyze page GitHub tab
  - GitHub OAuth callback + token storage (encrypted)

Success criteria:
  Paste github.com/owner/repo → same pipeline runs
  Public repos: no auth needed
  Private repos: GitHub OAuth prompt → works after connect
```

### Phase 4 — CLI (Week 2-3)
```
Deliverables:
  - cmd/deployable/main.go (Cobra CLI)
  - internal/client/api.go
  - internal/renderer/terminal.go (lipgloss)
  - handlers/api.go (/api/v1/analyze endpoint)
  - middleware/auth.go API key validation
  - scripts/install.sh
  - make build-cli

Success criteria:
  deployable . runs in any project directory
  Terminal output shows colored report
  --offline flag works without internet
  With --api-key, prints shareable URL
  API key auth works end to end
```

### Phase 5 — Polish + VPS Deploy (Week 3)
```
Deliverables:
  - Dashboard (saved reports list)
  - Report download (generated files as zip)
  - Rate limiting verified working
  - Caddyfile tuned for production
  - scripts/backup.sh
  - make deploy works on VPS
  - UptimeRobot monitoring set up

Success criteria:
  All three input methods work on production VPS
  TLS certificate provisioned by Caddy automatically
  make deploy runs without downtime
  make backup creates valid Postgres dump
  /health returns 200 from public URL
```

---

## Definition of Done

Every item must pass before the project is considered complete:

- [ ] `make setup` creates `.env` from `.env.example`
- [ ] `make dev` starts app + postgres + redis + all healthy in under 60s
- [ ] `make psql` connects and shows all 5 tables
- [ ] `make redis-cli` connects and PING returns PONG
- [ ] `make build` produces a working production image
- [ ] `make up` starts full production stack (app + postgres + redis + caddy)
- [ ] `make deploy` updates app without stopping Caddy
- [ ] `make backup` creates a `.sql.gz` in `backups/`
- [ ] `make build-cli` produces 5 platform binaries in `dist/`
- [ ] `make clean` removes all containers and volumes
- [ ] `/health` returns `{"status":"ok","postgres":"ok","redis":"ok"}`
- [ ] ZIP upload: drag a zip → processing page → report rendered
- [ ] GitHub URL: paste public repo URL → same pipeline runs
- [ ] GitHub OAuth: private repo accessible after connecting GitHub
- [ ] CLI: `deployable .` in any project dir renders colored terminal report
- [ ] CLI offline: `deployable . --offline` works without internet
- [ ] CLI with API key: posts to API, prints shareable URL
- [ ] Report: readiness score, security findings, resource estimates all render
- [ ] Report: platform recommendations with cost estimates rendered
- [ ] Report: generated files downloadable as zip
- [ ] Report: shareable URL works without login
- [ ] Rate limiting: 5 analyses/hour for anonymous users (Redis-backed)
- [ ] Content hash deduplication: same repo returns cached report instantly
- [ ] Anonymous reports expire after 7 days (Postgres expires_at enforced)
- [ ] Secret scanning: hardcoded API keys flagged correctly
- [ ] Stack detection: Go, Node.js, Python all detected correctly
- [ ] Caddy provisions TLS automatically on VPS (production only)
- [ ] All data persists across `make down && make up`
- [ ] No hardcoded secrets — all config via `.env`
- [ ] App starts with log.Fatal if any required env var is missing