# Phase 1 Work Plan (Backend-first, risk-ordered)

This is the build order for Phase 1 (pilot, **no SML dependency**). SML is mocked
behind `SmlDocumentGateway`; all SML answers only affect Phase 3.

**Read first:** `AGENTS.md`, `docs/domain.md` (workflow rules — the spec), `docs/db-schema.md`, `docs/api-contract.md`. The Go API rail already exists in `apps/api` and compiles (`go build ./...`, `go vet ./...` pass; `/health/ready` works). **Mirror the conventions already in `apps/api` and in the sibling repo `sml-api-bybos`** (zap, request-id, `httpx` envelope, table-driven tests, no Makefile — use `go test ./...`).

Work in this order. Do not start UI before the workflow engine is tested.

---

## Step 1 — Migration runner

- Add `golang-migrate` (library or CLI) so `migrate up` / `migrate down` replace raw `psql`.
- The migration files already exist in `apps/api/migrations` (`0001_init`, `0002_seed_dev`). Keep that naming.
- Wire a `make`-less command path, e.g. `go run ./cmd/migrate up|down`, or document the CLI invocation in `docs/deploy-instances.md`.
- **Done when:** `up` on an empty DB creates all tables; `down` removes them; re-runnable.

## Step 2 — Auth + RBAC

- Local username/password login → JWT (access + refresh). Hash passwords (bcrypt/argon2). Add a `password_hash` column via a new migration (do NOT edit `0001`).
- Middleware: `Auth` (verify JWT) and `RequireRole(...)`.
- Endpoints: `POST /auth/login`, `POST /auth/refresh`, `POST /auth/logout`, `GET /auth/me` (see `docs/api-contract.md`).
- Roles already seeded in `0002`. Seed a known password for the demo `admin`/`maker`/... users (dev only).
- **Done when:** login returns a JWT; a signer cannot call an admin-only route; `go test` covers role-guard allow/deny.

## Step 3 — Workflow engine + tests  ← HIGHEST RISK, DO BEFORE UI

This is the heart of the system. Implement it as pure, testable functions first (no HTTP), then wire endpoints.

Implement (see `docs/domain.md` for exact rules):

- **Step completion evaluation** for condition_type 1 (any-one), 2 (all), 3 (external).
- **Sequence gate:** opening sequence N+1 only when N is complete.
- **condition 1 race:** first signer to commit wins under a row lock (`SELECT ... FOR UPDATE` on the step); other tasks in the step → `skipped` in the same transaction. Late submit → "step already actioned", not an error.
- **Idempotent sign:** same `request_id` twice = one signature_event.

Required tests (`go test ./...`) — these mirror `docs/testing.md` §Must-not-skip:

- condition 1: A signs → B/C `skipped`.
- condition 1 race: A and B concurrent → exactly one wins. (Use a real test DB transaction or a deterministic concurrency test; if a DB is needed, gate behind a `PAPERLESS_TEST_DB` env and skip when absent.)
- condition 2: A signs → step incomplete until B signs (1/2 → 2/2).
- condition 3: expired token → cannot sign.
- sequence: step 2 cannot open while step 1 incomplete.
- reject: requires reason; writes audit; returns to defined step.

- **Done when:** all the above tests pass; engine has no HTTP/Gin imports (keep it a pure package, e.g. `internal/workflow`).

## Step 4 — Document import (manual upload)

- `POST /documents/import`: multipart PDF + metadata.
- Compute `idempotency_key = doc_format_code:doc_no:revision` and `source_hash` (sha-256 of PDF bytes + canonical metadata).
- Dedupe: same key+hash → return existing (`duplicate=true`); same key, different hash → `409` revision conflict.
- Pick the active workflow template for `doc_format_code`, **lock its version** onto the document.
- Store original PDF in MinIO; create first-sequence signature tasks (call the Step 3 engine).
- All in one transaction; write audit.
- **Done when:** importing the POP seed creates the right tasks; retrying does not duplicate; integration test covers it.

## Step 5 — Inbox + Sign + Reject API

