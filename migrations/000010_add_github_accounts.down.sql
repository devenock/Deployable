ALTER TABLE users ADD COLUMN github_token TEXT;

ALTER TABLE connected_repos DROP COLUMN IF EXISTS github_account_id;

DROP TABLE IF EXISTS github_accounts;
