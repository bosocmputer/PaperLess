-- 0001_init.down.sql
-- Reverse of 0001_init.up.sql. Drops in dependency order.

BEGIN;

DROP TABLE IF EXISTS sml_sync_jobs;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS signature_events;
DROP TABLE IF EXISTS signature_tasks;
ALTER TABLE IF EXISTS document_files DROP CONSTRAINT IF EXISTS fk_document_files_ext_signer;
DROP TABLE IF EXISTS external_signers;
DROP TABLE IF EXISTS document_files;
DROP TABLE IF EXISTS documents;
DROP TABLE IF EXISTS workflow_step_assignees;
DROP TABLE IF EXISTS workflow_steps;
DROP TABLE IF EXISTS workflow_templates;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;

COMMIT;