- `GET /signature-tasks/inbox`: only `open` tasks assigned to the caller; server-side pagination.
- `GET /signature-tasks/:id`: task + document + viewer data.
- `POST /signature-tasks/:id/sign`: validates (task open, doc pending, caller is assignee, signature not empty, `request_id`); calls the engine; on step/doc completion enqueues final-PDF job.
- `POST /signature-tasks/:id/reject`: requires reason.
- **Done when:** end-to-end POP flow (import → sign each step → completed) works against a test DB.

## Step 5.5 — Attachments + Audit viewer + Workflow status

Small but required by the MVP acceptance criteria — don't skip.

- **Attachments** (`POST/GET /documents/:id/attachments`, `DELETE /attachments/:id`): store in MinIO with type/size validation; record file_type/size/uploader/date in `document_files`. Permission-checked; audited.
- **Audit viewer** (`GET /documents/:id/audit-logs`): timeline of who did what when, for auditor/admin. Reads append-only `audit_logs` + `signature_events`. Never exposes tokens/signature binary.
- **Workflow status** (`GET /documents/:id/workflow-status`): steps + per-step progress (e.g. condition-2 shows 1/2). Backs the signer "ขั้นที่ 2 จาก 3" UI.
- **Done when:** attaching a file, then viewing the document's full audit timeline and workflow status, all work; tests cover permission denial.

## Step 6 — Final PDF (signature-evidence page default)

- On completion, generate the final PDF = original + an appended evidence page (each signer, timestamp, legal e-transaction-act text, verification code/hash). This works without SML signature coordinates.
- Store final PDF in MinIO; document `completed` is downloadable **regardless of SML sync**.
- Run as a worker job (idempotent). Phase 1 may run it inline if the worker isn't built yet, but keep the boundary.
- **Done when:** completing a document yields a downloadable final PDF with evidence + verification code.

## Step 7 — Frontend (Next.js PWA), mobile-first

Only after the API flow above is green.

- Login, inbox, document detail + `pdf.js` viewer (lazy load), signature capture (`signature_pad`, touch/pointer), confirm guard (disabled until viewed + signed), clear-signature confirm, preview before submit.
- Show workflow progress ("ขั้นที่ 2 จาก 3"); for condition 2 show signed count (1/2).
- Implement these explicit error states (from `docs/requirements/paperless-production-features.md`): no pending documents · document already completed · not allowed to sign · waiting for previous approver · signature required · attachment upload failed · SML sync failed · PDF preview failed but download available · workflow config missing · duplicate document · external signer info missing.
- On network drop during submit: show "กำลังตรวจสอบสถานะ" and prevent uncontrolled re-submit (rely on `request_id`).
- Test on iOS Safari + Android Chrome (portrait + landscape; page must not scroll while signing).
- **Done when:** the POP flow is signable end-to-end on a real phone; each error state above is reachable and clear.

---

## Guardrails for whoever builds this

- Never trust frontend state for authorization or completion — re-check from DB inside the transaction.
- Never log tokens, passwords, or raw signature binary.
- Don't edit an applied migration; add a new one.
- Don't mutate an in-use workflow template version; clone a new version.
- A document is usable on `completed`; SML sync is separate and may be mocked.
- Keep SML access behind `SmlDocumentGateway` (mock in Phase 1) — no direct SML calls in workflow code.
- After each step: `go build ./...`, `go vet ./...`, `go test ./...` green before moving on. Update `docs/current-state.md`.

## What's deferred to Phase 3 (waiting on SML answers — see `docs/sml-questions.md`)

- Real `confirm` / `lock` endpoints in `sml-api-bybos` (Q1, Q2).
- Automatic import from SML (Q3, Q4).
- Real sync worker + reconciliation report.
- Document chain rendering depends on the chain field mapping (Q5).

---

## Audit Checklist (used to review this work after it's built)

**Status: AUDITED — PASS (2026-06-16, Opus).** Verified against a real stack
(Docker Postgres + MinIO + API), not compile/skip. The audit found and fixed 6
runtime bugs the unit suite missed (commits `f1cc2dd`, `7d4ae59`, `81c2c65`) —
int→text pgx encode on every audit write, 500s from scanning timestamptz/numeric
into `*string`, external-step templates false-completing, test-cleanup row leak.
See `docs/current-state.md` for the full write-up.

### Build & quality gates

