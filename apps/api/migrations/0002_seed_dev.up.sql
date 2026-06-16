-- 0002_seed_dev.up.sql
-- DEV/UAT seed only. Provides a concrete POP workflow matching the customer
-- Excel example so condition 1/2/3 and the sequence gate can be tested without
-- guessing. Do NOT run this in production.

BEGIN;

-- roles
INSERT INTO roles (code, name) VALUES
    ('system_admin',   'System Admin'),
    ('workflow_admin', 'Workflow Admin'),
    ('document_admin', 'Document Admin'),
    ('signer',         'Signer'),
    ('auditor',        'Auditor'),
    ('integration',    'Integration Service');

-- users (password handling is added with auth; these are signers for the demo)
INSERT INTO users (username, display_name, email) VALUES
    ('admin',  'ผู้ดูแลระบบ',   'admin@example.local'),
    ('maker',  'ผู้จัดทำ',       'maker@example.local'),
    ('checkerA','ผู้ตรวจสอบ A',  'checkerA@example.local'),
    ('checkerB','ผู้ตรวจสอบ B',  'checkerB@example.local'),
    ('approver','ผู้อนุมัติ',     'approver@example.local');

INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id FROM users u, roles r
WHERE (u.username='admin' AND r.code IN ('system_admin','workflow_admin','document_admin'))
   OR (u.username IN ('maker','checkerA','checkerB','approver') AND r.code='signer');

-- POP workflow, version 1, active.
-- Step 1 (seq 1): ผู้จัดทำ  — condition 1 (any-one), assignee: maker
-- Step 2 (seq 2): ผู้ตรวจสอบ — condition 2 (all),    assignees: checkerA, checkerB
-- Step 3 (seq 3): ผู้อนุมัติ  — condition 1 (any-one), assignee: approver
INSERT INTO workflow_templates (doc_format_code, name, version, status, effective_from, created_by)
SELECT 'POP', 'ใบสั่งซื้อ (POP)', 1, 'active', now(), u.id
FROM users u WHERE u.username='admin';

INSERT INTO workflow_steps (workflow_template_id, position_code, position_name, sequence_no, condition_type)
SELECT t.id, 'MAKER',    'ผู้จัดทำ',    1, 1 FROM workflow_templates t WHERE t.doc_format_code='POP' AND t.version=1
UNION ALL
SELECT t.id, 'CHECKER',  'ผู้ตรวจสอบ',  2, 2 FROM workflow_templates t WHERE t.doc_format_code='POP' AND t.version=1
UNION ALL
SELECT t.id, 'APPROVER', 'ผู้อนุมัติ',   3, 1 FROM workflow_templates t WHERE t.doc_format_code='POP' AND t.version=1;

INSERT INTO workflow_step_assignees (workflow_step_id, user_id, display_order)
SELECT s.id, u.id, 1 FROM workflow_steps s
  JOIN workflow_templates t ON t.id=s.workflow_template_id AND t.doc_format_code='POP' AND t.version=1
  JOIN users u ON u.username='maker'    WHERE s.position_code='MAKER'
UNION ALL
SELECT s.id, u.id, 1 FROM workflow_steps s
  JOIN workflow_templates t ON t.id=s.workflow_template_id AND t.doc_format_code='POP' AND t.version=1
  JOIN users u ON u.username='checkerA' WHERE s.position_code='CHECKER'
UNION ALL
SELECT s.id, u.id, 2 FROM workflow_steps s
  JOIN workflow_templates t ON t.id=s.workflow_template_id AND t.doc_format_code='POP' AND t.version=1
  JOIN users u ON u.username='checkerB' WHERE s.position_code='CHECKER'
UNION ALL
SELECT s.id, u.id, 1 FROM workflow_steps s
  JOIN workflow_templates t ON t.id=s.workflow_template_id AND t.doc_format_code='POP' AND t.version=1
  JOIN users u ON u.username='approver' WHERE s.position_code='APPROVER';

COMMIT;
