-- 0002_seed_dev.down.sql
-- Remove dev seed data. Cascades clean up steps/assignees/user_roles.

BEGIN;

DELETE FROM workflow_templates WHERE doc_format_code='POP' AND version=1;
DELETE FROM users WHERE username IN ('admin','maker','checkerA','checkerB','approver');
DELETE FROM roles WHERE code IN ('system_admin','workflow_admin','document_admin','signer','auditor','integration');

COMMIT;
