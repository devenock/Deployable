ALTER TABLE users
    ADD COLUMN google_id         TEXT UNIQUE,
    ADD COLUMN email_verified_at TIMESTAMPTZ,
    ADD COLUMN welcomed_at       TIMESTAMPTZ,
    ADD COLUMN last_login_at     TIMESTAMPTZ;

CREATE INDEX idx_users_google_id ON users(google_id);
