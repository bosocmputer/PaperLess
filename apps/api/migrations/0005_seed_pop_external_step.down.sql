-- 0005_seed_pop_external_step.down.sql
BEGIN;
DELETE FROM workflow_templates WHERE doc_format_code = 'DEMO3' AND version = 1;
COMMIT;
