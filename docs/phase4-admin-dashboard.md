# Phase 4 — Admin Dashboard (pilot self-service, no SML dependency)

**Goal:** give a pilot admin a real UI to run the system day-to-day **without
`curl`** — find documents, track status, manage external signers, and manage
workflow templates. Every endpoint in this plan reads/writes **only the
PaperLess DB**; nothing here touches SML or `sml-api-bybos`, so it is fully
implementable now and is **not** blocked on `docs/sml-questions.md` (Q1–Q4).

**Read first:** `AGENTS.md`, `docs/current-state.md`, `docs/db-schema.md`,
`docs/domain.md` (roles + workflow rules — the spec), `docs/api-contract.md`,
`docs/testing.md`. **Mirror existing conventions exactly:** zap, request-id,
`httpx` envelope, `strconv.FormatInt` for id→text (NOT `$N::text` — Phase 1
audit bug), table-driven tests, `go test ./...` **with `PAPERLESS_TEST_DB` set**,
server-side pagination (mirror `Inbox`), Next.js 14 App Router + Tailwind
mobile-first, no test runner in the web app (gate = `npm run build` +
`npm run lint`).

**Audit lesson carried (non-negotiable):**

- A skipped test suite is NOT a pass. Every step that touches handlers/DB ships
  with a test that RUNS against a real DB (0 skips = no pass).
- Tests must cover the **HTTP decision point** (role-guard allow/deny, 4xx-vs-500,
  pagination bounds), not just helpers — the security/error decision lives at the
  handler boundary.
- For any FE step, diff the FE's contract (field names, bounds, enum values)
  against what the API actually returns/enforces — don't just confirm it compiles.
- After each step: `go build ./...`, `go vet ./...`, `go test ./...` (real DB),
  `npm run build`, `npm run lint`. Update `docs/current-state.md`.

**Invariants (carried):**

- New migration files only — never edit `0001`–`0006`.
- No SML calls; no `sml-api-bybos` calls; reads/writes are PaperLess DB only.
- Never log tokens, passwords, OTP, or raw signature binary.
- Token stored as SHA-256 hash only; raw token returned once, never re-fetched.
- A `completed`/`rejected`/`cancelled` document is terminal — guards must hold.
- Audit-write every state-changing admin action (resend, cancel, publish,
  deactivate) — mirror the existing `audit_logs` writes.

**Roles (verified in `0002`):** `system_admin`, `workflow_admin`,
`document_admin`, `signer`, `auditor`, `integration`. Dashboard read is for
`document_admin`/`system_admin`/`auditor`; template management is
`workflow_admin`/`system_admin`; signer resend/cancel is
`document_admin`/`system_admin`.

**Schema facts the plan relies on (verified, do not re-derive):**

- `documents` has `ix_documents_search (doc_format_code, doc_no, status, created_at)`
  and `ix_documents_sync (status, sync_status)` — the list/filter query MUST hit
  one of these (confirm with `EXPLAIN`, Phase 3 Step 3c style).
- `workflow_templates` has `UNIQUE (doc_format_code, version)` and a partial
  unique index `uq_workflow_active_per_format ON (doc_format_code) WHERE status='active'`
  — **at most one active version per format**. Publish must respect this; the DB
  enforces it (handle the 23505 cleanly, never a 500).
- `workflow_steps.condition_type ∈ (1,2,3)`; `(workflow_template_id, position_code)`
  unique; `workflow_step_assignees` links users to a step.
- `external_signers.status ∈ ('pending','signed','expired','cancelled')` — no
  other value exists; FE labels must mirror exactly (Phase 3 Step 4 lesson).

---

## Step 1 — Document list / search / filter API  ← do first (the missing core)

Today there is **no `GET /documents`** — an admin must already know a `doc_id` to
open anything. This is the single biggest gap for self-service. Server-side
paginated, filtered, mirrors `Inbox`'s pagination shape.

- `GET /documents` (`document_admin`/`system_admin`/`auditor`). Query params:
  `page` (default 1), `size` (default 20, **cap at 100**), `status` (optional,
  validated against the CHECK enum), `doc_format_code` (optional), `q` (optional
  substring match on `doc_no`), `sync_status` (optional). Return
  `{ data: [...], meta: { page, size, total } }` (mirror `Inbox`).
- Order by `created_at DESC, id DESC` (stable). Use `strconv.FormatInt` for any
  id→text, never `$N::text`.
- Each row: `id, doc_no, doc_format_code, revision, status, sync_status,
  amount, doc_date, created_at` + a cheap per-doc progress hint if it does not
  require an N+1 (e.g. `workflow_version`); do NOT fan out a query per row.
