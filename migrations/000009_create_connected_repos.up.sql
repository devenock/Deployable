CREATE TABLE connected_repos (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id        UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    github_id      BIGINT NOT NULL,
    full_name      TEXT NOT NULL,
    private        BOOLEAN NOT NULL DEFAULT FALSE,
    default_branch TEXT,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, github_id)
);

CREATE INDEX idx_connected_repos_user_id ON connected_repos(user_id);
