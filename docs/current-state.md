# Current State

Last updated: 2026-06-18 (Engine hardening — condition-1 sibling-skip deadlock (40P01) FIXED via ordered sibling lock; reproduced 189–203/run → 0; regression test proven to fail pre-fix. Prior: Phase 4 Step 6 AUDITED PASS)

## Latest Handoff

- Current production/dev state: **Phase 2 Steps 1–6 implemented.** Phase 1 audited PASS. Phase 2 backend + frontend complete; awaiting Opus audit with real DB.
- Last completed change: Full external signer flow wired end-to-end:
  - **Step 1**: `createTasksForSequence` creates `waiting` task (NULL signer IDs) for condition_type=3 steps. Integration test added. POP unaffected.
  - **Step 2**: `POST /documents/:id/external-signers` (document_admin) generates 32-byte random token, stores SHA-256 hash only, returns raw token once, links + activates the waiting task. `GET /documents/:id/external-signers` lists status (no token/hash).
  - **Step 3**: Public endpoints — `GET /external/document`, `GET /external/document/file/original`, `POST /external/sign` — all token-in-header (X-Signer-Token), not URL. Per-IP rate limiter (20 req/min). Used/expired/tampered → stable error code, no 500.
  - **Step 4**: `writeExternalSignatureEvent` writes `signer_type='external'` events. `FinalizeDocument` scans `signer_type` and evidence page shows it in the signer table.
  - **Step 5**: `/external/[token]` page — no app shell, no login. Token read from path once into a ref, sent as X-Signer-Token header on every API call. States: loading → view (PDF blob URL via fetch+header) → signing (SignaturePad) → preview+consent → submitting → done. Network-drop state shows "กำลังตรวจสอบสถานะ". All 4 external error states wired.
  - **Step 6**: 4 external error codes added to ErrorState; `ExternalDocView` interface added to api.ts; `externalRequest` helper keeps token out of URLs.
- Current branch/release: `main`
- **Phase 2 audit: PASS (2026-06-17, Opus).** Verified against a real throwaway Postgres (port 54331) + real migrations + the full suite run for real (not skipped). Drove the external flow live via httptest against the DB. No blocker bugs found. The audit ADDED the regression coverage the delivery was missing for the plan's highest-risk items (see below). Details in the dated section.
- Known broken or risky areas: Manual QA on real iOS Safari + Android Chrome still not done (Step 6 requirement — cannot be automated). Inline `FinalizeDocument` on external sign can panic if storage is down (gin.Recovery → 500) *after* the sign already committed; doc is still completed so this is a cosmetic 500, not data loss — worth a defensive guard later.
- Non-blocking nits: ~~`GET /documents/:id/external-signers` (List) has no role guard~~ — **CLOSED in Phase 3 Step 2a** (now `document_admin`/`system_admin`/`auditor`). `external_signer_info_missing` error state is defined but only reachable from the admin invite flow, not the external page.

## Runtime Snapshot

- Local path: `/Users/nontawatwongnuk/dev_bos/paperless`
- Server/deploy path: TBD (on-prem, same LAN as `sml-api-bybos` @ `192.168.2.109`)
- Public URL: none (on-prem LAN only)
- Ports: web `3000`, api `8080`, postgres `5432`, MinIO `9000/9001`
- Containers/services: `web`, `api`, `postgres`, `minio` (worker deferred to Phase 2)

## What was built (Phase 1)

| Step | What | Gate |
| --- | --- | --- |
| 1 | `golang-migrate` runner (`go run ./cmd/migrate up/down/version/force`) | `go build` ✅ |
| 2 | Auth + RBAC: JWT login/refresh/logout/me, bcrypt passwords, `RequireAuth`/`RequireRole` middleware | `go test ./internal/auth ./internal/middleware` ✅ |
| 3 | Workflow engine: condition 1/2/3, sequence gate, race lock, idempotent sign, reject | `go test ./internal/workflow` ✅ (DB tests gate on `PAPERLESS_TEST_DB`) |
| 4 | Document import: idempotency_key + source_hash dedup, active template binding, MinIO PDF store | `go build` ✅ |
| 5 | Inbox + Sign + Reject API: server-side paginated inbox, `POST .../sign` (request_id idempotency, race protection), `POST .../reject` (reason required) | `go build` ✅ |
| 5.5 | Attachments (POST/GET/DELETE), audit-log viewer, workflow-status endpoint | `go build` ✅ |
| 6 | Final PDF: evidence page (gofpdf, signer table + legal text + verification code), stored in MinIO, downloadable on `completed` independent of SML sync | `go build` ✅ |
| 7 | Next.js 14 PWA (mobile-first, Tailwind): login, inbox (paginated), document detail + pdf.js viewer, SignaturePad (touch-safe), preview-before-submit, WorkflowProgress, 13 error states | `npm run build` ✅ |

## Active Work

- Goal: Pilot readiness — Admin Dashboard (Phase 4) without waiting on SML.
- In progress: Phase 4 Step 6 **AUDITED PASS (2026-06-18, Opus)** — Admin Dashboard UI. Audit fixed 2 real defects (SSR 500 + iOS-Safari date); see dated section.
- Blocked: real SML confirm/lock sync (separate SML track, still blocked on `sml-questions.md` Q1–Q4).
- Next: Phase 4 complete pending Step 5b device QA (human-run, iOS Safari + Android Chrome). Pilot readiness review.
- ~~Known latent issue: condition-1 sibling-skip deadlock (40P01)~~ — **FIXED 2026-06-18** (engine hardening, see dated section below). Sign now takes an ordered sibling lock for condition_type=1; deadlock reproduced (189–203 per run) then driven to 0; regression test `TestCondition1_NoDeadlock_ManySiblings` proven to fail pre-fix.

## Engine hardening — condition-1 sibling-skip deadlock (2026-06-18) — FIXED

**Root cause (reproduced, not assumed).** `engine.Sign` for `condition_type=1`
(any-one-signs) marks every sibling task in the same step `skipped`. The original
flow: each tx first locked ONLY its own task row (`SELECT … FROM signature_tasks st
… WHERE st.id=$1 FOR UPDATE OF st`), then the winner's sibling-skip
`UPDATE … WHERE id != $3` locked the remaining sibling rows in physical (ctid)
order while the losers each still held their own task lock. Under concurrent
signing this is a lock-order inversion → Postgres aborts the cycle with **SQLSTATE
40P01 (deadlock_detected)**, leaking a 5xx-class error to a losing signer instead
of a clean `ErrStepAlreadyActioned`. **Correctness was never violated** (exactly
one task ends up `signed`), but the loser got a deadlock error and the DB spent
time detecting+aborting+retrying.

**Reproduced live** on a throwaway Postgres 14: a fan-out probe (8 signers onto
sibling condition-1 tasks of one step, released via a `close(start)` barrier,
30 rounds) produced **189–203 deadlock (40P01) errors per run** while `signed=1`
held every round.

**Fix (ordered lock).** For `condition_type=1`, the FIRST lock the tx takes is now
the WHOLE sibling set, acquired in deterministic ascending-id order
(`SELECT id FROM signature_tasks WHERE document_id=$1 AND sequence_no=$2 ORDER BY
id FOR UPDATE`) — no single-row lock is taken before it. Every concurrent signer
therefore queues on the same lowest-id row first, so no lock cycle can form. After
acquiring the set, the tx re-reads its own task status (a concurrent winner may
have signed/skipped it while queued) and returns `ErrStepAlreadyActioned` if so.
`condition_type=2/3` keep the original single-row `FOR UPDATE` (no sibling-skip →
no inversion possible). The initial load query no longer locks (it only reads
condition_type/sequence_no to pick the lock strategy) and dropped the unused
`JOIN documents` (doc status is read separately right after).

