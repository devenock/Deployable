# ARCHITECTURE.md — Deployable
> Deployment Readiness Platform for Vibe Coders

---

## 1. System Overview

Deployable is a DevOps tooling platform that sits between local/AI development and production deployment. It accepts a codebase via three input methods (zip upload, GitHub URL, CLI), analyzes it using deterministic file inspection and AI-powered semantic analysis, and produces a Deployment Readiness Report covering stack detection, security flags, resource estimation, platform recommendations, and generated deployment files.

It runs as a Go monolith on a single VPS, with all services orchestrated via Docker Compose and accessed through Makefile commands.

---

## 2. High-Level Architecture

```
┌─────────────────────────────────────────────────────────────────────────┐
│                            VPS (Ubuntu 24 LTS)                          │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │                     Docker Compose Network                        │  │
│  │                      (deployable_network)                          │  │
│  │                                                                   │  │
│  │   ┌─────────────────┐    ┌──────────────┐    ┌───────────────┐  │  │
│  │   │   Caddy (TLS)   │    │   Go App     │    │  PostgreSQL   │  │  │
│  │   │   :80 / :443    │───▶│   :8080      │───▶│   :5432       │  │  │
│  │   │  (reverse proxy)│    │  (monolith)  │    │  (internal)   │  │  │
│  │   └─────────────────┘    └──────┬───────┘    └───────────────┘  │  │
│  │                                 │                                 │  │
│  │                                 │            ┌───────────────┐   │  │
│  │                                 └───────────▶│     Redis     │   │  │
│  │                                              │   :6379       │   │  │
│  │                                              │  (internal)   │   │  │
│  │                                              └───────────────┘   │  │
│  │                                                                   │  │
│  │   ┌─────────────────────────────────────────────────────────┐   │  │
│  │   │                  Named Volumes                           │   │  │
│  │   │  deployable_pgdata  │  deployable_uploads  │  caddy_data  │   │  │
│  │   └─────────────────────────────────────────────────────────┘   │  │
│  └──────────────────────────────────────────────────────────────────┘  │
│                                                                         │
│  ┌──────────────────────────────────────────────────────────────────┐  │
│  │                     Host System                                   │  │
│  │   - Docker Engine + Docker Compose v2                             │  │
│  │   - Makefile (single entry point for all operations)              │  │
│  │   - /opt/deployable/ (app root, all files here)                    │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────────────────┘

External:
  - Users (browser, CLI binary)
  - GitHub API (repo fetching)
  - Anthropic Claude API (semantic analysis)
```

---

## 3. Component Details

### 3.1 Caddy (Reverse Proxy + TLS)
- Handles all inbound traffic on ports 80 and 443
- Automatically provisions and renews TLS certificates via Let's Encrypt
- Proxies all traffic to the Go app on :8080
- Serves no static files directly — all assets served by Go
- Config: `Caddyfile` in repo root, mounted into container
- Why Caddy over Nginx: zero-config TLS, single file config, no certbot cron needed

### 3.2 Go Monolith (:8080)
The single Go binary handles everything:
- HTTP server (chi router)
- HTML template rendering (html/template)
- File analysis engine (deterministic checks)
- Claude API client (semantic analysis)
- GitHub API client (repo fetching)
- Zip extraction and file walking
- Report generation and storage
- Background job processing (goroutines)
- CLI binary (separate build target, same module)
- HTMX partial rendering
- Static asset serving (embedded via go:embed)

The monolith is intentional. No microservices, no message queues, no worker processes. Go's goroutines handle concurrency. Redis handles caching and job state. Postgres handles persistence. This is the correct architecture for a VPS deployment with predictable load.

### 3.3 PostgreSQL (:5432, internal only)
- Never exposed outside Docker network
- Stores: users, sessions, analyses, reports, generated files
- Migrations run automatically on app startup via golang-migrate
- Volume: `deployable_pgdata` — persists across container restarts and updates
- Connection pool: pgxpool, max 20 connections

### 3.4 Redis (:6379, internal only)
- Never exposed outside Docker network
- Used for:
  - Analysis job state (pending/running/complete/failed)
  - Rate limiting (per-IP, per-user)
  - Session caching (reduce Postgres reads)
  - Claude API response caching (same repo+commit = cached report, 24h TTL)
  - GitHub API response caching (rate limit protection)
- Client: `github.com/redis/go-redis/v9`

