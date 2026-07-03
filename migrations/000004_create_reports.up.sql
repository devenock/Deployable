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
