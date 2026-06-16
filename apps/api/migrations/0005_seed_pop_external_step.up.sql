-- 0005_seed_pop_external_step.up.sql
-- Demonstrates all three condition types (1, 2, 3) in the dev seed, as required
-- by the Phase 1 audit checklist, WITHOUT breaking the POP happy-path.
--
-- IMPORTANT: condition_type=3 (external signer) is engine-ready but its import
-- path is deferred (no OTP/email-link flow in Phase 1). A document bound to a
-- template that contains an external step cannot currently auto-complete the
-- external step on import — so the external step must NOT live on the active POP
-- template, or POP documents would never reach `completed`.
--
-- Therefore we seed a SEPARATE template (doc_format_code='DEMO3', status='draft')
-- that contains condition 1 + 2 + 3 for config demonstration and future
-- external-flow testing. It is 'draft' (not 'active'), so import never binds a
-- real document to it until the external flow is built in a later phase.
-- DEV/UAT only — do NOT run in production.

BEGIN;

-- Demo template showing condition 1/2/3 (draft — not used by import).
INSERT INTO workflow_templates (doc_format_code, name, version, status, effective_from, created_by)
SELECT 'DEMO3', 'ตัวอย่างครบ 3 เงื่อนไข (condition 1/2/3)', 1, 'draft', now(), u.id
  FROM users u WHERE u.username = 'admin'
   AND NOT EXISTS (
       SELECT 1 FROM workflow_templates WHERE doc_format_code = 'DEMO3' AND version = 1
   );

INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
SELECT t.id, 'MAKER',    'ผู้จัดทำ',              1, 1 FROM workflow_templates t WHERE t.doc_format_code='DEMO3' AND t.version=1
UNION ALL
SELECT t.id, 'CHECKER',  'ผู้ตรวจสอบ',            2, 2 FROM workflow_templates t WHERE t.doc_format_code='DEMO3' AND t.version=1
UNION ALL
SELECT t.id, 'CUSTOMER', 'ผู้เซ็นภายนอก (ลูกค้า)', 3, 3 FROM workflow_templates t WHERE t.doc_format_code='DEMO3' AND t.version=1;

-- condition_type=3 has no workflow_step_assignees — external signers are created
-- per-document via the external_signers table when the external flow is built.
INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
SELECT s.id, u.id, 1 FROM workflow_steps s
  JOIN workflow_templates t ON t.id = s.workflow_template_id AND t.doc_format_code='DEMO3' AND t.version=1
  JOIN users u ON u.username = 'maker'    WHERE s.position_code='MAKER'
UNION ALL
SELECT s.id, u.id, 1 FROM workflow_steps s
  JOIN workflow_templates t ON t.id = s.workflow_template_id AND t.doc_format_code='DEMO3' AND t.version=1
  JOIN users u ON u.username = 'checkerA' WHERE s.position_code='CHECKER';

COMMIT;