- Validate `status`/`sync_status` against the real CHECK enums → `invalid_request`
  (400), never a silent empty result or a 500 on a bad enum.
- **Done when:** a table-driven test against a real DB (seed ~30 docs across
  statuses/formats) proves: pagination bounds (size cap 100, page math), each
  filter narrows correctly, bad enum → 400, signer role → 403, admin → 200, and
  `EXPLAIN` shows `ix_documents_search` used (record in `testing.md`).

---

## Step 2 — Document detail (admin read) + bounded related lists

The admin detail view needs one coherent payload without the signer-task flow.

- `GET /documents/:id` already exists (`docH.Get`) — confirm it returns enough
  for an admin view (status, format, dates, amount, sync_status,
  workflow_version). If a field the dashboard needs is missing, ADD it to the
  response (don't create a parallel endpoint).
- Reuse existing bounded reads for the detail page: `GET …/workflow-status`,
  `GET …/external-signers`, `GET …/audit-logs`, `GET …/attachments` (all already
  LIMIT-bounded in Phase 3 Step 3c). No new endpoint unless a field is missing.
- **Done when:** the admin detail payload (existing `Get` + the four bounded
  lists) is sufficient for the FE in Step 6, proven by a test asserting the
  shape; no unbounded list is introduced; signer-without-access → the existing
  guard still holds.

---

## Step 3 — External signer resend + cancel (admin recovery)

Phase 3 Step 4 invite is one-shot. A pilot admin needs to **cancel** a stale
invite and **resend** (issue a fresh one-time token) when the signer lost the
link. This is the "minimal dashboard deferred from Step 4" work.

- `POST /documents/:id/external-signers/:signerId/cancel`
  (`document_admin`/`system_admin`): set `external_signers.status='cancelled'`;
  the linked `signature_task` returns to `waiting` (un-link `external_signer_id`,
  status back to `waiting`) so a resend can re-activate it. Audit-write
  `external_signer_cancelled`. Idempotent: cancelling an already-cancelled signer
  → clean 200/409 with a stable code, never a 500.
- `POST /documents/:id/external-signers/:signerId/resend`
  (`document_admin`/`system_admin`): only valid when the signer is `pending`
  (not `signed`/`cancelled`). Generate a NEW 32-byte token, store the new
  SHA-256 hash (overwrite/replace the row's hash + expiry), return the raw token
  **once** (same one-time contract as invite — never re-fetchable, never logged).
  Audit-write `external_signer_resent`.
- A `signed`/`cancelled` signer cannot be resent → clean 409. A
  `completed`/`rejected`/`cancelled` document cannot have signers
  resent/cancelled → clean 409 (terminal-doc guard).
- **Done when:** tests (real DB) prove: cancel returns the task to `waiting`;
  resend on a `pending` signer issues a fresh token and invalidates the old hash
  (old token → 401/410 via the existing external path); resend on
  `signed`/`cancelled` → 409; role-guard allow/deny; raw token never persisted or
  logged (grep the test DB row: only the hash).

---

## Step 4 — Workflow template read API (list + detail)

Backs the template-management UI. Read-only first (write in Step 5) so the UI
can be built and reviewed before any mutation path exists.

- `GET /workflow-templates` (`workflow_admin`/`system_admin`): list templates
  with `id, doc_format_code, name, version, status, effective_from, created_at`.
  Optional `doc_format_code` filter. Bounded (cap 200).
- `GET /workflow-templates/:id`: the template + its ordered steps
  (`sequence_no`, `position_code`, `position_name`, `condition_type`) + each
  step's assignees (`user_id`, `display_order`, username/display_name). No N+1
  per assignee — join or batch.
- **Done when:** a test (real DB, seed POP + DEMO3 from migrations) returns the
  full ordered step/assignee tree for a template; role-guard (signer → 403,
  workflow_admin → 200); bounds respected.

---

## Step 5 — Workflow template lifecycle (clone → edit → publish/deactivate)

The riskiest write path in this plan — it changes which workflow new imports
bind to. Guard the `active` invariant at the DB and handle it cleanly.

- `POST /workflow-templates/:id/clone` (`workflow_admin`/`system_admin`):
  copy a template + steps + assignees into a NEW `version` (next version for
  that `doc_format_code`), `status='draft'`. Returns the new template id.
  Respects `UNIQUE (doc_format_code, version)`.
- `POST /workflow-templates/:id/publish` (`workflow_admin`/`system_admin`):
  flip a `draft` to `active`. The partial unique index
  `uq_workflow_active_per_format` allows only ONE active per format — publishing
  a second active for the same format violates 23505. Handle it: either
  atomically demote the current active to `inactive` in the same tx
  (recommended — "publish replaces active"), or reject with a stable 409 if a
  policy requires explicit deactivation first. **Pick one, document it, test the
  concurrent case** (two publishes racing → exactly one active, no 500).
- `POST /workflow-templates/:id/deactivate`: `active` → `inactive`. A doc format
  with no active template cannot be imported (existing import behavior) — that's
  acceptable and already handled; confirm import returns a clean
  `workflow_config_missing`, not a 500.
- **Do NOT mutate a template that is bound to in-flight documents** in a way that
  changes their workflow — imports bind a `workflow_version` at import time, so
  existing docs are unaffected by definition (verify this is true: documents
  reference `workflow_version`, not the live template). Note the invariant in
  code + plan.
- **Done when:** clone produces an independent draft (editing it doesn't touch
  the source); publish enforces single-active (concurrent publish test → one
  active, mirror `TestCondition1_Race` style, `-race`, `-count=3`); deactivate +
  re-import → clean `workflow_config_missing`; all role-guarded; audit-written.

> **NOTE:** step EDITING (add/remove/reorder steps, change assignees) on a DRAFT
> is in scope only if it fits cleanly; if it balloons, ship clone+publish+
> deactivate first and split step-editing into Step 5b. Decide during
> implementation and record the cut in `current-state.md` — do not half-build it.

---

## Step 6 — Admin dashboard UI (Next.js)

Mobile-first but desktop-usable (admins will use a laptop). New route group; the
existing signer/inbox UI is untouched.

- New route(s) under the app group, e.g. `/admin/documents` (list + filters +
  search + pagination), `/admin/documents/[id]` (admin detail: status, workflow
  progress, external signers with resend/cancel, audit timeline, download
  original/final), and `/admin/workflows` (template list + detail + clone/publish/
  deactivate). Gate every admin route on `getUser().roles` (UI gate) — the API
  enforces `RequireRole` independently (defense in depth).
- Mirror Phase 3 Step 4 token handling: any one-time token (resend) is held in a
  `useRef`, shown once with the "คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก" warning, never
  in React state, never re-fetched.
- Status/enum labels pinned to the real DB CHECK values (grep the migration,
  comment the source) — no invented values (Phase 3 Step 4 lesson).
- Loading / empty / error / disabled states for every list and action (ux-qa):
  empty document list, a publish that 409s (active exists), a resend on a
  non-pending signer, etc. — all recoverable with a clear message.
- **Done when:** an admin can, from the UI alone: find a document by
  doc_no/format/status, open it, see workflow progress + signers + audit,
  resend/cancel an external signer, and clone→publish→deactivate a workflow
  template. `npm run build` + `npm run lint` clean. FE contract diffed against
  the real API (field names, bounds, enum values) — not just compiles.

---

## Out of scope (explicitly deferred)

- Anything requiring SML (confirm/lock/real import/reconciliation) — blocked on
  `sml-questions.md` Q1–Q4; separate future plan.
- Notification adapters (email/LINE/SMS/push) — separate plan; partly gated on
  Q10 + provider creds.
- Bulk operations (multi-select sign/cancel/export) — single-item actions first;
  bulk is a fast-follow once the single-item paths are proven.
- New user/role management UI — roles are seeded; user CRUD is its own plan.
- Coordinate-stamped signatures — evidence page remains default (Q8).

---

## Implementer notes (Sonnet 4.6) — verified against the current code

These are the spots prior audits caught repeatedly. Each cites the real code so
there is no guessing.

1. **Pagination: reuse the existing helpers, do NOT hand-roll the envelope.**
   `Inbox` (`internal/handlers/tasks.go`) is the exact pattern to mirror:
   - clamp with `page < 1 → 1`, `size < 1 || size > 100 → 20` (tasks.go:44–48);
   - `offset := (page-1)*size`;
   - run a separate `COUNT(*)` for `total`;
   - return via **`httpx.List(c, http.StatusOK, items, httpx.Meta{Total, Page, Size})`**
     (tasks.go:108) — there is a dedicated `httpx.List` + `httpx.Meta`; do not
     build `{data, meta}` by hand with `gin.H`. Read `internal/httpx/` first.

2. **Step 2 — `documents.Get` is currently INSUFFICIENT for the dashboard and
   leaks an internal field.** `Get` (documents.go:287–299) scans only
   `id, doc_format_code, doc_no, revision, status, sync_status, idempotency_key`.
   The dashboard needs `amount`, `doc_date`, `workflow_version`, `created_at` —
   ADD them to the struct + SELECT (the columns exist on `documents`). Also
   **remove `idempotency_key` from the response** — it's an internal client dedup
   key, not admin-facing data (it's not sensitive, but it doesn't belong in a
   detail view). Extend the existing `Get`; do NOT add a parallel endpoint.
   `amount` is `numeric(18,2)` and `doc_date` is `date` and both are NULLable —
   scan into `*string`/pointer types (mirror how `sync_status *string` is done)
   so a NULL doesn't blow up the scan.

3. **Step 5 invariant is TRUE — confirm it in a test, don't just assert it.**
   Import binds `workflow_template_id, workflow_version` at import time
   (documents.go:199). So mutating/deactivating a template does not change the
   workflow of already-imported docs (they carry their bound `workflow_version`).
   The publish/deactivate test should seed a doc bound to v1, then publish v2 /
   deactivate v1, and assert the existing doc's `workflow_version` is unchanged
   and its in-flight tasks are untouched.

4. **Publish single-active: let the DB be the source of truth.** The partial
   unique index `uq_workflow_active_per_format` already guarantees one active per
   format. The recommended publish is: in ONE tx, demote the current active to
   `inactive` then set the target to `active`. Under two racing publishes, one tx
   wins and the other hits 23505 — catch it with the existing `isDuplicateKey`
   helper (it's in `internal/workflow/engine.go`, used by Sign/ExternalSign) and
   return a clean 409, never a 500. Test it `TestCondition1_Race`-style
   (`close(start)` barrier, `-race`, `-count=3`).

5. **Enum validation = grep the migration, mirror exactly.** `status` ∈
   (`imported,pending,rejected,completed,cancelled`), `sync_status` ∈
   (`not_required,sync_pending,synced,sync_failed,sync_unknown`),
   `external_signers.status` ∈ (`pending,signed,expired,cancelled`). Validate a
   filter value against the real set → `invalid_request` (400). FE labels must
   pin to these exact values with a comment citing `0001_init.up.sql` — no
   invented keys (the Phase 3 Step 4 `active`-label bug).

6. **Resend token = the invite contract, byte for byte.** Mirror
   `external_signers.go` `Invite`: 32 bytes via `crypto/rand` → `hex.EncodeToString`
   (64-char hex), store **only** the SHA-256 hash, return the raw token once in
   the response, never log it (there's an explicit "raw token intentionally NOT
   logged" comment to preserve). After resend, the OLD hash must be gone so the
   old token fails — overwrite the row's `token_hash` + `token_expires_at` in
   place (don't insert a second signer row).

## Audit Checklist (Opus runs this against the delivered code — real DB, not skipped)

### Build & quality gates

- [ ] `go build ./...`, `go vet ./...` clean; `go test ./...` green **with
      `PAPERLESS_TEST_DB` set** (all new tests RAN, none skipped; `-race` clean,
      `-count=2` stable).
- [ ] `npm run build` + `npm run lint` clean (re-run by the auditor, don't trust
      the delivery's claim).
- [ ] New migration files only (if any); `0001`–`0006` untouched (git diff empty).

### Document list / detail (Steps 1–2)

- [ ] `GET /documents` paginated + filtered; size capped at 100; bad enum → 400;
      signer → 403, admin → 200; `ix_documents_search` used (`EXPLAIN` recorded).
- [ ] No N+1 in the list; admin detail payload sufficient; no unbounded list
      introduced.

### Signer resend / cancel (Step 3)

- [ ] Cancel returns the task to `waiting`; resend issues a fresh token,
      invalidates the old hash (old token → 401/410), only on `pending`;
      `signed`/`cancelled` → 409; terminal-doc → 409; role-guarded; raw token
      never persisted or logged (verified by inspecting the DB row).

### Workflow template lifecycle (Steps 4–5)

- [ ] Read returns the ordered step/assignee tree, no N+1, bounded, role-guarded.
- [ ] Clone produces an independent draft; publish enforces single-active under
      a concurrent race (exactly one active, no 500); deactivate + re-import →
      clean `workflow_config_missing`; in-flight docs unaffected (bound by
      `workflow_version`); every mutation audit-written; role-guarded.

### Dashboard UI (Step 6)

- [ ] All admin actions reachable from the UI; one-time token held in `useRef`,
      shown once, never re-fetched; enum labels pinned to DB CHECK values;
      loading/empty/error/disabled states present; FE contract diffed vs the API.

### Invariants (carried)

- [ ] No SML / `sml-api-bybos` call anywhere in the diff.
- [ ] No applied migration edited; no in-use template mutated in a way that
      changes in-flight documents.
- [ ] No secrets committed; `.env` untracked.
- [ ] Never logs token/password/OTP/signature binary.
