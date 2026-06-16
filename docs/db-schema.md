# Database Schema (PaperLess PostgreSQL)

PaperLess owns this database. It is separate from SML. All tables use `bigint` identity PKs, `timestamptz` for time, and soft enums via `text` + `CHECK` (kept simple for migrations).

> This is the schema contract. Migrations must match it and ship with reversible `up`/`down`. Indexes and constraints below are required, not optional — they back the invariants in `docs/domain.md` and the performance budget in `docs/requirements/`.

## Conventions

- Timestamps: `created_at timestamptz NOT NULL DEFAULT now()`, `updated_at` where mutable.
- Money: `numeric(18,2)`.
- Hashes: `text` (hex sha-256).
- Object storage references: `text` object key (e.g. `documents/{id}/original.pdf`) — never store binaries in DB.
- App DB role: `SELECT/INSERT/UPDATE` on most tables; **no `UPDATE`/`DELETE`** on `audit_logs` and `signature_events` (append-only enforced at the role level).

## Tables

### users / roles / user_roles

```text
users(id PK, username UNIQUE, display_name, email, phone, status CHECK in('active','inactive'), created_at, updated_at)
roles(id PK, code UNIQUE, name)               -- system_admin, workflow_admin, document_admin, signer, auditor, integration
user_roles(user_id FK, role_id FK, PRIMARY KEY(user_id, role_id))
```

### workflow_templates

```text
workflow_templates(
  id PK,
  doc_format_code text NOT NULL,
  name text NOT NULL,
  version int NOT NULL,
  status text NOT NULL CHECK in('draft','active','inactive'),
  effective_from timestamptz,
  created_by FK users.id,
  created_at)
```

- `UNIQUE(doc_format_code, version)`
- **Partial unique:** `CREATE UNIQUE INDEX ON workflow_templates(doc_format_code) WHERE status='active';` — at most one active version per doc format.

### workflow_steps

```text
workflow_steps(
  id PK,
  workflow_template_id FK,
  position_code text NOT NULL,
  position_name text NOT NULL,
  sequence_no int NOT NULL,
  condition_type smallint NOT NULL CHECK (condition_type in (1,2,3)),
  signature_slot jsonb)        -- optional page/x/y for future exact placement
```

- `UNIQUE(workflow_template_id, position_code)`
- Index `(workflow_template_id, sequence_no)`

### workflow_step_assignees

```text
workflow_step_assignees(
  id PK,
  workflow_step_id FK,
  user_id FK users.id,
  display_order smallint)       -- 1=User01, 2=User02, 3=User03
```

- `UNIQUE(workflow_step_id, user_id)`

### documents

```text
documents(
  id PK,
  doc_format_code text NOT NULL,
  doc_no text NOT NULL,
  revision int NOT NULL DEFAULT 0,
  doc_date date,
  amount numeric(18,2),
  source_doc_no text,                    -- chain reference from SML
  workflow_template_id FK,
  workflow_version int NOT NULL,          -- locked at import
  status text NOT NULL CHECK in('imported','pending','rejected','completed','cancelled'),
  sync_status text CHECK in('not_required','sync_pending','synced','sync_failed','sync_unknown'),
  idempotency_key text NOT NULL,          -- doc_format_code:doc_no:revision
  source_hash text NOT NULL,              -- hash of PDF + canonical metadata (revision vs retry)
  created_at, updated_at)
```

- `UNIQUE(idempotency_key)`
- Index `(doc_format_code, doc_no, status, created_at)`  — search
- Index `(status, sync_status)` — dashboard/reconciliation

### document_files

```text
document_files(
  id PK,
  document_id FK,
  file_type text NOT NULL CHECK in('original_pdf','final_pdf','attachment','signature_image','thumbnail'),
  object_key text NOT NULL,
  file_hash text,
  page_count int,
  mime_type text,
  size_bytes bigint,
  uploaded_by_user_id FK NULL,
  external_signer_id FK NULL,
  created_at)
```

- Index `(document_id, file_type)`

