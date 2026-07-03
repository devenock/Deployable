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