### 3.5 CLI Binary
- Separate build target: `cmd/deployable/main.go`
- Distributed as a static binary for Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)
- Behavior:
  1. Walk current directory (or specified path)
  2. Collect relevant files (respecting .gitignore)
  3. POST to Deployable API with files as multipart form
  4. Poll for analysis completion
  5. Render colored terminal report
  6. Print shareable URL to web report
- Can run fully offline (limited mode) without posting to API
- Built and released via GitHub Actions on tag push

---

## 4. Data Flow — Three Input Methods

### 4.1 Zip Upload

```
Browser
  │
  │  POST /analyze/zip (multipart, max 50MB)
  ▼
Go Handler
  │
  ├── Validate: zip format, size limit
  ├── Extract to temp dir (/tmp/deployable/{job_id}/)
  ├── Create analysis job in Postgres (status: pending)
  ├── Store job_id in Redis (TTL: 1h)
  │
  └── Goroutine: run analysis pipeline
        │
        ├── [1] File Walker: collect all files, build manifest
        ├── [2] Deterministic Analyzer: run all rule checks
        ├── [3] Claude API: semantic analysis call
        ├── [4] Report Builder: assemble full report
        ├── [5] File Generator: generate deployment files
        └── [6] Save to Postgres, update Redis status: complete
  │
  ▼ (HTMX polls /analyze/{job_id}/status every 2s)
  │
  └── Redirect to /report/{report_id}
```

### 4.2 GitHub URL

```
Browser
  │
  │  POST /analyze/github (body: {url: "github.com/user/repo"})
  ▼
Go Handler
  │
  ├── Parse URL: extract owner/repo/branch
  ├── GitHub API: GET /repos/{owner}/{repo} (check exists, get default branch)
  ├── GitHub API: GET /repos/{owner}/{repo}/zipball/{branch}
  │     (uses OAuth token if private, unauthenticated if public)
  ├── Download zip to temp dir
  └── Continue same pipeline as zip upload above

  For private repos:
  ├── Check if user has connected GitHub OAuth
  ├── If not: redirect to /auth/github with return_url
  └── If yes: use stored OAuth token for API calls
```

### 4.3 CLI

```
User terminal
  │
  │  $ deployable . [--api-key KEY] [--output json|text]
  ▼
CLI Binary (Go)
  │
  ├── Walk directory, collect files (respects .gitignore)
  ├── Build multipart form with files
  │
  ├── If --offline flag: run deterministic checks only, no API call
  │     Output: colored terminal report, no shareable URL
  │
  └── If online (default):
        │
        │  POST https://deployable.dev/api/v1/analyze/cli
        │  Headers: X-API-Key: {user_api_key}
        ▼
      Go API Handler
        │
        ├── Authenticate via API key (lookup in Postgres, cached in Redis)
        ├── Same pipeline as zip upload
        └── Return: {report_url, summary_json}
        │
        ▼
      CLI Binary
        ├── Render colored terminal output (lipgloss)
        └── Print: "Full report: https://deployable.dev/report/abc123"
```

---

## 5. Analysis Pipeline — Detailed

The analysis pipeline runs identically regardless of input method. It has two layers.

### Layer 1 — Deterministic Analysis (Go, no AI)
Fast, runs in milliseconds. Checks facts that don't require understanding intent.