### signature_tasks

```text
signature_tasks(
  id PK,
  document_id FK,
  workflow_step_id FK,
  assigned_user_id FK users.id NULL,
  external_signer_id FK NULL,
  sequence_no int NOT NULL,
  condition_type smallint NOT NULL CHECK (condition_type in (1,2,3)),
  status text NOT NULL CHECK in('waiting','open','signed','skipped','cancelled','rejected'),
  version int NOT NULL DEFAULT 0,         -- optimistic lock
  opened_at, completed_at, created_at)
```

- Inbox index `(assigned_user_id, status, sequence_no, opened_at)`
- Index `(document_id, sequence_no, status)` — step completion checks
- Guard: at most one non-terminal task per (document, step, signer) — enforce in import logic.

### signature_events  (append-only)

```text
signature_events(
  id PK,
  task_id FK,
  document_id FK,
  signer_type text CHECK in('internal','external'),
  signer_user_id FK NULL,
  external_signer_id FK NULL,
  signer_name text NOT NULL,
  action text NOT NULL CHECK in('sign','reject','skip'),
  signature_file_id FK document_files.id NULL,
  signature_image_hash text,
  comment text,                            -- reject reason / note
  consent_text text,
  original_pdf_hash text,
  ip_address inet,
  user_agent text,
  session_id text,
  request_id text,                          -- double-tap guard
  signed_at timestamptz NOT NULL DEFAULT now())
```

- Index `(document_id, signed_at)`
- App role: no UPDATE/DELETE.

### external_signers

```text
external_signers(
  id PK,
  document_id FK,
  name text NOT NULL,
  email text, phone text,
  token_hash text NOT NULL,                 -- never store raw token
  token_expires_at timestamptz NOT NULL,
  otp_verified_at timestamptz NULL,
  status text NOT NULL CHECK in('pending','signed','expired','cancelled'),
  created_at)
```

- `UNIQUE(token_hash)`
- Index `(document_id, status)`

### audit_logs  (append-only)

```text
audit_logs(
  id PK,
  actor_type text CHECK in('user','system','external'),
  actor_id text,
  action text NOT NULL,
  entity_type text NOT NULL CHECK in('document','task','config','file','sync','external_signer'),
  entity_id text NOT NULL,
  old_value jsonb,
  new_value jsonb,
  reason text,                              -- required for cancel/reprocess/re-sync
  ip_address inet,
  user_agent text,
  created_at timestamptz NOT NULL DEFAULT now())
```

- Index `(entity_type, entity_id, created_at)`
- App role: no UPDATE/DELETE. Plan partition/archive by `created_at` when large.

### sml_sync_jobs

```text
sml_sync_jobs(
  id PK,
  document_id FK,
  job_type text CHECK in('update_confirm','update_lock'),
  status text NOT NULL CHECK in('pending','running','succeeded','failed','retry'),
  attempt_count int NOT NULL DEFAULT 0,
  max_attempts int NOT NULL DEFAULT 5,
  request_payload jsonb,
  response_payload jsonb,
  error_message text,
  next_retry_at timestamptz,
  created_at, updated_at)
```

- Index `(status, next_retry_at, attempt_count)` — sync queue scan
- River job tables live alongside (managed by the River library).

## Invariants enforced by the schema

- One active workflow version per doc format → partial unique index.
- No duplicate import → `UNIQUE(idempotency_key)`; revision-vs-retry → `source_hash` compare in app.
- A task signs once → terminal status + `version` optimistic lock; condition-1 race resolved by row lock in the sign transaction (see `docs/domain.md`).
- Audit/evidence cannot be rewritten via the app → role lacks UPDATE/DELETE.
- No PDF/signature binaries in DB → only `object_key` references.

## Migration discipline

- Every migration is reversible (`up`/`down`) and tested from an empty DB.
- Adding a NOT NULL column to an existing table: add nullable → backfill → set NOT NULL, in separate steps.
- Before a production migration: backup, dry-run on a copy, document rollback command.
