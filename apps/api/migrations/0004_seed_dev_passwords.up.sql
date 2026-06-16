-- 0004_seed_dev_passwords.up.sql
-- Sets a known bcrypt hash for all dev seed users so they can log in during UAT.
-- DEV ONLY. Password for all accounts: password123
-- Do NOT run in production; rotate immediately if ever applied to prod.

BEGIN;

UPDATE users
SET password_hash = '$2a$10$HLSosIQLc/83FArbeaXMh.V91QFOHRgk/8Nd7HcE3V9OPye8faFFO'
WHERE username IN ('admin', 'maker', 'checkerA', 'checkerB', 'approver');

COMMIT;
