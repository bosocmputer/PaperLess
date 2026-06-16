-- 0003_add_auth_fields.up.sql
-- Add password_hash to users for local auth; do NOT edit 0001_init.

BEGIN;

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash text;

-- Refresh tokens (stored hashed; one row per active session).
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id    bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash text   NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS ix_refresh_tokens_user ON refresh_tokens (user_id);

COMMIT;
