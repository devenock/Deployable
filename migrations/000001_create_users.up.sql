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