```
FileWalker
  │
  ├── Stack Detection
  │     go.mod → Go version, dependencies
  │     package.json → Node version, framework (Next.js, Express, etc.)
  │     requirements.txt / pyproject.toml → Python, framework (FastAPI, Django)
  │     Cargo.toml → Rust
  │     pom.xml / build.gradle → Java/Kotlin
  │     Gemfile → Ruby/Rails
  │     composer.json → PHP/Laravel
  │
  ├── Infrastructure File Checks
  │     Dockerfile: exists? multi-stage? EXPOSE defined? CMD/ENTRYPOINT set?
  │     docker-compose.yml: exists? services defined? volumes named?
  │     .env.example: exists? matches .env vars found in code?
  │     .gitignore: exists? includes .env? includes node_modules/vendor?
  │     CI config: .github/workflows/ or .gitlab-ci.yml exists?
  │     Health check: does any route match /health or /healthz?
  │
  ├── Environment Variable Extraction
  │     Scan all .go/.js/.ts/.py/.rb files for:
  │       os.Getenv("X") → Go
  │       process.env.X → Node
  │       os.environ["X"] / os.getenv("X") → Python
  │       ENV["X"] → Ruby
  │     Build complete list of all referenced env vars
  │     Cross-reference with .env.example (what's missing?)
  │
  ├── Secret Detection (regex patterns)
  │     Hardcoded: API keys, passwords, connection strings in source files
  │     Patterns: sk-*, ghp_*, postgres://user:pass@, Bearer [A-Za-z0-9]{32,}
  │     File-specific: .env committed to repo (not in .gitignore)
  │     Private keys: -----BEGIN RSA PRIVATE KEY-----
  │
  ├── Database Detection
  │     Import/require patterns for: pg, psycopg2, sqlalchemy, mysql, mongo,
  │       redis, sqlite3, gorm, prisma, sequelize, typeorm
  │     Connection string patterns in code
  │     docker-compose.yml services
  │
  ├── Port Detection
  │     ListenAndServe(":PORT") → Go
  │     app.listen(PORT) → Node
  │     uvicorn/gunicorn port args → Python
  │     EXPOSE in Dockerfile
  │
  └── Dependency Count + Lock File
        go.sum, package-lock.json, poetry.lock, Gemfile.lock present?
        (missing lock file = non-reproducible build = deployment risk)
```

### Layer 2 — Semantic Analysis (Claude API, one call)
Runs after Layer 1. Receives a structured context object built from Layer 1 results plus relevant file snippets.

```go
// What gets sent to Claude:
type AnalysisContext struct {
    StackSummary      string            // "Go 1.22 web app with PostgreSQL and Redis"
    DeterministicFindings []Finding     // all Layer 1 results
    FileManifest      []FileEntry       // path + size + extension for every file
    KeyFileContents   map[string]string // main.go, routes, db.go, Dockerfile (truncated to 2000 chars each)
    EnvVarsFound      []string          // all referenced env vars
    SecretsFound      []SecretFinding   // from regex scan
    DatabasesDetected []string
    FrameworkDetected string
}

// What Claude returns (structured JSON):
type SemanticReport struct {
    // Security
    AuthAssessment    string   // "Routes /admin and /api/users have no auth middleware detected"
    SecurityRisks     []Risk   // each with severity: critical/high/medium/low
    
    // Architecture
    ArchitectureSummary string // plain English: "This is a REST API with..."
    ComplexityScore   int      // 1-10
    
    // Production readiness gaps
    CriticalGaps      []string // must fix before deploying
    Warnings          []string // should fix
    Suggestions       []string // nice to have
    
    // Resource estimation
    MinRAMMB          int      // minimum RAM in MB
    RecommendedRAMMB  int      // recommended RAM in MB  
    MinCPU            float64  // minimum vCPUs
    StorageGB         int      // estimated storage need
    EstimatedRPS      int      // rough requests/sec capacity at recommended size
    Reasoning         string   // plain English explanation of estimates
    
    // Platform recommendations
    Platforms []PlatformRec  // ranked list
    
    // Plain English summary
    ReadinessSummary  string   // 2-3 sentence human summary
    ReadinessScore    int      // 0-100
}

type PlatformRec struct {
    Name         string  // "Render", "Fly.io", "Railway", "DigitalOcean App Platform"
    Rank         int
    MonthlyUSD   string  // "$7-14/month" 
    InstanceType string  // "512MB RAM, 0.5 vCPU"
    Reasoning    string  // why this platform fits this specific app
    DeploySteps  []string // exact steps for this app on this platform
    ConfigFile   string  // generated fly.toml or render.yaml content
}
```

### Layer 3 — File Generation (Go templates)
After both analysis layers complete, Go generates deployment files using `text/template`:

```
Generated files (all downloadable as a zip):
  Dockerfile          ← correct for detected language/framework/version
  docker-compose.yml  ← with all detected services (db, redis, app)
  .env.example        ← every env var found in codebase, with descriptions
  .dockerignore       ← appropriate for detected stack
  fly.toml            ← for top-ranked platform if Fly.io
  render.yaml         ← for top-ranked platform if Render
  .github/workflows/
    deploy.yml        ← CI/CD for recommended platform
  DEPLOYMENT.md       ← step-by-step human-readable guide
```

---

## 6. Database Schema

