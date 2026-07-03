CREATE TABLE github_accounts (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    github_id    TEXT NOT NULL,
    github_login TEXT NOT NULL,
    avatar_url   TEXT,
    token        TEXT NOT NULL,
    connected_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, github_id)
);

CREATE INDEX idx_github_accounts_user_id ON github_accounts(user_id);

ALTER TABLE connected_repos
    ADD COLUMN github_account_id UUID REFERENCES github_accounts(id) ON DELETE CASCADE;

ALTER TABLE users DROP COLUMN github_token;
