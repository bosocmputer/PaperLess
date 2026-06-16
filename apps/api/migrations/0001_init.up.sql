-- 0001_init.up.sql
-- Core PaperLess schema. Matches docs/db-schema.md.
-- All tables: bigint identity PK, timestamptz, soft enums via text + CHECK.

BEGIN;

-- ── identity / rbac ────────────────────────────────────────────────────────
CREATE TABLE users (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    username     text NOT NULL UNIQUE,
    display_name text NOT NULL,
    email        text,
    phone        text,
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active','inactive')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE roles (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code text NOT NULL UNIQUE,
    name text NOT NULL
);

CREATE TABLE user_roles (
    user_id bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id bigint NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- ── workflow config (versioned) ────────────────────────────────────────────
CREATE TABLE workflow_templates (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_format_code text NOT NULL,
    name            text NOT NULL,
    version         int  NOT NULL,
    status          text NOT NULL DEFAULT 'draft' CHECK (status IN ('draft','active','inactive')),
    effective_from  timestamptz,
    created_by      bigint REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (doc_format_code, version)
);

-- At most one active version per doc format.
CREATE UNIQUE INDEX uq_workflow_active_per_format
    ON workflow_templates (doc_format_code)
    WHERE status = 'active';

CREATE TABLE workflow_steps (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_template_id bigint NOT NULL REFERENCES workflow_templates(id) ON DELETE CASCADE,
    position_code        text NOT NULL,
    position_name        text NOT NULL,
    sequence_no          int  NOT NULL,
    condition_type       smallint NOT NULL CHECK (condition_type IN (1,2,3)),
    signature_slot       jsonb,
    UNIQUE (workflow_template_id, position_code)
);
CREATE INDEX ix_workflow_steps_seq ON workflow_steps (workflow_template_id, sequence_no);

CREATE TABLE workflow_step_assignees (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    workflow_step_id bigint NOT NULL REFERENCES workflow_steps(id) ON DELETE CASCADE,
    user_id          bigint NOT NULL REFERENCES users(id),
    display_order    smallint,
    UNIQUE (workflow_step_id, user_id)
);

-- ── documents ──────────────────────────────────────────────────────────────
CREATE TABLE documents (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    doc_format_code      text NOT NULL,
    doc_no               text NOT NULL,
    revision             int  NOT NULL DEFAULT 0,
    doc_date             date,
    amount               numeric(18,2),
    source_doc_no        text,
    workflow_template_id bigint REFERENCES workflow_templates(id),
    workflow_version     int  NOT NULL,
    status               text NOT NULL DEFAULT 'imported'
                              CHECK (status IN ('imported','pending','rejected','completed','cancelled')),
    sync_status          text CHECK (sync_status IN ('not_required','sync_pending','synced','sync_failed','sync_unknown')),
    idempotency_key      text NOT NULL UNIQUE,
    source_hash          text NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_documents_search ON documents (doc_format_code, doc_no, status, created_at);
CREATE INDEX ix_documents_sync   ON documents (status, sync_status);

CREATE TABLE document_files (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id         bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    file_type           text NOT NULL CHECK (file_type IN ('original_pdf','final_pdf','attachment','signature_image','thumbnail')),
    object_key          text NOT NULL,
    file_hash           text,
    page_count          int,
    mime_type           text,
    size_bytes          bigint,
    uploaded_by_user_id bigint REFERENCES users(id),
    external_signer_id  bigint,
    created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_document_files_doc ON document_files (document_id, file_type);

-- ── external signers ───────────────────────────────────────────────────────
CREATE TABLE external_signers (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id      bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    name             text NOT NULL,
    email            text,
    phone            text,
    token_hash       text NOT NULL UNIQUE,
    token_expires_at timestamptz NOT NULL,
    otp_verified_at  timestamptz,
    status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','signed','expired','cancelled')),
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_external_signers_doc ON external_signers (document_id, status);

ALTER TABLE document_files
    ADD CONSTRAINT fk_document_files_ext_signer
    FOREIGN KEY (external_signer_id) REFERENCES external_signers(id);

-- ── signature tasks ────────────────────────────────────────────────────────
CREATE TABLE signature_tasks (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id        bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    workflow_step_id   bigint NOT NULL REFERENCES workflow_steps(id),
    assigned_user_id   bigint REFERENCES users(id),
    external_signer_id bigint REFERENCES external_signers(id),
    sequence_no        int  NOT NULL,
    condition_type     smallint NOT NULL CHECK (condition_type IN (1,2,3)),
    status             text NOT NULL DEFAULT 'waiting'
                            CHECK (status IN ('waiting','open','signed','skipped','cancelled','rejected')),
    version            int  NOT NULL DEFAULT 0,
    opened_at          timestamptz,
    completed_at       timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_tasks_inbox ON signature_tasks (assigned_user_id, status, sequence_no, opened_at);
CREATE INDEX ix_tasks_step  ON signature_tasks (document_id, sequence_no, status);

-- ── signature events (append-only) ─────────────────────────────────────────
CREATE TABLE signature_events (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    task_id             bigint NOT NULL REFERENCES signature_tasks(id),
    document_id         bigint NOT NULL REFERENCES documents(id),
    signer_type         text CHECK (signer_type IN ('internal','external')),
    signer_user_id      bigint REFERENCES users(id),
    external_signer_id  bigint REFERENCES external_signers(id),
    signer_name         text NOT NULL,
    action              text NOT NULL CHECK (action IN ('sign','reject','skip')),
    signature_file_id   bigint REFERENCES document_files(id),
    signature_image_hash text,
    comment             text,
    consent_text        text,
    original_pdf_hash   text,
    ip_address          inet,
    user_agent          text,
    session_id          text,
    request_id          text,
    signed_at           timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_signature_events_doc ON signature_events (document_id, signed_at);

-- ── audit log (append-only) ────────────────────────────────────────────────
CREATE TABLE audit_logs (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_type  text CHECK (actor_type IN ('user','system','external')),
    actor_id    text,
    action      text NOT NULL,
    entity_type text NOT NULL CHECK (entity_type IN ('document','task','config','file','sync','external_signer')),
    entity_id   text NOT NULL,
    old_value   jsonb,
    new_value   jsonb,
    reason      text,
    ip_address  inet,
    user_agent  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_audit_entity ON audit_logs (entity_type, entity_id, created_at);

-- ── sml sync jobs ──────────────────────────────────────────────────────────
CREATE TABLE sml_sync_jobs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    document_id     bigint NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    job_type        text NOT NULL CHECK (job_type IN ('update_confirm','update_lock')),
    status          text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','running','succeeded','failed','retry')),
    attempt_count   int  NOT NULL DEFAULT 0,
    max_attempts    int  NOT NULL DEFAULT 5,
    request_payload jsonb,
    response_payload jsonb,
    error_message   text,
    next_retry_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ix_sync_queue ON sml_sync_jobs (status, next_retry_at, attempt_count);

COMMIT;