```sql
-- Users (optional — anonymous analysis allowed)
CREATE TABLE users (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email         TEXT UNIQUE,
    name          TEXT,
    password_hash TEXT,
    github_id     TEXT UNIQUE,
    github_token  TEXT,            -- encrypted at rest
    api_key       TEXT UNIQUE,     -- for CLI authentication
    plan          TEXT NOT NULL DEFAULT 'free',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Sessions
CREATE TABLE sessions (
    id         TEXT PRIMARY KEY,
    user_id    UUID REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Analysis jobs (tracks pipeline state)
CREATE TABLE analysis_jobs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    input_type   TEXT NOT NULL,    -- 'zip' | 'github' | 'cli'
    input_ref    TEXT,             -- github URL or original filename
    status       TEXT NOT NULL DEFAULT 'pending',
                                   -- pending|running|complete|failed
    error_msg    TEXT,
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Reports (completed analysis results)
CREATE TABLE reports (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id           UUID NOT NULL REFERENCES analysis_jobs(id),
    user_id          UUID REFERENCES users(id) ON DELETE SET NULL,
    slug             TEXT UNIQUE NOT NULL,   -- short ID for public URL
    is_public        BOOLEAN NOT NULL DEFAULT TRUE,
    
    -- Stack info
    language         TEXT,
    framework        TEXT,
    databases        TEXT[],
    
    -- Scores
    readiness_score  INTEGER,     -- 0-100
    complexity_score INTEGER,     -- 1-10
    
    -- Full report stored as JSONB
    deterministic    JSONB,       -- Layer 1 findings
    semantic         JSONB,       -- Layer 2 Claude output
    
    -- Resource estimates
    min_ram_mb       INTEGER,
    rec_ram_mb       INTEGER,
    min_cpu          NUMERIC,
    storage_gb       INTEGER,
    
    -- Generated files stored as JSONB map: filename → content
    generated_files  JSONB,
    
    -- Platform recommendations as JSONB array
    platforms        JSONB,
    
    -- Cache key for Redis
    content_hash     TEXT,        -- SHA256 of analyzed files
    
    expires_at       TIMESTAMPTZ, -- NULL = permanent (logged in users)
                                  -- 7 days for anonymous
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Rate limiting reference (primary in Redis, Postgres for audit)
CREATE TABLE rate_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    identifier TEXT NOT NULL,    -- IP or user_id
    event_type TEXT NOT NULL,    -- 'analysis'
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Indexes
CREATE INDEX idx_reports_slug ON reports(slug);
CREATE INDEX idx_reports_user_id ON reports(user_id);
CREATE INDEX idx_reports_content_hash ON reports(content_hash);
CREATE INDEX idx_jobs_user_id ON analysis_jobs(user_id);
CREATE INDEX idx_jobs_status ON analysis_jobs(status);
CREATE INDEX idx_sessions_user_id ON sessions(user_id);
```

---

## 7. Redis Key Schema

```
# Analysis job state (TTL: 2h)
job:{job_id}:status          → "pending" | "running" | "complete" | "failed"
job:{job_id}:progress        → JSON: {step: 2, total: 6, message: "Running semantic analysis..."}
job:{job_id}:report_id       → UUID of completed report

# Report cache (TTL: 24h — same repo+commit = same result)
report:cache:{content_hash}  → report UUID

# Session cache (TTL: 30 days, mirrors cookie)
session:{session_id}         → JSON: {user_id, email, plan}

# Rate limiting (TTL: 1h rolling window)
ratelimit:analysis:ip:{ip}        → count (max 5/hour anonymous)
ratelimit:analysis:user:{user_id} → count (max 20/hour free, 100/hour pro)

# GitHub API cache (TTL: 5m)
github:repo:{owner}:{repo}   → JSON repo metadata
github:zip:{owner}:{repo}:{sha} → cached zip URL

# CLI API key validation cache (TTL: 5m)
apikey:{key_hash}            → JSON: {user_id, plan}
```

---

## 8. VPS Deployment Architecture

### Server Requirements (Minimum)
```
Provider: Hetzner CX22 or DigitalOcean Basic Droplet
CPU:      2 vCPU
RAM:      4GB
Storage:  40GB SSD
OS:       Ubuntu 24.04 LTS
Network:  IPv4 + IPv6
Firewall: ports 22 (SSH), 80 (HTTP), 443 (HTTPS) only
```

