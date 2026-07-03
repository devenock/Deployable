DROP INDEX IF EXISTS idx_users_google_id;

ALTER TABLE users
    DROP COLUMN IF EXISTS google_id,
    DROP COLUMN IF EXISTS email_verified_at,
    DROP COLUMN IF EXISTS welcomed_at,
    DROP COLUMN IF EXISTS last_login_at;
