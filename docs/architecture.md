# Architecture

## System Overview

- **Product boundary:** PaperLess is a standalone e-signature workflow system with its own database and storage. It is the system of record for *signing* (who signed what, when, with what evidence) and the *workflow* (config, tasks, state). SML remains the system of record for the *business document* itself.
- **Main components:** Next.js PWA (web), Go API (`paperless-api`), Go background worker, PostgreSQL, MinIO object storage.
- **External systems:** SML ERP — reached **only** through the existing `sml-api-bybos` HTTP gateway. PaperLess never opens a connection to the SML database.
- **Data stores:** PaperLess PostgreSQL (workflow/tasks/audit/metadata), MinIO (original PDF, final PDF, signature images, thumbnails).

```text
┌──────────────────────────────────────────────────────────────────────┐
│  Customer LAN (on-premise, 192.168.2.x)                                │
│                                                                        │
│   SML PostgreSQL            sml-api-bybos (Go/Gin/pgx) :8200            │
│   192.168.2.248:5432 ◄─pgx─ [+ new paperless endpoints]                │
│                                     ▲                                  │
│                                     │ HTTP REST (X-Api-Key, X-Tenant)  │
│   ┌─────────────────────────────────┴──────────────────────────────┐  │
│   │ PaperLess                                                       │  │
│   │                                                                 │  │
│   │  Next.js PWA ──HTTP──► paperless-api (Gin) ──pgx──► Postgres     │  │
│   │  (mobile sign)            │      ▲                  (paperless)  │  │
│   │                           │      │ enqueue/claim jobs            │  │
│   │                           ├──► MinIO (S3)                        │  │
│   │                           └──► worker (River) ──► sml-api-bybos  │  │
│   └─────────────────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────────────────┘
```

## Component Map

| Component | Responsibility | Key files (planned) |
| --- | --- | --- |
| Frontend (`apps/web`) | PWA inbox, PDF viewer, touch signature capture, admin config, dashboard, external signer page | `apps/web/` |
| Backend API (`apps/api`) | Auth/RBAC, document/workflow/task/attachment/audit APIs, enqueues jobs, serves files via signed access | `apps/api/internal/handlers/` |
| Worker (`workers/`) | Thumbnail gen, final PDF gen, SML sync (confirm/lock), notifications, retry. River jobs, idempotent | `workers/pdf-worker/`, `workers/sync-worker/` |
| Database | Workflow config (versioned), documents, signature tasks/events, external signers, audit (append-only), sync jobs | Postgres + migrations |
| Object storage | Original/final PDFs, signature images, thumbnails | MinIO |
| SML gateway | `sml-api-bybos` extended with import + confirm/lock endpoints | external repo |

## Data Flow

1. **Input:** Phase 1 — admin manual-uploads PDF + metadata. Phase 3 — import pulls doc + metadata from SML via `sml-api-bybos`. Both paths converge on the same import service.
2. **Processing:** Import computes `idempotency_key` + `source_hash`, dedupes, locks the active workflow template version, creates first-sequence signature tasks, sets document `pending`. Signers act on tasks; the workflow engine evaluates step completion (condition 1/2/3) inside a transaction and opens the next sequence.
3. **Storage:** Original PDF → MinIO on import. Signature images → MinIO on sign. Final PDF → MinIO on completion. DB holds only references (object keys + hashes), never binaries.
4. **Output:** On completion, worker generates the final PDF (signature evidence page + legal text + verification code), document becomes `completed` and is immediately downloadable. A separate `sml_sync_job` then attempts Confirm/Lock — independently of document usability.
5. **Audit/observability:** Every state-changing action writes an append-only audit log (actor, action, old/new, ip, user_agent). Worker jobs and SML calls log request/response/status/error (no secrets). Metrics per blueprint §18.

## Failure Boundaries

- **Retryable failures:** SML sync timeout/5xx/network, final PDF gen transient errors, thumbnail gen. → River retry with backoff + max attempts; visible in admin retry UI.
- **Non-retryable failures:** duplicate document (same key+hash), invalid PDF, missing workflow config, signing a non-`open`/unauthorized task. → fail fast with a clear, specific error state; do not retry.
- **Partial success:** Document `completed` but SML `sync_failed` is a valid, visible state — the final PDF is still usable. A reconciliation report (Phase 3) surfaces "completed-but-not-synced" and "SML-confirmed-but-not-synced-here".
- **Idempotency key:** `documents.idempotency_key = doc_format_code + ':' + doc_no + ':' + revision`, plus `source_hash` to distinguish a true revision from a retry. Sign requests carry a client `request_id` to defeat double-tap. Worker jobs are idempotent (re-running produces no duplicate effect).
- **Rollback path:** All migrations are reversible (up/down). A failed deploy rolls back the API/worker image via Compose; DB rollback uses the down migration + restored backup. Document state changes are transactional, so a crashed request leaves no half-applied step.