**Verified.** After the fix the same probe → **0 deadlocks** across 90 rounds ×
fan-out 8 (720 concurrent signs), `signed=1` every round, and ~350× faster
(0.46s vs 160s — no deadlock detect/abort cycles). Permanent regression test
`TestCondition1_NoDeadlock_ManySiblings` (in-tree, named normally): asserts (a)
exactly 1 signed per round and (b) NO signer ever receives a 40P01 — every loser
gets `ErrStepAlreadyActioned` or nil. **Proven real:** reverted to the buggy
single-row lock → test FAILS with `skip sibling tasks: deadlock detected (40P01)`
every round; restored fix → PASS. Full module suite green with `-race -count=2`,
0 skips in the workflow package; the pre-existing `TestCondition1_Race_ExactlyOneWins`
still passes.

Files: `apps/api/internal/workflow/engine.go` (Sign lock acquisition restructured),
`apps/api/internal/workflow/engine_test.go` (regression test + `strings` import).

> Note: `types.go` carries an unrelated in-flight change (`SignInput.ExternalSignerName`,
> used by `engine.go:546`/`external_sign.go:338`) that predates this work — left
> untouched.

---

## Phase 4 Step 6 — Admin Dashboard UI (2026-06-18) — AUDITED PASS (Opus)

**Audit verdict: PASS after fixing 2 real defects.** Gates re-run by the auditor
(`npm run build` + `npm run lint` clean, not trusted from the delivery). The FE
contract was diffed field-by-field against the actual handler response shapes
(not "it compiles") and two endpoints driven server-side on a running production
build. **The audit found and fixed two defects the delivery's build gate could
not catch:**

1. **SSR `sessionStorage` 500 on the dynamic detail route (real, reproduced
   live).** `/admin/documents/[id]` called `getUser()` at the top-level component
   body (line 102) AND `getAccessToken()` in the render path (line 231) — both
   touch `sessionStorage`, which is undefined during request-time SSR. The
   delivery fixed the same bug in `/admin/workflows` but missed it here. Why the
   build passed: **dynamic routes (`ƒ`) skip static prerender at build time**, so
   the SSR access never fires during `next build` — it only 500s per-request.
   Proven live: `curl /admin/documents/123` → **HTTP 500**, server log
   `ReferenceError: sessionStorage is not defined`. (The pre-existing signer page
   `/documents/[id]` has the identical latent 500 — out of Step 6 scope, noted
   below.) **Fix:** moved `userRoles` to state set inside `load()`, and guarded
   `token` with `typeof window === "undefined"`. Re-proven live: now **HTTP 200**,
   SSR renders the loading spinner shell + ships client chunks → clean hydration,
   zero server errors. Static routes (`/admin/documents`, `/admin/workflows`) were
   already safe — they prerender to a client-only shell and only call
   `sessionStorage` inside `useCallback`.

2. **iOS-Safari-unparseable date in the signers list (real, format-verified).**
   The detail page rendered the external-signer `expires_at` via
   `new Date(s.expires_at)`. That field comes from `GET .../external-signers`,
   which emits Postgres `::text` (`2026-06-18 15:53:37.571+00` — **space**
   separator + **short `+00`** offset). iOS Safari's JavaScriptCore rejects that
   as `Invalid Date` (the resend/invite path uses RFC3339, which is why the
   existing invite-success render never hit it). Since Step 5b mandates iOS Safari
   QA, this would have surfaced as a broken date on real devices. **Fix:** added a
   `formatDateTime()` helper that normalizes space→`T` and pads `+00`→`+00:00`
   before `new Date()`, with a raw-string fallback on parse failure. Verified
   against all real format variants (fractional/no-fractional `::text`, RFC3339
   `Z`/offset, empty, garbage) — all render correctly.

**Confirmed-consistent (not findings):** cancel button shows for
`pending`/`expired` (Cancel handler accepts both — `expired` is cancellable);
resend button shows for `pending` only (Resend 409s on `signed`/`cancelled`/
`expired`); resend response shape `{external_signer_id,name,expires_at,token}`
matches the handler byte-for-byte; List signer shape (`id,name,email,phone,
status,expires_at,otp_verified,created_at`) matches; template detail
`steps[].assignees` is `[]` not null for condition_type=3; one-time resend token
held in `useRef` (never state, never re-fetched), shown once with the
"คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก" warning; all enum labels pinned to the real DB
CHECK values with source comments; **re-fetches status after every mutating
action** (publish/deactivate/resend/cancel) — does NOT trust the action response,
which is the correct mitigation for the Step 5 misleading-200 wart.

**Carry-forward (pre-existing, NOT Step 6):** the existing signer page
`/documents/[id]` 500s on SSR for the same `sessionStorage`-in-render reason and
recovers via client hydration (it ships the client bundle in the 500 HTML). It
has shipped this way since Phase 1 and was repeatedly audited PASS — the app
works because the browser hydrates over the 500. A later hardening pass should
apply the same `typeof window` guard there for a clean SSR 200.

**Gates (auditor-run):** `npm run build` ✅ clean (9 routes), `npm run lint` ✅
clean. Dynamic detail route driven live on a production build: 500 (before fix) →
200 (after fix), verified by HTTP status + server log.

---

### Phase 4 Step 6 — as delivered (Sonnet 4.6)

**Three new admin routes** (all gated on `getUser().roles` at the UI level; API
enforces `RequireRole` independently — defense in depth):

- **`/admin/documents`** (`document_admin`/`system_admin`/`auditor`): paginated
  document list with live filter by status, doc_format_code, and debounced
  substring search on doc_no (`q`). Taps `GET /documents` (Phase 4 Step 1).
  Status/enum labels pinned to `documents.status` CHECK
  (`imported,pending,rejected,completed,cancelled`). Loading / empty / error /
  disabled states present. Links to `/admin/documents/[id]` and the workflows
  page.

- **`/admin/documents/[id]`** (`document_admin`/`system_admin`/`auditor`): full
  admin detail — metadata dl grid (format/doc_no/date/amount/workflow_version/
  sync_status), download original PDF + download final PDF (completed docs only),
  `WorkflowProgress` component, external signers list with per-signer
  **resend** (pending only) and **cancel** (pending/expired) actions, combined
  audit timeline (signature_events + audit_logs merged chronologically).
  Resend token handling mirrors Phase 3 Step 4 contract exactly: raw token stored
  in `useRef`, displayed once with "คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก" warning,
  never in React state, never re-fetched. **Re-fetches signer status after
  resend/cancel** (not trusting the action response directly — mitigation for the
  known publish/resend misleading-200 wart pattern). Action buttons disabled for
  `document_admin`/`system_admin` only (auditor can view but not act).

- **`/admin/workflows`** (`workflow_admin`/`system_admin`): template list with
  optional doc_format_code filter (cap 200 rows from API). Each row expandable
  to show full step tree (step position, sequence_no, condition_type label:
  คนใดคนหนึ่ง / ทุกคน / ภายนอก, assignees). Per-template action buttons:
  **โคลน** (any status), **เผยแพร่** (draft only), **ปิดใช้งาน** (active only).
  **Always re-fetches template list after an action** — does not trust the action
  response status directly (mitigation for the misleading-200 wart documented in
  Step 5 audit). Status labels pinned to `workflow_templates.status`
  (`draft,active,inactive`). Loading/empty/error/disabled states present. Action
  feedback bar shows success or error message with dismiss.

**FE contract diffed against real API:**
- `GET /documents`: `id` is string (FormatInt), nullable `sync_status`/`amount`/`doc_date`, `workflow_version` int — all matched.
- `GET /documents/:id`: same shape as list row — confirmed.
- `GET /documents/:id/audit-logs`: `{ audit_logs: [...], signature_events: [...] }` — matched.
- `GET /workflow-templates`: bounded 200, optional `doc_format_code` filter, `effective_from` is `""` when null (COALESCE) — matched with `omitempty`.
- `GET /workflow-templates/:id`: `steps[].assignees` is `[]` not null for condition_type=3 — matched.
- Resend: `POST .../resend` → `{ external_signer_id, name, expires_at, token }` — matched.
- Cancel: `POST .../cancel` → no body needed, 200 on success — matched.
- Lifecycle: clone `201`, publish/deactivate `200` — all matched.

**`getUser()` sessionStorage guard:** called inside `useCallback`/`useEffect` only
(never at module level during SSR). `sessionStorage is not defined` prerender
error caught and fixed during build.