### Directory Structure on VPS
```
/opt/deployable/
├── docker-compose.yml          # production compose
├── docker-compose.override.yml # machine-specific overrides (not in git)
├── Caddyfile                   # reverse proxy config
├── .env                        # production secrets (never in git)
├── Makefile                    # all operational commands
└── data/                       # gitignored runtime data
    └── uploads/                # temp upload storage (mounted volume)
```

### Docker Network
All containers on internal bridge network `deployable_network`. Only Caddy has ports exposed to the host (80, 443). App, Postgres, Redis are internal only.

### Volume Strategy
```
deployable_pgdata    → Postgres data directory (never delete)
deployable_redis     → Redis AOF persistence (survives restart)
deployable_uploads   → Temp upload files (purge files > 1h old via cron)
caddy_data          → TLS certificates (never delete)
caddy_config        → Caddy config cache
```

### Deployment Flow (production update)
```bash
# On VPS:
cd /opt/deployable
git pull origin main          # pull latest
make build                    # rebuild Go binary + Docker image
make deploy                   # docker compose up -d --no-deps app
                              # zero-downtime: Caddy keeps serving during rebuild
```

---

## 9. Security Architecture

### What's Exposed to the Internet
- Port 443 (HTTPS via Caddy) — only entry point
- Port 80 (HTTP via Caddy) — immediately redirects to HTTPS
- Port 22 (SSH) — key-based only, password auth disabled

### What's Internal Only
- Port 8080 (Go app) — only reachable from Caddy container
- Port 5432 (Postgres) — only reachable from Go app container
- Port 6379 (Redis) — only reachable from Go app container

### Application Security
- GitHub OAuth tokens encrypted at rest (AES-256-GCM, key from env)
- API keys stored as SHA-256 hashes (never plaintext)
- Uploaded files stored in isolated temp directories, purged after analysis
- User uploads never stored permanently — only extracted content matters
- Claude API receives file content snippets, never full binaries
- Rate limiting: 5 analyses/hour anonymous, 20/hour free, 100/hour pro
- File size limit: 50MB zip upload maximum
- Excluded from analysis: node_modules, vendor, .git, __pycache__, *.lock files

### Content Hash Deduplication
Before running analysis, compute SHA-256 of all analyzed file contents. Check Redis cache. If hit: return cached report instantly, no Claude API call, no processing. This protects against:
- Repeated analysis of same repo
- Cost: each duplicate is $0.00 instead of ~$0.05
- Rate limit gaming

---

## 10. Caddyfile

```
{
    email admin@yourdomain.com
}

deployable.yourdomain.com {
    reverse_proxy app:8080 {
        header_up X-Forwarded-For {remote_host}
        header_up X-Real-IP {remote_host}
    }
    
    # Security headers
    header {
        Strict-Transport-Security "max-age=31536000; includeSubDomains"
        X-Content-Type-Options nosniff
        X-Frame-Options DENY
        Referrer-Policy strict-origin-when-cross-origin
    }
    
    # File upload size limit (matches Go handler limit)
    request_body {
        max_size 55MB
    }
    
    encode gzip
    log {
        output file /var/log/caddy/access.log
        format json
    }
}
```

---

## 11. Concurrency Model

Go handles all concurrency natively. No separate worker processes.

```
HTTP Request → Chi Router → Handler
                               │
                               ├── Sync path: validate, create job, return 202
                               │
                               └── go func() { runAnalysisPipeline(jobID) }
                                      │
                                      ├── Layer 1: deterministic (fast, in-process)
                                      ├── Layer 2: Claude API call (HTTP, ~10-30s)
                                      ├── Layer 3: file generation (fast, in-process)
                                      └── Update Postgres + Redis status

HTMX polling (every 2s):
  GET /analyze/{jobID}/status
    → Read from Redis (fast)
    → Return: spinner | progress | redirect to report
```

Maximum concurrent analyses: controlled by a semaphore in Go:
```go
var analysisSem = make(chan struct{}, 10) // max 10 concurrent analyses
```

This prevents memory exhaustion from simultaneous large zip extractions.

---

## 12. CLI Architecture

```
cmd/
└── deployable/
    └── main.go         # CLI entry point

internal/
├── analyzer/           # shared analysis logic (used by both web and CLI)
│   ├── walker.go       # file tree walking
│   ├── detector.go     # stack/framework detection
│   ├── secrets.go      # secret scanning
│   └── env.go          # env var extraction
├── client/
│   └── api.go          # HTTP client for posting to Deployable API
└── renderer/
    └── terminal.go     # colored terminal output (lipgloss)
```

