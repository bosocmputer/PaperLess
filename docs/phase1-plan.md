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

## Step 6 — Final PDF (signature-evidence page default)

- On completion, generate the final PDF = original + an appended evidence page (each signer, timestamp, legal e-transaction-act text, verification code/hash). This works without SML signature coordinates.
- Store final PDF in MinIO; document `completed` is downloadable **regardless of SML sync**.
- Run as a worker job (idempotent). Phase 1 may run it inline if the worker isn't built yet, but keep the boundary.
- **Done when:** completing a document yields a downloadable final PDF with evidence + verification code.

## Step 7 — Frontend (Next.js PWA), mobile-first

Only after the API flow above is green.

- Login, inbox, document detail + `pdf.js` viewer (lazy load), signature capture (`signature_pad`, touch/pointer), confirm guard (disabled until viewed + signed), clear-signature confirm, preview before submit.
- Handle empty/loading/error/disabled states (see error list in `docs/requirements/`).
- Test on iOS Safari + Android Chrome.

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
