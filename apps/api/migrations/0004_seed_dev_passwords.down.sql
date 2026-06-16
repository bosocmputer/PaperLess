-- 0004_seed_dev_passwords.down.sql
BEGIN;
UPDATE users SET password_hash = NULL WHERE username IN ('admin', 'maker', 'checkerA', 'checkerB', 'approver');
COMMIT;
