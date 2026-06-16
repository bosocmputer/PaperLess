-- 0005_seed_pop_external_step.down.sql
BEGIN;
DELETE FROM workflow_steps
 WHERE position_code = 'CUSTOMER'
   AND workflow_template_id IN (
       SELECT id FROM workflow_templates WHERE doc_format_code = 'POP' AND version = 1
   );
COMMIT;