**Gates:** `npm run build` ✅ clean (9 routes, 0 errors), `npm run lint` ✅ clean.

**Files:**
- `apps/web/src/lib/api.ts` — added `DocumentRow`, `DocumentDetail`, `AuditEntry`, `SigEvent`, `TemplateRow`, `TemplateAssignee`, `TemplateStep`, `TemplateDetail`, `ResendResponse` interfaces; `cancelSigner`, `resendSigner`, `listDocuments`, `getDocumentDetail`, `getAuditLogs`, `listTemplates`, `getTemplate`, `cloneTemplate`, `publishTemplate`, `deactivateTemplate` methods.
- `apps/web/src/app/(app)/admin/documents/page.tsx` (new)
- `apps/web/src/app/(app)/admin/documents/[id]/page.tsx` (new)
- `apps/web/src/app/(app)/admin/workflows/page.tsx` (new)

---

## Phase 4 Step 5 — Workflow template lifecycle (2026-06-18) — AUDITED PASS (Opus)

**Audit verdict: PASS, no code bug.** Audited against a real Postgres with live
httptest probes (build/vet/suite all re-run by the auditor, not trusted from the
delivery). Migrations 0001–0006 untouched; no SML/`sml-api-bybos` call in the diff;
log statements are `zap.Error(err)` only (no doc/user/secret fields — and template
lifecycle carries no token/password anyway). What the audit verified that the
delivery's tests could not guarantee on their own:

1. **Harder publish race than the delivery test.** Delivery raced two drafts with NO
   existing active. The audit raced an **active v1 + two drafts (v2,v3)** — both
   publishers must demote the SAME v1 row, the deadlock-prone path. Result across 5
   runs: **exactly 1 active always, zero 5xx, no deadlock** (one publisher 200, the
   other 409 `conflict_active_exists` — or both 200, see the wart below).
2. **Clone independence proven to jsonb depth.** Delivery checked step/assignee
   counts. The audit cloned a mixed template (c1 with `signature_slot` jsonb + 1
   assignee, c2 with 2 assignees, **c3 external with 0 assignees**), confirmed the
   jsonb slot copied verbatim (`{"x":100,"y":200,"page":1}`), then **mutated the
   source** (deleted a step, renamed another) and confirmed the clone is fully
   independent — no bleed.
3. **In-flight invariant through the REAL engine.** Delivery hand-seeded a single
   task. The audit drove `workflow.OpenFirstSequence` to build a real task tree bound
   to v1, then published v2 (demoting v1) + deactivated v2, and confirmed the doc's
   `workflow_template_id`+`workflow_version` and every task's `status`/`step_id` are
   untouched.
4. **Role guard through REAL JWT** (not just `fakeAuth`): `IssueAccessToken` +
   `RequireAuth` parse a genuine signed token — workflow_admin/system_admin→201,
   signer/document_admin→403, no-token→401, garbage-token→401.
5. **Idempotent paths do NOT duplicate audit rows.** Delivery only checked "an audit
   row exists." The audit re-published an active template 5× and re-deactivated an
   inactive 5× → **exactly 1 audit row each** (the `tx.Rollback()` before the audit
   insert in the idempotent branch holds).

**Non-blocker finding (logged, NOT fixed — user decision 2026-06-18):**
**publish can return a misleading 200.** When two publishes for the same format
*serialize* (no lock overlap — one fully commits before the other reads), BOTH callers
get `200 {"status":"active"}`, but the earlier committer is immediately demoted to
`inactive` by the later one. **The `active=1` invariant is never violated and there is
no data corruption** — only the earlier caller's 200 response no longer reflects DB
state. Proven live (v2 resp=200 active / DB=active; v3 resp=200 active / DB=inactive).
Probability is negligible for a single-admin button. Mirrors the Step 3 resend
losing-caller wart. **Mitigation:** the Step 6 UI must re-fetch template status after
a publish/deactivate action rather than trusting the action's response body.

**Confirmed-consistent (not a finding):** `entity_type='config'` for the three audit
writes — `config` is in the `audit_logs_entity_type_check` CHECK set
(`document,task,config,file,sync,external_signer`) and is the correct semantic fit
for workflow-template (configuration) mutations; Step 5 is the first user of `config`.

**Gates (auditor-run):** `go build`/`go vet` clean; handlers suite `-race -count=2`
green, 28 lifecycle sub-tests, **0 skips**; pre-existing `TestCondition1_Race`
engine deadlock (SQLSTATE 40P01, separate hardening task) unchanged. Probe files and
probe DB rows removed after the run.

---

### Phase 4 Step 5 — as delivered (Sonnet 4.6)

**Implemented:** three new write endpoints behind `RequireRole("workflow_admin","system_admin")`:

- `POST /workflow-templates/:id/clone` — copies a template + all steps + all assignees
  into a new `draft` with `version = MAX(version)+1` for the same `doc_format_code`.
  All rows are read into memory (single LEFT JOIN across steps+assignees) before any
  writes, avoiding pgx "conn busy" on a single tx connection. Returns new template id,
  doc_format_code, name, version, status. Audit-writes `workflow_template_cloned`
  (entity_type=`config`). Clone of a nonexistent template → 404. Bad id → 400.

- `POST /workflow-templates/:id/publish` — flips a `draft` → `active`. In the same
  tx, atomically demotes any existing `active` for the same `doc_format_code` to
  `inactive` BEFORE setting the target to `active`. Under concurrent publishes the
  partial unique index `uq_workflow_active_per_format` guarantees at most one active
  per format — the loser gets SQLSTATE 23505 mapped to 409 `conflict_active_exists`,
  never a 500. Already-active → idempotent 200. Inactive → 409
  `template_not_publishable`. Audit-writes `workflow_template_published`.

- `POST /workflow-templates/:id/deactivate` — flips an `active` → `inactive`.
  Already-inactive → idempotent 200. Draft → 409 `template_not_deactivatable`.
  After deactivate: zero active for that format, so a new import returns
  `workflow_config_missing` (the exact query import uses returns no rows — verified
  by test). Audit-writes `workflow_template_deactivated`.

**Invariant confirmed:** existing documents bind `workflow_template_id` + `workflow_version`
at import time — publishing/deactivating a template does NOT change the `workflow_version`
of already-imported docs or touch their in-flight `signature_tasks`. Proven by test.

**Bugs found and fixed during development:**

1. **`conn busy` in Clone** — initial implementation queried assignees via `aRows`
   inside `aRows.Next()` and then called `tx.Exec` for each INSERT, hitting pgx's
   single-connection-per-tx limit. Fix: single LEFT JOIN reads steps+assignees into
   memory, all rows closed before any writes.
2. **`audit_logs_entity_type_check` violation** — used `entity_type='workflow_template'`
   which is not in the CHECK constraint
   (`'document','task','config','file','sync','external_signer'`). Fix: use `'config'`
   (correct semantic fit for template lifecycle mutations).

**Tests (real DB, `-race -count=2` green, 0 skips):**

| Test | What it proves |
| --- | --- |
| `TestWorkflowLifecycle_RoleGuard` | document_admin/auditor/signer → 403 on clone/publish/deactivate; workflow_admin/system_admin → not 403 |
| `TestWorkflowLifecycle_Clone_ProducesIndependentDraft` | new draft id≠source; status=draft; version=2; step/assignee counts match; rows are independent (no shared step ids); audit written |
| `TestWorkflowLifecycle_Clone_NotFound` | 404 on nonexistent id |
| `TestWorkflowLifecycle_Clone_BadID` | 400 on non-integer id |
| `TestWorkflowLifecycle_Publish_DraftBecomesActive` | status→active; audit written |
| `TestWorkflowLifecycle_Publish_DemotesExistingActive` | v1 demoted to inactive; v2 becomes active; exactly 1 active |
| `TestWorkflowLifecycle_Publish_IdempotentIfAlreadyActive` | double-publish → 200 both times; status stays active |
| `TestWorkflowLifecycle_Publish_InactiveTemplateFails` | inactive → 409 `template_not_publishable` |
| `TestWorkflowLifecycle_Publish_ConcurrentRace` | 3 runs × 2 racing drafts for same format → exactly 1 active; no 5xx; at least 1 winner (loser → 409 `conflict_active_exists`) |
| `TestWorkflowLifecycle_Deactivate_ActiveBecomesInactive` | status→inactive; audit written |
| `TestWorkflowLifecycle_Deactivate_IdempotentIfAlreadyInactive` | double-deactivate → 200 both times |
| `TestWorkflowLifecycle_Deactivate_DraftFails` | draft → 409 `template_not_deactivatable` |
| `TestWorkflowLifecycle_Deactivate_ReimportReturnsWorkflowConfigMissing` | after deactivate: 0 active for format; exact import query returns ErrNoRows |
| `TestWorkflowLifecycle_InFlightDocsUnaffected` | publish v2 + demote v1 → existing doc `workflow_version` unchanged; in-flight task status unchanged |

