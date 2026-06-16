-- 0005_seed_pop_external_step.up.sql
-- Adds step 4 (condition_type=3, external signer) to the POP dev template so
-- the seed demonstrates all three condition types (1, 2, 3) as required by the
-- Phase 1 audit checklist. DEV/UAT only — do NOT run in production.
--
-- Also adds a demo external_signer user (non-login, for documentation only).

BEGIN;

-- Add CUSTOMER step (seq 4, condition_type=3) to POP v1.
INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
SELECT t.id, 'CUSTOMER', 'ผู้เซ็นภายนอก (ลูกค้า)', 4, 3
  FROM workflow_templates t
 WHERE t.doc_format_code = 'POP' AND t.version = 1
   AND NOT EXISTS (
       SELECT 1 FROM workflow_steps s
        WHERE s.workflow_template_id = t.id AND s.position_code = 'CUSTOMER'
   );

-- No assignee row for condition_type=3 — external signers are created per-document
-- at import time via the external_signers table, not via workflow_step_assignees.

COMMIT;
