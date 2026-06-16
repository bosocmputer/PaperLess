# API Contract

Two surfaces:

1. **PaperLess API** (`apps/api`) — consumed by the Next.js web app.
2. **sml-api-bybos extensions** — new endpoints we add to the existing gateway for SML import + confirm/lock.

## Conventions (shared with sml-api-bybos)

- Response envelope: `{ "success": true, "data": ..., "meta": {...} }` on success; `{ "success": false, "error": { "code": "...", "message": "..." } }` on failure.
- Auth (PaperLess API): `Authorization: Bearer <jwt>`. Tokens are per-user; no shared accounts.
- Auth (sml-api-bybos): `X-Api-Key` + `X-Tenant` headers (existing convention).
- Pagination: `?page=1&size=20`; list responses include `meta.total`.
- Every state-changing endpoint: server re-validates current state from DB (never trusts client), writes an audit log, and is safe against double submit via `request_id`.
- Errors are specific (map to the UI error states in `docs/requirements/`), not generic 500s.

## PaperLess API

### Auth

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/auth/login` | username/password → access + refresh JWT |
| POST | `/api/v1/auth/refresh` | refresh → new access token |
| POST | `/api/v1/auth/logout` | invalidate refresh |
| GET | `/api/v1/auth/me` | current user + roles |

### Workflow Config (workflow_admin)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/workflow-templates` | list, filter by `doc_format_code`, `status` |
| POST | `/api/v1/workflow-templates` | create draft (steps, assignees, condition, sequence) |
| GET | `/api/v1/workflow-templates/:id` | full template with steps |
| POST | `/api/v1/workflow-templates/:id/clone-version` | clone in-use version to a new draft (never mutate in-use) |
| POST | `/api/v1/workflow-templates/:id/publish` | validate + activate; demotes prior active version |

Validation on publish: ≥1 step; unique `sequence_no`; condition 1/2 needs ≥1 internal assignee; condition 3 needs external flow configured.

### Documents

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/documents` | server-side paginate/filter (doc_no, format, status, date, signer) |
| POST | `/api/v1/documents/import` | Phase 1: multipart PDF + metadata. Computes idempotency_key + source_hash; dedupe |
| GET | `/api/v1/documents/:id` | metadata + workflow status + chain |
| GET | `/api/v1/documents/:id/files/original` | permission-checked stream/redirect (signed URL) |
| GET | `/api/v1/documents/:id/files/final` | available once `completed` |
| GET | `/api/v1/documents/:id/audit-logs` | timeline (auditor/admin) |
| GET | `/api/v1/documents/:id/workflow-status` | steps + per-step progress (e.g. 1/2) |
| POST | `/api/v1/documents/:id/cancel` | requires reason; terminal |

Import responses: `200` created; `200` with `duplicate=true` + existing doc ref when same key+hash; `409` revision-conflict when same key but different hash.

### Signature Tasks (signer)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/signature-tasks/inbox` | only `open` tasks assigned to the caller; paginated |
| GET | `/api/v1/signature-tasks/:id` | task + document + viewer data |
| POST | `/api/v1/signature-tasks/:id/sign` | body: signature image, `request_id`, consent. Transactional; evaluates step completion |
| POST | `/api/v1/signature-tasks/:id/reject` | requires reason; returns to defined step |

Sign guards (server-side): task is `open`; document is `pending`; caller is the assignee; signature not empty; condition-1 row lock; idempotent on `request_id`.

### Attachments

| Method | Path | Notes |
| --- | --- | --- |
| POST | `/api/v1/documents/:id/attachments` | type/size validated; to MinIO |
| GET | `/api/v1/documents/:id/attachments` | list metadata |
| DELETE | `/api/v1/attachments/:id` | permission-checked; audited |

### External Signer (no JWT — token-scoped)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/external/:token` | validate token (unused, unexpired); return document to sign |
| POST | `/api/v1/external/:token/otp` | (optional) verify OTP |
| POST | `/api/v1/external/:token/sign` | sign; one-time; consent required |

### SML Sync (document_admin)

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/sml-sync-jobs` | filter by status |
| POST | `/api/v1/sml-sync-jobs/:id/retry` | requires reason; audited |
| GET | `/api/v1/documents/:id/sml-sync-status` | current sync lifecycle |

### Dashboard

| Method | Path | Notes |
| --- | --- | --- |
| GET | `/api/v1/dashboard/summary` | pending / overdue-SLA / completed / rejected / sync_failed, by doc format. Backed by aggregate, not live transaction scan |

## sml-api-bybos Extensions (new, follow existing handler/log/envelope pattern)

Status legend: ✅ buildable now · ⚠️ blocked on SML team (see `docs/sml-integration-notes.md`).

| Method | Path | Purpose | Status |
| --- | --- | --- | --- |
| GET | `/api/v1/ic/doc-formats?screen_code=...` | already exists — source of `doc_format_code` | ✅ exists |
| GET | `/api/v1/paperless/documents/:doc_no` | fetch document metadata (+ source PDF reference) for import | ✅ buildable (read) |
| POST | `/api/v1/paperless/documents/:doc_no/confirm` | mark document Confirm in SML after signing complete | ⚠️ need table/field |
| POST | `/api/v1/paperless/documents/:doc_no/lock` | lock document in SML to prevent edits after signing | ⚠️ need table/field |

The confirm/lock endpoints are the only true blockers; everything else proceeds without them via the mock `SmlDocumentGateway` (Phase 1/2).

## SML Gateway Boundary (PaperLess side)

```text
interface SmlDocumentGateway {
  FetchDocument(tenant, docNo) -> (metadata, pdf, error)   // Phase 3 real; Phase 1 mock/manual
  Confirm(tenant, docNo) -> (result, error)                // Phase 3 real
  Lock(tenant, docNo) -> (result, error)                   // Phase 3 real
}
```

Workflow logic depends only on this interface, never on SML directly. Every call logs request/response/status/error (no secrets). Timeout ⇒ `sync_unknown`/`sync_pending`, never assumed success. Retries use backoff + max attempts.