- [x] `go build ./...`, `go vet ./...` clean; `go test ./...` green **with `PAPERLESS_TEST_DB` set** (auth + middleware + workflow all pass — 7/7 workflow tests ran, not skipped).
- [x] `npm run build` (web) green (7 routes generated).
- [x] Migrations: `up` from empty DB creates everything (version 5); `down` removes everything (0 tables left); re-runnable (up→down→up clean).

### Workflow engine (highest risk)

- [x] condition 1: A signs → B/C become `skipped`. (`TestCondition1_AnyOneSigns_OthersBecomeSkipped`)
- [x] condition 1 race: concurrent A/B → exactly one wins; loser sees "step already actioned"; `SELECT … FOR UPDATE OF st` present. (`TestCondition1_Race_ExactlyOneWins`)
- [x] condition 2: step incomplete until all sign; progress count correct. (verified live: held at 1/2 until both checkers signed)
- [x] condition 3: expired/used token cannot sign. (`TestExternalToken_Expired` — engine-level; full external *flow* is Phase 2)
- [x] sequence gate: step N+1 closed until N complete. (`TestSequenceGate…` + live POP run)
- [x] sign is idempotent on `request_id` (no duplicate signature_event). (verified live: double-tap same X-Request-ID → 1 event)
- [x] engine package has no Gin/HTTP imports (pure, testable).

### Import

- [x] retry with same key+hash → no duplicate document. (live: `duplicate=true`)
- [x] same key, different hash → 409 revision conflict. (live: HTTP 409 `revision_conflict`)
- [x] document binds the active workflow version at import (immutable thereafter). (`workflow_template_id` + `workflow_version` stored at INSERT)

### Security & invariants

- [x] non-assignee sign → rejected (403 `not_allowed_to_sign`); admin-only route blocked for signer (audit-logs → 403; no-auth → 401).
- [x] completed/cancelled document cannot be signed again. (live: 409 `document_already_completed`)
- [x] file download permission-checked. (all `/documents` routes behind `RequireAuth`; final PDF also gated on `completed`)
- [x] no token / password / raw signature binary in logs or audit. (grep clean; audit viewer omits token/hash/consent/session)
- [x] audit_logs / signature_events not editable via app. (no UPDATE/DELETE on those tables anywhere; append-only)
- [x] state changes re-validate from DB, not frontend. (`Sign`/`Reject` load+lock the task in-tx; body carries only ids/signature meta)
- [x] no applied migration was edited; changes added as new migrations (0003–0005).
- [x] no in-use workflow version mutated. (no `UPDATE workflow_templates` anywhere)
- [x] `completed` document downloadable independent of SML sync. (`DownloadFinal` checks only `status='completed'`)
- [x] SML access only behind `SmlDocumentGateway` (mock in Phase 1); no direct SML calls in workflow/handlers.

### Pattern consistency

- [x] mirrors `apps/api` + `sml-api-bybos` conventions (zap, request-id, httpx envelope, table-driven tests).
- [x] no secrets committed; `.env` not tracked (`.gitignore` blocks it; none in tree).

### MVP acceptance (from requirements)

- [x] import POP without duplicate on retry.
- [x] config ≥3 steps with condition 1/2/3. (POP = c1/c2/c1 active; `DEMO3` draft template = c1/c2/c3 — added in 0005 so the seed shows all three without breaking the POP happy-path)
- [x] user sees only their tasks. (inbox `WHERE assigned_user_id=$1 AND status='open'`)
- [x] sequence 2 not opened until 1 complete.
- [x] reject with reason works. (reason required at HTTP + engine; audit written)
- [x] final PDF with signatures + legal text + verification code. (live: valid `%PDF`, contains Verification code + พ.ร.บ. 2544 + all 4 signer rows)
- [x] admin can view full audit trail of one document. (live: 5 audit logs + 4 signature events; role-guarded)
- [x] inbox paginates/filters server-side. (`LIMIT/OFFSET`, total in `meta`)

### Known scope boundary (NOT a Phase 1 gap — see `docs/phase2-plan.md`)

- condition_type=3 **external signer flow** (import-creates-external-task, token
  invite, public sign page) is unwired. The engine handles external sign + token
  expiry; the import path does not yet create external tasks. A guard now makes
  the engine error rather than false-complete such a document. This is Phase 2,
  Step 1–5.
