-- 0003_add_auth_fields.down.sql
BEGIN;
DROP TABLE IF EXISTS refresh_tokens;
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
COMMIT;