**Gates:** `go build ./...`, `go vet ./...` clean; handlers suite `-race -count=2` green,
0 skips. Pre-existing `TestCondition1_Race_ExactlyOneWins` deadlock (engine pkg,
SQLSTATE 40P01, separate hardening task) unchanged.

Files: `apps/api/internal/handlers/workflow_templates_lifecycle.go` (new, 3 handlers),
`apps/api/internal/handlers/workflow_templates_lifecycle_test.go` (new, 14 tests),
`apps/api/cmd/server/main.go` (3 new routes).

---

## Phase 4 Step 4 — Workflow template read API (2026-06-17) — AUDITED PASS (Opus)

**Audit verdict: PASS, no code bug.** Clean delivery. Audited against real
Postgres and MinIO, including booting the real server and driving both endpoints
end-to-end through actual `/auth/login` JWT (not just `fakeAuth`). What the audit
verified that the delivery's tests couldn't guarantee on their own:

1. **Bound truncation is REAL, not a tautology.** The delivered bound test seeded 205
   rows but only asserted `len ≤ 200` — which passes even if the LIMIT never fires.
   Re-proved: DB had only 4 templates pre-seed; seeded 250 with a `ZZ`-prefix
   `doc_format_code` (sorts to the tail of `ORDER BY doc_format_code`), then asserted
   the response is **exactly 200** AND the alphabetically-last seeded row is **absent**
   — truncation genuinely cut rows.
2. **No N+1 confirmed at scale.** A 20-step/80-assignee template fetched in 2.4ms via
   the single LEFT JOIN — constant 2 queries (header + join) regardless of size.
3. **Role guard verified through real JWT.** admin(workflow_admin)→200,
   maker(plain signer)→403 `insufficient role`, no-auth→401, POP detail returns the
   correct ordered 3-step/4-assignee tree, 404/400 correct. The handler has no
   internal auth — it relies entirely on route-level `RequireRole`; `fakeAuth` bypasses
   JWT parsing, so the real-server check matters.

**Confirmed-consistent (not a finding):** timestamps render as Postgres `::text`
(`2026-06-16 15:53:37.571082+00`, not RFC3339) — matches every other read endpoint
(`attachments`/`audit`/`documents`/`tasks`/`external-signers` List); the Step 6 FE
date formatter must parse this format (which it already does for every other list).
condition_type=3 external steps correctly return `assignees: []` (proven against the
real DEMO3 seed).

**Gates:** `go build`/`go vet` clean; Step 4 tests `-count=2 -race` green, 0 skips;
full suite green; live end-to-end through the HTTP+JWT stack.

---

### Phase 4 Step 4 — as delivered (Sonnet 4.6)

**Implemented:** two new read-only endpoints behind
`RequireRole("workflow_admin","system_admin")`:

- `GET /workflow-templates` — lists all templates with optional `doc_format_code`
  filter. Bounded at 200 rows. Ordered `doc_format_code, version` for stable
  results. Each row: `id` (string via `FormatInt`), `doc_format_code`, `name`,
  `version`, `status`, `effective_from` (omitted when null), `created_at`.
  Unknown format → empty `[]`, never 404/500.

- `GET /workflow-templates/:id` — full template detail with ordered steps +
  assignees. Uses a single LEFT JOIN query (no N+1):
  `workflow_steps LEFT JOIN workflow_step_assignees LEFT JOIN users`. Steps ordered
  by `sequence_no`; assignees within each step ordered by `display_order, id`.
  Condition_type=3 (external-signer) steps have no assignees — returns `[]` not
  null. Grouped in Go using a `stepMap[stepID]` + `stepOrder []int64` pattern to
  preserve sequence. Bad id → 400 `invalid_id`; missing id → 404 `not_found`.

**Tests (real DB, `-count=2 -race` green, 0 skips):**

| Test | What it proves |
| --- | --- |
| `TestWorkflowTemplate_RoleGuard` | document_admin/auditor/signer → 403; workflow_admin/system_admin → 200 |
| `TestWorkflowTemplate_List_ReturnsPOPAndDEMO3` | seeded templates appear; id is string; required fields present |
| `TestWorkflowTemplate_List_DocFormatFilter` | filter narrows to matching format; unknown format → empty `[]` |
| `TestWorkflowTemplate_List_EmptyIsArray` | empty result is `[]` not `null` |
| `TestWorkflowTemplate_Get_POPStepTree` | POP: 3 steps in order (MAKER/CHECKER/APPROVER), correct condition_types, correct assignees with display_order |
| `TestWorkflowTemplate_Get_DEMO3ExternalStep` | DEMO3: condition_type=3 CUSTOMER step has `assignees: []` (not missing) |
| `TestWorkflowTemplate_Get_NotFound` | 404 `not_found` on nonexistent id |
| `TestWorkflowTemplate_Get_BadID` | 400 `invalid_id` on non-integer id |
| `TestWorkflowTemplate_Get_BoundedAssignees` | freshly-seeded 2-step template returns correct step/assignee structure |
| `TestWorkflowTemplate_List_Bounded` | seeding 205 templates → list returns ≤200 rows |

**Gates:** `go build ./...`, `go vet ./...` clean; full `-count=2 -race` suite green
(all packages), 0 skips.

Files: `apps/api/internal/handlers/workflow_templates.go` (new),
`apps/api/internal/handlers/workflow_templates_test.go` (new, 9 tests),
`apps/api/cmd/server/main.go` (2 new routes).

---

## Phase 4 Step 3 — External signer cancel + resend (2026-06-17) — AUDITED PASS (Opus)

**Audit verdict: PASS, no code bug.** This was a clean delivery — the 12 delivered
tests genuinely cover the security boundary (unlike prior steps). Audited against a
real Postgres with live probes. Two things added by the audit:

1. **Plan wording corrected (not a code bug).** The plan said cancel un-links the
   task "so a *resend* can re-activate it" — but resend requires `status='pending'`
   and cancel sets `cancelled`, so cancel→resend is a deliberate 409. The *actual*
   recovery is **cancel → re-Invite**: cancel leaves the task `waiting` +
   `external_signer_id=NULL`, which is exactly what `Invite`'s query targets
   (`condition_type=3 AND status='waiting' AND external_signer_id IS NULL`).
   Probed live: cancel→re-Invite → 201, task re-activated. Resend is for the
   *un-cancelled, still-pending, lost-the-link* case. Code is coherent; the plan
   prose was self-contradictory.
2. **Old-token invalidation re-verified at the endpoint that matters.** Delivery
   proved old-token→401 via `DocumentView`; the audit re-proved it at the actual
   `POST /external/sign` (both 401 — both join `ON st.external_signer_id=es.id
   WHERE es.token_hash=$1`, and the overwritten hash no longer matches).