The analyzer package is shared between the web app and the CLI. The web app calls `analyzer.Run(dir)` directly. The CLI calls the same function and either renders locally or posts to the API.

Build matrix (via Makefile + goreleaser):
```
GOOS=linux   GOARCH=amd64  → deployable-linux-amd64
GOOS=linux   GOARCH=arm64  → deployable-linux-arm64
GOOS=darwin  GOARCH=amd64  → deployable-darwin-amd64
GOOS=darwin  GOARCH=arm64  → deployable-darwin-arm64
GOOS=windows GOARCH=amd64  → deployable-windows-amd64.exe
```

---

## 13. Monitoring on VPS (Simple, No Extra Cost)

```
make logs          → docker compose logs -f (all containers)
make logs-app      → app container only
make logs-db       → postgres container only
make health        → curl https://deployable.yourdomain.com/health

/health endpoint returns:
{
  "status": "ok",
  "postgres": "ok",
  "redis": "ok",
  "version": "1.0.0",
  "uptime": "72h14m"
}
```

Uptime monitoring: UptimeRobot free tier pings `/health` every 5 minutes. Email alert on failure. No Datadog, no Prometheus — overkill for a VPS deployment.

---

## 14. Backup Strategy

```bash
# Runs via host cron daily at 02:00
make backup
  → docker exec deployable_postgres pg_dump -U ${POSTGRES_USER} ${POSTGRES_DB}
  → gzip output
  → save to /opt/deployable/backups/$(date +%Y%m%d).sql.gz
  → keep last 7 days, delete older

# Optional: rsync backups to remote storage
make backup-remote
  → rsync /opt/deployable/backups/ user@backup-server:/backups/deployable/
```

---

## 15. Request Flow — End to End

```
User opens https://deployable.yourdomain.com

1. Browser → Caddy (:443) → TLS termination
2. Caddy → Go app (:8080, internal) via reverse_proxy
3. Go app → chi router → handler
4. Handler creates analysis job:
   a. INSERT INTO analysis_jobs (status='pending')
   b. SET Redis job:{id}:status = "pending"
   c. go func() { runPipeline(id) }  ← non-blocking
   d. Return 202 with job_id
5. Browser HTMX polls /analyze/{id}/status every 2s
6. Pipeline goroutine:
   a. UPDATE Redis status = "running", step 1/6
   b. Layer 1: walk files (ms)
   c. UPDATE Redis step 2/6
   d. Layer 2: Claude API call (10-30s)
   e. UPDATE Redis step 5/6
   f. Layer 3: generate files (ms)
   g. INSERT INTO reports
   h. UPDATE Redis status = "complete", report_id = {uuid}
7. HTMX poll gets status=complete
   → Browser redirects to /report/{slug}
8. Go app renders report page from Postgres
   → Full HTMX page with collapsible sections
9. User downloads generated files as zip
   → GET /report/{slug}/download
   → Go streams zip from JSONB stored in Postgres
```

---

## 16. Technology Decision Log

| Decision | Choice | Rejected | Reason |
|---|---|---|---|
| Architecture | Monolith | Microservices | VPS deployment, predictable load, simpler ops |
| Language | Go | Python, Node | Performance, single binary, easy Docker |
| Frontend | HTMX | React, Vue | No build step, server-side rendering, Go templates |
| Styling | Tailwind CDN | Bundled CSS | No build step for MVP |
| Database | Postgres | MySQL, SQLite | JSONB support, UUID, arrays, production-grade |
| Cache | Redis | Memcached, in-memory | Persistence (AOF), pub/sub potential, rate limiting |
| Reverse proxy | Caddy | Nginx | Automatic TLS, simpler config |
| AI layer | Claude API | OpenAI, local LLM | Best code understanding, structured JSON output |
| Migrations | golang-migrate | GORM automigrate | Explicit SQL, reversible, auditable |
| CLI styling | lipgloss | custom ANSI | Charmbracelet ecosystem, maintained |
| TLS | Let's Encrypt via Caddy | Manual certs, Cloudflare | Zero config, auto-renew |
| Secrets | Environment variables | Vault, AWS SSM | Appropriate for single VPS, simple |