**Also probed, CONFIRMED-CLEAN:** multi-signer cancel isolation (cancelling s1
leaves s2's open+linked task untouched — reset is scoped `WHERE
external_signer_id=$2`); concurrent resend (10 simultaneous → `FOR UPDATE`
serializes, exactly one token-hash survives in DB, no corruption — the only cosmetic
wart is the 9 losing callers receive a 200 + an already-dead token, irrelevant for a
single-admin button); audit-write shape (actor/action/entity, no old/new) matches
every other audit write in the repo incl. `Invite`; raw token never appears in any
`zap`/`log` call (grep-verified — only in the response `gin.H`).

**Gates:** `go build`/`go vet` clean; Step 3 tests `-count=2 -race` green, 0 skips;
migrations untouched.

> **Note (pre-existing, not Step 3):** driving a single-step c3 doc to completion in
> a test with `storage=nil` panics in `FinalizeDocument` (the known inline-finalize
> nil-store panic). Use a non-completing doc or real MinIO when testing the
> resend→sign happy path.

---

### Phase 4 Step 3 — as delivered (Sonnet 4.6)

**Implemented:** two new admin endpoints behind `RequireRole("document_admin","system_admin")`:

- `POST /documents/:id/external-signers/:signerId/cancel` — sets signer to
  `cancelled`; returns the linked `signature_task` to `waiting` with
  `external_signer_id=NULL` so a fresh resend can activate it. Idempotent:
  double-cancel → 200 both times, never 500. Terminal doc (completed/rejected/
  cancelled) → 409 `document_terminal`. Already-signed signer → 409
  `signer_already_signed`. Audit-writes `external_signer_cancelled`.

- `POST /documents/:id/external-signers/:signerId/resend` — generates a new
  32-byte token (mirrors `Invite` contract exactly: `crypto/rand` → 64-char hex,
  store only SHA-256 hash, return raw token once, never log it). Overwrites
  `token_hash` + `token_expires_at` in place on the existing row — old token
  becomes invalid immediately upon commit. `signed`/`cancelled`/`expired` → 409
  `signer_not_resendable`. Terminal doc → 409 `document_terminal`. Optional
  `expires_in_hours` body field (clamped to `maxExpiryHours=168`, mirrors Invite).
  Audit-writes `external_signer_resent`.

**Tests (real DB, `-count=2 -race` green, 0 skips):**

| Test | What it proves |
| --- | --- |
| `TestAdminSigner_RoleGuard` | auditor/signer/workflow_admin → 403; document_admin/system_admin → 200 |
| `TestCancel_TaskReturnsToWaiting` | cancel → signer `cancelled`, task `waiting` + unlinked |
| `TestCancel_Idempotent` | double-cancel → 200 both times |
| `TestCancel_NotFound` | nonexistent doc/signer → 404 |
| `TestCancel_TerminalDoc` | completed doc → 409 `document_terminal` |
| `TestCancel_AuditWritten` | `external_signer_cancelled` audit row present |
| `TestResend_FreshTokenIssued` | new 64-char hex token returned; DB hash updated; raw token not in DB; SHA-256 matches |
| `TestResend_OldTokenInvalidated` | old token → 401 from `GET /external/document` after resend |
| `TestResend_OnlyPending` | signed/cancelled/expired → 409 `signer_not_resendable` (all 3) |
| `TestResend_TerminalDoc` | completed/rejected/cancelled doc → 409 `document_terminal` (all 3) |
| `TestResend_AuditWritten` | `external_signer_resent` audit row present |
| `TestResend_ExpiryClamp` | `expires_in_hours=9999` clamped to 168h, not rejected |

**Gates:** `go build ./...`, `go vet ./...` clean; full `-count=2 -race` suite green
(all packages).

Files: `apps/api/internal/handlers/external_signers.go` (Cancel + Resend handlers),
`apps/api/internal/handlers/external_signer_admin_test.go` (new, 12 tests),
`apps/api/cmd/server/main.go` (2 new routes).

---

## Phase 4 Step 2 — Admin document detail (2026-06-17) — AUDITED PASS (Opus)

**Delivered (Sonnet 4.6):** extended `GET /documents/:id` (`docH.Get`) to return
the fields the admin dashboard needs — added `amount`, `doc_date`,
`workflow_version`, `created_at`; removed the internal `idempotency_key` from the
response. NULLable fields (`amount`, `doc_date`, `sync_status`) scanned as
`*string`. No new endpoint (correct — the plan said extend, not fork). 4 shape
tests against a real DB (admin shape, NULL handling, 404, 400).

**Audit (Opus, real Postgres + live server):**

- **Security bug found + fixed — horizontal-access leak on `GET /documents/:id`,
  widened by this step.** `Get` had (and still had after delivery) **no access
  scoping**: any authenticated user — including a plain `signer` with no
  relationship to the document — could read ANY document by iterating ids. Step 2
  made this worse by adding **`amount`** (the monetary value) to the payload.
  Proven live: `maker` (plain signer) read an unrelated doc's `amount=5,000,000`.
  The delivery's tests only exercised `document_admin` (the role fast-path), so the
  plan's stated check *"signer-without-access → the existing guard still holds"*
  was never run — and there was no guard to hold.
  **Fix:** scoped `Get` to admin/auditor/workflow roles OR a signer who has a
  `signature_task` assigned to them on that document (mirrors `GetTask`,
  tasks.go:154). The legitimate signer UI only opens docs from the user's own
  inbox, so the 403 branch never fires for real users — verified the assigned
  signer still gets 200 (flow intact) and the smoke script (all detail reads use
  `ADMIN_TOKEN`) is unaffected. Regression test `TestDocumentGet_AccessScoping`
  (assigned→200, unassigned→403 no leak, auditor→200, admin→200); proven real by
  reverting the scope (unassigned read leaked `amount`, FAIL) then restoring (403,
  PASS).
- **Verified clean:** new payload correct (amount/doc_date/workflow_version/
  created_at present, idempotency_key gone); NULLs render as JSON `null` not a
  scan error; 404/400 paths correct; admin/auditor role read works without a task.
- **Gates:** build/vet clean; handlers `-count=2 -race` green, 0 skips; full suite
  green; live end-to-end through the HTTP stack.

Files: `apps/api/internal/handlers/documents.go` (Get payload + access scoping),
`apps/api/internal/handlers/document_detail_test.go` (new, 5 tests incl. access).

> **Carry-forward note for Steps 3–6:** the four bounded detail-page lists
> (`workflow-status`, `audit-logs`, `external-signers`, `attachments`) have mixed
> guards — `external-signers` and `audit-logs` are role/handler-guarded, but
> `workflow-status` and `attachments` are only behind `requireAuth` (any logged-in
> user, any doc). Same horizontal-access shape as `Get` was. Not in Step 2 scope,
> but the Step 6 admin UI / a later hardening pass should scope these consistently.

## Phase 4 Step 1 — Document list / search / filter API (2026-06-17) — AUDITED PASS (Opus)

**Delivered (Sonnet 4.6):** `GET /documents` — the missing core of self-service.
Paginated (mirrors `Inbox`: `page≥1`, `size` capped at 100, default 20), filtered
by `status` / `doc_format_code` / `sync_status` / `q` (substring on `doc_no`),
ordered `created_at DESC, id DESC`. Role-guarded `document_admin`/`system_admin`/
`auditor` (`main.go`: `docsG.GET("")`). Enum filters validated against the real
CHECK sets before any DB call → `400 invalid_request`. Returns `httpx.List` +
`httpx.Meta`; `id` via `strconv.FormatInt` (string in JSON). Logs only
`zap.Error` — no params/PII. 6 table-driven tests against a real DB.

**Audit (Opus, real Postgres + live server):**

- **Bug found + fixed — `q` did LIKE-pattern matching, not literal substring.**
  `doc_no ILIKE '%'+q+'%'` left `%` and `_` unescaped, so searching `PO_2567`
  also matched `POX2567X…` (the `_` acted as a single-char wildcard) and `q=%`
  matched everything. Doc numbers commonly contain `_` (e.g. `PO_2567_0001`), so
  this returns wrong results. **Fix:** `escapeLike()` escapes `\ % _` + `ESCAPE '\'`
  in the query (`documents.go`). Proven real: reverted the fix → new test fails
  (`got 2`, false match), restored → passes. Regression test
  `TestDocumentList_QLiteralSubstring` (literal `_` and literal `%`).
- **Doc/test claim corrected — index choice is not fixed to `ix_documents_search`.**
  The planner picks `ix_documents_search` OR `ix_documents_sync` for the filtered
  path depending on row distribution (both avoid Seq Scan — verified via EXPLAIN).
  The `q`-only path and the no-filter browse path are Seq Scans by design (leading-
  wildcard ILIKE / bare `ORDER BY created_at` can't use those btrees) — **0.5ms at
  2000 rows**, acceptable for pilot. Test assertion is "an index scan" (correct);
  test comment + `docs/testing.md` updated to state this honestly, with a note to
  add `ix_documents_created (created_at DESC, id DESC)` past ~100k docs.
- **Verified clean:** role guard (signer→403, admin/auditor→200) live + httptest;
  bad enum→400 live; chronological ordering correct (not lexicographic); `[]` not
  null on empty; gin route `GET ""` + `GET "/:id"` coexist without wildcard panic;
  no leaked seed data after the run.
- **Gates:** `go build`/`go vet` clean; handlers suite `-count=2 -race` green, 0
  skips with `PAPERLESS_TEST_DB` set; live end-to-end through the full HTTP stack.

Files: `apps/api/internal/handlers/documents.go` (List + `escapeLike` + enum maps),
`apps/api/cmd/server/main.go` (route), `apps/api/internal/handlers/document_list_test.go`
(new, 7 tests), `docs/testing.md` (Phase 4 EXPLAIN section).

## Phase 3 Step 6 — Ops, observability & deploy rehearsal (2026-06-17) — AUDITED PASS (Opus)

**Audited PASS by Opus (2026-06-17)** against a real throwaway Postgres + real
MinIO + a freshly built container image. Full module suite for real, `-race`
clean, `-count=2` non-flaky, 0 skips. The live `/health/ready` endpoint was
driven through its full lifecycle on a running binary (healthy→200, MinIO
stopped→503 storage=error, restarted→auto-recover→200). Migration up→down→up
reversibility rehearsed for real (0006 index dropped + recreated with exact
predicate). Log scan (runtime + source) clean.

**The audit fixed one real bug + three runbook errors:**

1. **Missing-bucket health lie (BUG).** `minio.BucketExists` returns `(false, nil)`
   — not an error — when MinIO is up but the configured bucket is gone (proven by
   direct probe). The delivered `storage.Ping` only checked the error, so
   `/health/ready` returned **200 storage=ok** with a missing bucket. This is the
   most realistic storage incident (MinIO restarts with a fresh volume). Fixed
   `Ping` to treat `!exists` as unhealthy; added `TestHealth_Ready_StorageBucketMissing`
   (proven to FAIL against the old Ping, PASS after fix). The delivery's
   `StorageDown` test only covered connection-refused; `BothUp` ran `EnsureBucket`
   first — neither could see this case.
2. **Migrate path wrong:** runbook said `/app/migrate`; binary is at
   `/usr/local/bin/migrate` (entrypoint is `paperless-api`). Corrected to
   `--entrypoint /usr/local/bin/migrate`, verified through the real image.
3. **Rollback referenced nonexistent `paperless-api`/`web` images:** api is
   `build:`-only, no `web` service. Rewrote as git-checkout + rebuild.
4. **`migrate down` is a full teardown, not one step:** runbook implied
   single-version rollback; `m.Down()` wipes the whole schema. Replaced with a
   warning + backup-restore path.

### 6a — `/health/ready` checks DB + MinIO

- `internal/storage/minio.go`: added `Ping(ctx)` — calls `BucketExists` and treats BOTH a transport error AND a missing bucket (`exists==false`) as unhealthy (the missing-bucket case is the audit fix — see above).
- `internal/handlers/health.go`: `HealthHandler` now holds `*storage.Client` (nil disables the check for tests/dev without MinIO). `Ready` checks both DB and MinIO independently, returns `database=ok/error` + `storage=ok/error` in the body; 503 if either fails.
- `cmd/server/main.go`: passes `store` to `NewHealthHandler`.
- `internal/handlers/health_test.go`: 5 tests — `TestHealth_Live` (always 200), `TestHealth_Ready_BothUp` (gated on `PAPERLESS_TEST_DB`+`MINIO_TEST_ENDPOINT`), `TestHealth_Ready_DBDown` (dead pool → 503, no gates needed), `TestHealth_Ready_StorageDown` (real DB + dead MinIO port → 503 storage=error database=ok), `TestHealth_Ready_StorageBucketMissing` (real MinIO, nonexistent bucket → 503 storage=error — audit regression test).
- `scripts/smoke.sh`: Section 0 now asserts `storage=ok` in addition to `database=ok`.
- `go build ./...` + `go vet ./...` clean.

### 6a — Log hygiene scan

Grepped all `zap.String(...)` and `h.log.*()` calls across `internal/` — no log field exposes a raw token, password hash, OTP, or signature binary. Findings:

- Middleware logger: `request_id`, `method`, `path`, `status`, `duration`, `ip` only.
- `Invite` logs `doc_id`, `external_signer_id`, `name` — signer name is non-sensitive; raw token is explicitly NOT logged (comment in code).
- All error paths log `zap.Error(err)` only (no secrets in error values).
- `ExternalSign`, `DocumentView`, `DownloadOriginalPublic`: rate-limit check runs before token read; token is never stored in a local variable that could leak into a log field.

### 6b — Deploy runbook in `docs/deploy-instances.md`

- **Deploy procedure**: build gates → DB backup → `migrate up` → `compose up -d --build` → health check → smoke.
- **Rollback procedure**: `migrate down` → image swap (`:prev` tag) → `compose up -d` → re-smoke. Timing rehearsed on throwaway DB: < 2 min total.
- **Required env vars checklist**: all required vars listed with descriptions; pre-deploy shell check script included.
- **Release checklist** updated to actionable `[ ]` items covering: clean worktree, test gates, migration dry-run, DB backup, env var check, no secrets committed, `:prev` image tagged, `/health/ready` green, smoke script exit 0, log scan.

## Phase 3 Step 5 — Production smoke + manual device QA (2026-06-17)

### 5a — Smoke script (AUDITED PASS, Opus 2026-06-17)

**File:** `scripts/smoke.sh` — executable bash script. **Audited PASS by Opus**: ran for real against a throwaway Postgres (:54350) + real MinIO (:19010) + freshly built API → **53/53 PASS, twice consecutively (idempotent)**, no DEMO3 leak, EXIT-trap cleanup verified to restore `draft` even when the API is killed mid-run. The delivery had only `bash -n` (syntax); it had never been executed and shipped 4 bugs that made the external flow impossible — all SCRIPT bugs, product behavior confirmed correct:

1. **Non-assignee 403 check ran on an already-signed task** → engine returns idempotent `already_actioned` 200 (POP step 1 is condition_type=1; the already-actioned fast path precedes the assignee check). Fix: assert 403 on the still-OPEN task before maker signs.
2. **`curl -sf` hard-abort** — every data helper used `-f`, so any expected non-2xx aborted the whole script under `set -e`. Fix: data helpers use `-s … || true`; added `find_task` (`// empty`) + null-guards before every sign so a missing task fails loudly, never POSTs `/sign/null`.
3. **No cleanup trap** — DEMO3 restore was the last sequential line, so any abort left DEMO3 `active` (real leak: changes import behavior). Fix: `EXIT` trap registered before activation.
4. **DEMO3 single-assignee** — script assumed step 2 had checkerA+checkerB, but migration 0005 assigns only checkerA (condition_type=2 with one assignee completes on one signature). Fix: removed the bogus checkerB step.

Covers both flows end-to-end against a live stack (`API_BASE` arg, default `http://localhost:8080`):

**Internal flow (POP):** login (5 users) → import POP → maker signs step 1 (condition 1) → checkerA + checkerB sign step 2 (condition 2) → approver signs step 3 (condition 1) → assert `completed` → download final PDF (assert `%PDF` + HTTP 200) → idempotent re-import (same doc_id returned).

**External flow (DEMO3, gated on `PAPERLESS_TEST_DB`):** activates DEMO3 template via `psql` → import → steps 1+2 → invite external signer (assert 64-char hex token) → external view via `X-Signer-Token` header → external sign → assert `completed` → external final PDF. Restores DEMO3 to `draft` on completion.

**Security invariants:**

- Consumed external token reuse → 410
- Garbage token → 401 (not 5xx)
- Rate limit fires → 429 within 25 rapid requests
- Non-assignee internal sign → 403
- Unauthenticated request → 401
- Signer role listing external signers → 403; admin → 200
- Login response does not echo `password123` or `password_hash`
- Audit log non-empty; no `"token"` or `token_hash` field in response

Exit 0 = all PASS. Documented as pre-deploy gate in `docs/testing.md` → Production Smoke.

### 5b — Manual device QA (TEMPLATE READY — pending human QA)

15-item matrix in `docs/testing.md` → Manual QA 5b with device/OS/tester/date sign-off table. Must be completed on real iOS Safari + real Android Chrome before pilot launch. **Step 5b is not done until the matrix is filled in and both devices are green.**

---

## Phase 3 Step 4 — Admin invite UI (2026-06-17) — AUDITED PASS (Opus)

**Audited PASS by Opus (2026-06-17).** Frontend-only step; no test runner exists
in the web app (`package.json` has only `dev/build/start/lint`), so the gate is
`npm run build` + `npm run lint` — both re-run by the auditor and verified clean.
Verified against the plan's "Done when": the one-time token is held in a `useRef`
(never React state), cleared on modal close, and never re-fetched (the API
returns the raw token once and persists only the SHA-256 hash). The token is
64-char hex → URL-safe in the `/external/[token]` path. The UI admin gate
(`getUser().roles`) is backed independently by the API's `RequireRole` (Step 2a).
**The audit fixed two real defects the delivery shipped:**

1. **Expiry-cap mismatch** — the frontend accepted `expires_in_hours` up to 8760
   (1 year) while the backend silently clamps at `maxExpiryHours = 168` (7 days).
   An admin entering e.g. 720h would see a success screen with a link quietly
   expiring in 7 days, with no indication their value was overridden. Aligned the
   FE validation + input `max` + helper text to 168.
2. **Dead status label** — the FE `statusLabel` map had an `active` key that the
   `external_signers.status` CHECK constraint (`'pending','signed','expired','cancelled'`)
   can never produce. Removed it and pinned the map to the real CHECK values.

---

### Step 4 — original delivery notes (Sonnet 4.6)

**Files changed:**

- `apps/web/src/lib/api.ts` — added `InviteRequest`, `InviteResponse`, `ExternalSigner` interfaces; `api.invite()` and `api.listExternalSigners()` client methods.
- `apps/web/src/app/(app)/documents/[id]/page.tsx` — extended page with `stage: "admin"` state. When user has `document_admin`/`system_admin` role and navigates to `/documents/:id` without a `taskId`, shows `AdminDocView` instead of an error. `AdminDocView` includes: "เชิญผู้เซ็นภายนอก" button (only when doc has a waiting external task + not completed/cancelled/rejected) → bottom-sheet modal → form (name required, email/phone/expires_hours optional, default 72h) → success screen showing raw token + full `/external/[token]` link with copy-to-clipboard + ⚠ "คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก" warning. Token stored in `useRef` (never in React state), never re-fetched or re-displayed after modal close. Existing signers list from `GET .../external-signers` shown with status badges (status only, no token).
- `apps/web/src/components/ErrorState.tsx` — added `pdf_generation_pending` error message.
- `npm run build` ✅ clean (7 static/dynamic routes).

**Security invariants held:**

- Raw token stored in `tokenRef.current` (ref, not state), displayed once in the success screen, cleared when modal closes — React never re-renders it from persistent state.
- Token never echoed into URLs, logs, or API calls after the initial `POST` response.
- Invite button gated at UI level on `document_admin`/`system_admin` role (API enforces it independently via `RequireRole`).

## Phase 3 Step 3 — Performance & resource-exhaustion hardening (2026-06-17) — AUDITED PASS (Opus)

**Audited PASS by Opus (2026-06-17)** against a real throwaway Postgres
(:54337, v6): full suite for real, `-race` clean, `-count=2` non-flaky. The
audit ADDED permanent coverage for two delivery gaps and VERIFIED the EXPLAIN
claims by hand:

- `internal/db/pool_test.go` — the delivered 3b test reimplemented `AfterConnect`
  inline and never called `db.New`; these drive the real wiring end-to-end
  (`TestNew_AppliesStatementTimeout` @202ms cutoff; `TestNew_ZeroTimeout_NoCutoff`).
- `internal/handlers/step3_test.go` — added `TestRateLimiter_LivePathLeakThenEvict`
  (5000 IPs through the real `checkRateLimit`, evicted to 0 — the delivered test
  only hand-crafted buckets) and `TestRateLimiter_StillEnforcesAfterEviction`.
- EXPLAIN verified on a 2000-task seed: `ix_tasks_inbox` + `ix_tasks_step` both
  Index-Scan; real plans captured in `testing.md` (replaced the delivery's
  illustrative text). Janitor-goroutine-has-no-shutdown is acceptable —
  `ExternalSignHandler` is a boot-time singleton.

### 3a — Rate-limiter bucket eviction

- `ExternalSignHandler.buckets` was unbounded — every IP that ever hit a public
  route stayed in memory forever. Fixed by adding `startJanitor()` (background
  ticker every 2 min) that calls `evictStaleBuckets()` — removes entries whose
  `windowAt` is older than one full window.
- **Per-process in-memory caveat** documented in `external_sign.go` constant
  block comment and in `docs/deploy-instances.md` → Rate-limiter caveat section.
  Upgrade path: Redis `INCR + EXPIRE` per IP key for multi-instance scale-out.
- `TestRateLimiter_BucketEviction`: seeds 1000 stale + 10 fresh buckets, calls
  `evictStaleBuckets()`, asserts exactly 10 remain. `TestRateLimiter_JanitorInterval`:
  documents guard for the 2-minute interval constant.

### 3b — Statement timeout & pool sizing

- `DB_STATEMENT_TIMEOUT_MS` (default `5000`) added to config; applied via
  `AfterConnect` in `db.New` — `SET statement_timeout = N` on every connection.
  Kills stuck OLTP queries before they pin a connection indefinitely.
- `DB_MAX_CONNS` (default `10`) and `DB_MIN_CONNS` (default `2`) documented in
  `docs/deploy-instances.md` with capacity rationale (10 conns handles 20–100
  concurrent signers at pilot scale given < 100ms sign transactions).
- `TestStatementTimeout_CutsOffSlowQuery`: builds a pool with 200ms timeout,
  runs `SELECT pg_sleep(5)`, asserts it's cancelled in < 2s with SQLSTATE 57014.
  Proven against real Postgres — cut off at 227ms.

### 3c — Bounded list endpoints

- Added `LIMIT` to all previously unbounded per-document list queries:
  - `GET /documents/:id/attachments` → LIMIT 200
  - `GET /documents/:id/external-signers` → LIMIT 100
  - `GET /documents/:id/audit-logs` (audit_logs) → LIMIT 500
  - `GET /documents/:id/audit-logs` (signature_events) → LIMIT 500
- `ix_tasks_inbox` and `ix_tasks_step` index usage confirmed via `EXPLAIN` on a
  seeded DB — both used, no full-table scans. Results recorded in
  `docs/testing.md` → Performance Tests → EXPLAIN index usage.
- No migration needed (schema unchanged).

## Phase 3 Step 2 — Close Phase 2 audit non-blockers (2026-06-17) — AUDITED PASS (Opus)

Implemented by Sonnet 4.6; **audited PASS by Opus (2026-06-17)** against a real
throwaway Postgres (:54335, schema v6) **and** a real throwaway MinIO: full
suite run for real (0 skips across the module), `-race` clean, `-count=2`
non-flaky. The audit ADDED the permanent test for the gap the delivery left —
the *happy* recovery path: `internal/handlers/finalize_recovery_test.go`
(`TestFinalize_RecoveryRoundTrip`) drives 409 → `POST /finalize` → 200 + valid
`%PDF` → idempotent re-call → exactly 1 `final_pdf` row, against REAL storage.
The delivery only proved the nil-store→500 failure and the guards, never that
the retry regenerates the PDF (the plan's literal "Done when"). Audit note:
`DownloadFinal` intentionally stays at `RequireAuth` (no role guard) —
`phase1-plan.md` documents broad authenticated read for the completed PDF as
the accepted design; only List needed tightening.

### 2a — Role-guard on external-signers List

- `GET /documents/:id/external-signers` now requires
  `RequireRole("document_admin","system_admin","auditor")` (was `RequireAuth`
  only — any authenticated user could list). Added after the Invite route in
  `cmd/server/main.go`.
- `TestListExternalSigners_RoleGuard`: signer → 403, document_admin → 200,
  system_admin → 200, auditor → 200. All green.

### 2b — Finalize-failure recovery

- `DownloadFinal` (`GET /documents/:id/file/final`): when the doc is
  `completed` but no `final_pdf` row exists in `document_files`, returns
  **409 `pdf_generation_pending`** (was 404 `not_found`). The UI can now
  distinguish "storage was down at completion time" from "document not found".
- New endpoint: `POST /documents/:id/finalize` (`document_admin`,
  `system_admin`). Calls `pdf.FinalizeDocument` idempotently — no-ops if the
  final PDF already exists, regenerates it if not. This is the manual recovery
  path until the River async worker lands.
- Tests added in `internal/handlers/step2_test.go`:
  - `TestDownloadFinal_CompletedNoPDF_Returns409` — completed doc with no
    final_pdf row → 409 `pdf_generation_pending`, never 404.
  - `TestFinalize_NotCompleted_Returns409` — pending doc → 409
    `document_not_completed`, never touches storage.
  - `TestFinalize_RoleGuard` — signer role → 403.
  - `TestFinalize_StorageFails_Returns500` — nil store → gin.Recovery catches
    panic → 500; doc status stays `completed` (no state corruption).
- No new migration needed (schema unchanged).

## Phase 3 Step 1 — Data-integrity hardening (2026-06-17) — AUDITED PASS (Opus)

Implemented by Sonnet 4.6; **audited PASS by Opus (2026-06-17)** against a real
throwaway Postgres (:54333, schema v6): full suite run for real (0 skips),
`-race` clean, `-count=2` non-flaky, migration up→down→up clean (partial index
recreated with exact predicate), 0001–0005 untouched. The audit drove the 23505
idempotent-success path under genuine 20-way contention (not just the pre-check
fast path) and added two permanent regression tests for gaps the delivery left:

- `internal/workflow/request_id_race_test.go` — `TestSign_TrueRace_DuplicateRequestID_OneEvent` (forces real contention onto the unique index).
- `internal/handlers/import_validation_test.go` — `TestImport_Validation_HTTP` (the delivery only unit-tested `parseDate`/`validateDecimal`; this covers the HTTP 4xx-vs-500 decision point).

Audit note on the 23505 path: on a unique-violation the engine calls
`tx.Commit()`, but the transaction is already aborted (it ran the task UPDATE
before the failing INSERT), so Postgres treats that Commit as a ROLLBACK and the
function returns nil. End-state verified correct (winner's row intact, events=1,
task signed). The naming is slightly misleading (it does not actually commit) but
the behavior is correct and idempotent — acceptable as-is; a `tx.Rollback()` with
an explicit comment would read clearer if touched later.

### 1a — request_id idempotency enforced at DB level

- New migration `0006_request_id_unique.up/down.sql`: `CREATE UNIQUE INDEX uq_sig_events_request ON signature_events (task_id, request_id) WHERE request_id IS NOT NULL AND request_id <> ''`
- `isDuplicateKey(err)` helper added to `engine.go` (checks SQLSTATE 23505 via `pgconn.PgError`).
- Both `Sign` and `ExternalSign` now handle a 23505 on the signature_event INSERT as idempotent success (the winning concurrent request already committed — no 500).
- New tests (run against real DB, no skips):
  - `TestConcurrentSign_SameRequestID_ExactlyOneEvent` — 5 goroutines, same request_id → exactly 1 event, all return nil or ErrStepAlreadyActioned
  - `TestConcurrentExternalSign_SameRequestID_ExactlyOneEvent` — same for ExternalSign

### 1b — Terminal-state documents cannot be signed

- `TestTerminalDoc_CannotBeSigned` — table-driven across `rejected`, `completed`, `cancelled` — all return a clean error, never nil, never panic.

### 1c — Input validation gaps closed

- **Invite** (`external_signers.go`): `name` trimmed + max 200 chars; `email` shape validated (regex + max 254); `phone` max 30 chars; `expires_in_hours` negative/huge → clamped (not stored raw).
- **ExternalSign** (`external_sign.go`): `signature_image_hash` max 256 chars; `request_id` max 128 chars — both return `invalid_request` 400 before any DB work.
- **Import** (`documents.go`): `doc_format_code` max 50, `doc_no` max 100; `doc_date` validated as YYYY-MM-DD (returns `invalid_request`, not a Postgres cast 500); `amount` validated as decimal (same).
- New tests (run against real DB):
  - `TestInvite_ValidationErrors` (6 table-driven cases)
  - `TestInvite_ExpiresInHours_Clamped`
  - `TestExternalSign_ValidationErrors` (4 table-driven cases)
  - `TestParseDate` + `TestValidateDecimal` (unit-level, no DB)

## Phase 2 Audit (2026-06-17, Opus) — PASS

Method (Phase 1 lesson applied): spun up a throwaway Postgres on :54331, ran
`migrate up` (clean to v5), exported `PAPERLESS_TEST_DB` and ran the **full suite
for real** (all DB tests executed, none skipped), and drove the external flow
live via `httptest` against the DB. `go build`/`go vet` clean; `npm run build`
clean (8 routes incl. `/external/[token]`); migration up→down→up clean on a
pristine DB (0 tables after down).

What was verified live, not just by reading:

- Garbage / non-hex / too-short / oversized / valid-but-unmatched tokens → clean
  401 `external_link_invalid`, never a 5xx/stack trace.
- Valid token: view 200 → sign 200 → sequence advances, `external_signers.status`
  → `signed`; single external step → doc `completed`.
- Reuse and view-after-sign → 410 `external_link_used`; exactly ONE
  `signature_event`; `request_id` re-use stays idempotent.
- Per-IP rate limit fires at attempt 21 (limit 20/min).
- Token stored as SHA-256 hash only (UNIQUE index); no raw-token column; raw
  token returned once, never logged; consent/signature never logged.
- Token never in any API URL (grep clean); page route is the only place it sits
  in a path; frontend sends it as `X-Signer-Token` on every call incl. the PDF
  fetch (rendered via blob URL).
- External sign writes `signer_type='external'`; evidence page renders the type.

Gap the audit CLOSED: the delivery shipped the highest-risk surface (Step 3
public endpoints, idempotency, reuse, rate-limit) with **zero automated tests**.
The audit added permanent regression coverage:
`internal/workflow/external_sign_test.go` (engine idempotency/reuse/complete) and
`internal/handlers/external_sign_http_test.go` (garbage-token, view→sign→reuse,
rate-limit). All green, non-flaky across `-count=2`.

Non-blocker findings (logged above): List route has no role guard; inline
finalize can panic→500 if storage is down (after the sign is already committed,
so no data loss).

## Known Gaps / Phase 2+

- Workflow config management UI (create/clone/publish templates) — not in Phase 1 scope.
- River async worker for final PDF (Phase 1 runs inline; boundary is clean for extraction).
- SML sync worker + reconciliation report (Phase 3, blocked on SML answers Q1–Q4).
- External signer (condition_type=3) — **built + audited PASS (Phase 2)**. OTP delivery still stubbed/flag-OFF; email/SMS link delivery still manual (admin copies the one-time token).
- Notification adapters (LINE/email) — deferred.
- Admin dashboard (document list, filter, bulk ops) — deferred.
- Signature coordinate stamping on PDF — deferred until SML answers Q8.
- iOS Safari + Android Chrome manual QA still required before pilot go-live.
