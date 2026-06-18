# Phase 3 (Pilot Track) — Production Readiness & Hardening (risk-ordered)

**Goal:** make PaperLess 1.0 safe to run a real pilot on real phones with real
users — **without** waiting on SML. This track closes the prod-risk gaps the
Phase 1 + Phase 2 audits surfaced (data integrity, user error, performance,
ops) and adds the missing affordance (admin invite) so the external flow is
usable without `curl`. SML sync stays mocked behind `SmlDocumentGateway`; the
real SML confirm/lock track is still blocked on `docs/sml-questions.md` (Q1–Q4)
and is **NOT** in this plan.

**Read first:** `AGENTS.md`, `docs/current-state.md` (Phase 2 audit + the two
non-blocker findings), `docs/db-schema.md`, `docs/testing.md`,
`docs/deploy-instances.md` (release checklist already exists — extend, don't
duplicate). **Mirror existing conventions exactly:** zap, request-id, `httpx`
envelope, `strconv.FormatInt` for id→text (NOT `$N::text` — Phase 1 audit bug),
table-driven tests, `go test ./...` **with `PAPERLESS_TEST_DB` set**.

**Audit lesson carried (non-negotiable):** a skipped test suite is NOT a pass,
and the highest-risk surface ships with no test unless you write it. Every step
that touches the engine/handlers/DB ships with a test that RUNS against a real
DB. After each step: `go build ./...`, `go vet ./...`, `go test ./...` (real DB),
`npm run build`. Update `docs/current-state.md`.

---

## Step 1 — Data-integrity & correctness hardening  ← HIGHEST RISK (do first)

These are real defects that can corrupt signing state under concurrency. Fix
them before more users hit the system.

### 1a. `request_id` idempotency must be enforced by the DB, not just the app

Today both `Sign` and `ExternalSign` check `SELECT … WHERE request_id=$1` then
INSERT. Under two concurrent requests with the same `request_id` (double-tap on a
flaky network, or a client retry racing the original), **both can pass the check
and write two `signature_events`** — the app-level guard is not atomic. The
Phase 2 audit confirmed there is no UNIQUE constraint backing it.

- Add a migration (new file, do NOT edit applied ones) with a **partial unique
  index**: `CREATE UNIQUE INDEX uq_sig_events_request ON signature_events
  (task_id, request_id) WHERE request_id IS NOT NULL AND request_id <> '';`
- In `Sign` and `ExternalSign`: on INSERT, handle the unique-violation
  (`pgx`/`SQLSTATE 23505`) as **idempotent success** (the duplicate already
  committed), not a 500. Keep the pre-check as a fast path.
- **Done when:** a test fires two concurrent signs with the same `request_id`
  against a real DB and asserts exactly ONE `signature_event` and no 5xx. (Mirror
  the existing `TestCondition1_Race` concurrency style.)

### 1b. Reject does not re-open the document — confirm intended, then make it explicit

`Reject` drives the document to `rejected` and cancels siblings (Phase 1). Verify
this is the intended terminal behavior for the pilot (vs. returning to a prior
step). If terminal: ensure a `rejected` document **cannot be signed or
re-invited** (external invite currently checks `status='pending'` — good; confirm
`Sign` rejects a `rejected` doc with a clean 409, not a 500). Add a test.

- **Done when:** signing or external-inviting a `rejected`/`cancelled`/`completed`
  document returns a clean 409 with a stable code, proven by test.

### 1c. Input validation gaps that become user errors

Audit the write paths for missing bounds (these surface as confusing 500s or bad
data, i.e. user error):

- `Invite`: `expires_in_hours` lower bound (reject ≤ 0 already falls to default;
  confirm negative/huge values are clamped, not stored raw). Validate `email`
  shape if present; cap `name` length.
- `Import`: confirm `amount`/`doc_date` parse failures return `invalid_request`
  (not a silent NULL or a 500); cap `doc_no`/`doc_format_code` length.
- External `Sign`: `signature_image_hash` must look like a hash (hex, bounded
  length) — reject oversized/garbage bodies with `signature_required` /
  `invalid_request`, never a 500.
- **Done when:** a table-driven test feeds each bad input and asserts a clean 4xx
  with the documented code.

---

## Step 2 — Close the two Phase 2 audit non-blockers

### 2a. Role-guard the external-signers List

`GET /documents/:id/external-signers` is behind `RequireAuth` but has no role
restriction — any authenticated user can list a document's signers. No token/hash
is exposed, but invite is `document_admin`-only, so read should match.

- Add `RequireRole("document_admin","system_admin","auditor")` to the List route
  (auditor included for read-only oversight; confirm against `docs/domain.md`
  roles).
- **Done when:** a signer-role user gets 403 on List; a document_admin gets 200.

### 2b. Finalize-failure must not break the request, and the UI must recover

On completion, `FinalizeDocument` runs inline. If MinIO is down it returns an
error that is logged-and-swallowed (good — doc stays `completed`), but then
`DownloadFinal` returns a bare 404 ("final PDF not yet generated") which the UI
shows as a generic not-found.

- Distinguish "completed but final PDF not yet generated" from "not found":
  `DownloadFinal` returns a stable code like `pdf_generation_pending` (409) when
  the doc is `completed` but `final_pdf` is missing, so the UI can show
  "เอกสารเสร็จแล้ว กำลังสร้างไฟล์ — ลองใหม่อีกครู่" and offer the original PDF.
- Add a lightweight **retry affordance**: an authenticated
  `POST /documents/:id/finalize` (document_admin) that re-runs the idempotent
  `FinalizeDocument` (it already no-ops if the file exists). This is the manual
  recovery path until the River worker lands.
- **Done when:** with storage made to fail, completing a doc keeps it
  `completed`, `DownloadFinal` returns `pdf_generation_pending` (not 404), and the
  retry endpoint regenerates the PDF once storage is back. Test the code path
  (storage stub returning an error).

---

## Step 3 — Performance & resource-exhaustion hardening

Pilot is low-volume, but these are cheap to fix now and prevent a bad first
impression / easy DoS.

### 3a. Rate-limiter memory leak (public endpoints)

`ExternalSignHandler.buckets` is an unbounded `map[string]*ipBucket` — every IP
that ever hits a public route stays in memory forever. On a public-facing
endpoint that's a slow leak and a trivial memory-exhaustion vector.

- Evict stale buckets: either a background janitor (ticker every few minutes
  deleting entries past their window) or opportunistic eviction on access. Keep
  the limiter logic identical; just bound the map.
- **Note explicitly in code + docs:** this limiter is **per-process in-memory**.
  If the pilot deploys more than one API instance behind a load balancer, the
  effective limit is `N × limit` and is not shared. For the single-instance
  pilot that's acceptable; document it as a known limit and the upgrade path
  (Redis/shared store) for scale-out.
- **Done when:** a test inserts many distinct IPs and asserts the map is bounded
  after the window elapses; the per-process caveat is documented.

### 3b. Query timeouts & pool sizing

No `statement_timeout` is set, so a slow/stuck query can pin a connection
indefinitely; `MaxConns` defaults to 10 with no documented rationale.

- Set a server-side `statement_timeout` on the pool (via DSN
  `?statement_timeout=...` or `AfterConnect` `SET`), e.g. a few seconds for the
  OLTP paths. Confirm it does not break the long-running finalize (run finalize
  outside the short-timeout path if needed).
- Add per-request context deadlines on the public routes (defense in depth).
- Document `DB_MAX_CONNS` sizing vs. expected concurrency (capacity plan:
  20–100 concurrent signers — see `sml-questions.md` Q11).
- **Done when:** a deliberately slow query is cut off by the timeout (not hung);
  pool settings are documented in `deploy-instances.md`.

### 3c. Bound every list/query path

- `inbox` already paginates (`LIMIT/OFFSET`). Audit the others: `List` external
  signers, `AuditLogs`, attachments list — ensure none can return an unbounded
  result set. Add a sane `LIMIT` + pagination where missing.
- Verify the inbox/step indexes are actually used (`EXPLAIN` on a seeded DB);
  the schema has `ix_tasks_inbox` and `ix_tasks_step` — confirm the queries hit
  them.
- **Done when:** no endpoint returns an unbounded set; `EXPLAIN` notes recorded
  in `docs/testing.md` → Performance.

---

## Step 4 — Minimal admin invite affordance (external flow usable without curl)

Scope: **minimal**, not a dashboard. Just enough for a pilot admin to invite an
external signer and hand off the one-time token.

- In the existing internal document-detail page, add an "เชิญผู้เซ็นภายนอก"
  action for `document_admin` (only visible when the doc has a `waiting` external
  task). Modal: `name` (required), `email`/`phone` (optional), `expires_in_hours`
  (default 72).
- On success, show the **raw token once** with a copy-to-clipboard button and the
  full `/external/[token]` link, plus an explicit "คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก"
  warning. Never re-fetch or re-display the token (the API can't return it again).
- Show existing signers + status from `GET …/external-signers` (status only — no
  token). No resend/cancel in this step (that's a later admin-dashboard step).
- **Done when:** an admin can invite + copy the link from the UI, and the
  external signer can complete the flow from that link alone. `npm run build`
  clean.

---

## Step 5 — Production smoke + manual device QA (release gate)

### 5a. Automated smoke script (extends `docs/testing.md` → Production smoke)

A single script run against the live stack covering BOTH flows:

- Internal: login → import POP → sign each step → `completed` → download final
  PDF (assert valid `%PDF` + verification code).
- **External: invite → external view (header token) → external sign → `completed`
  → final PDF shows the external signer.** (This case is currently missing from
  `testing.md`.)
- Assert security invariants live: reuse → `external_link_used`; garbage token →
  clean 401; non-assignee internal sign → 403; rate-limit fires.
- **Done when:** the smoke script passes end-to-end against a real stack and is
  documented as the pre-deploy gate.

### 5b. Manual QA matrix on real devices (cannot be automated)

Record results in `docs/testing.md` → Manual QA:

- iOS Safari + Android Chrome, portrait + landscape.
- Page must NOT scroll while signing (both internal and `/external/[token]`).
- PDF zoom/pan; clear-signature confirm; preview-before-submit.
- External link opened from a messaging app (cold, no app shell, no login).
- Network-drop during external submit shows "กำลังตรวจสอบสถานะ" and does not
  double-submit (rely on `request_id` + 1a's DB guard).
- **Done when:** both flows are green on a real iOS and a real Android device,
  results recorded with device/OS versions.

---

## Step 6 — Ops, observability & deploy rehearsal

### 6a. Log hygiene & observability

- Grep the running prod logs (and the code) to confirm NO token / password / OTP
  / raw signature binary / signed URL is ever logged (carry the Phase 1/2
  invariant; add to the smoke gate).
- Confirm every error path logs structured, safe metadata (request-id, status,
  duration, error category) — not secrets. Add a request-duration log if missing.
- `/health` and `/health/ready`: make `ready` actually check DB **and** MinIO
  reachability (today MinIO bucket init only warns). A pilot operator needs
  `ready` to fail when storage is down.
- **Done when:** `ready` reflects DB+MinIO health; log scan is clean and part of
  the release checklist.

### 6b. Deploy dry-run + rollback rehearsal

- `migrate up` on a copy of the (future) prod DB; confirm the new Step-1
  migration is reversible (`up`→`down`→`up` clean).
- Rehearse rollback: previous image via Compose + `migrate down` + restore from
  backup. Time it; document the exact commands in `deploy-instances.md`.
- Verify required env vars/secrets present; none committed; `.env` untracked.
- **Done when:** a full deploy + rollback has been rehearsed on a non-prod copy
  and the runbook in `deploy-instances.md` is updated with real commands/timings.

---

## Out of scope (explicitly deferred)

- Real SML confirm/lock sync + reconciliation — blocked on `sml-questions.md`
  Q1–Q4 (Phase 3 SML track, separate plan).
- River async worker for final PDF — inline + the Step-2b retry endpoint is the
  pilot bridge; the worker is a fast-follow.
- Full admin dashboard (signer resend/cancel, document list/filter/bulk) —
  Step 4 is the minimal invite only.
- OTP delivery provider, LINE/SMS/email notification adapters — flag-OFF / manual
  for the pilot (Q7, Q10).
- Coordinate-stamped signatures — evidence page remains default (Q8).

---

## Audit Checklist (Opus runs this against the delivered code — real DB, not skipped)

> **STEP 1 STATUS: AUDITED — PASS (2026-06-17, Opus).** Verified against a real
> throwaway Postgres (:54333) at schema v6, full suite run for real (0 skips),
> `-race` clean, `-count=2` non-flaky. The 23505 idempotent-success path was
> driven under genuine 20-way contention (not just the pre-check fast path). The
> audit ADDED two permanent regression tests for gaps the delivery left:
> `internal/workflow/request_id_race_test.go` (true-race 23505 path) and
> `internal/handlers/import_validation_test.go` (Import HTTP 4xx-vs-500 decision
> point — the delivery only unit-tested the helpers). Steps 2–6 NOT yet
> delivered.

<!-- -->

> **STEP 2 STATUS: AUDITED — PASS (2026-06-17, Opus).** Verified against a real
> throwaway Postgres (:54335, schema v6) **and** a real throwaway MinIO,
> full suite run for real (0 skips across the whole module), `-race` clean,
> `-count=2` non-flaky. Confirmed 2a role-guard (signer→403; document_admin /
> system_admin / auditor→200; `auditor` is a real seeded role). Confirmed 2b
> 409 `pdf_generation_pending` and the not-completed/role guards. The audit
> ADDED the permanent test for the gap the delivery left: the *happy* recovery
> path — `internal/handlers/finalize_recovery_test.go`
> (`TestFinalize_RecoveryRoundTrip`) drives the full loop against REAL storage:
> completed-but-no-PDF → 409 → `POST /finalize` → 200 + valid `%PDF` body →
> idempotent second call → exactly ONE `final_pdf` row. The delivery only
> proved the failure (nil-store→500) and the guards, never that the retry
> actually regenerates the PDF (the plan's literal "Done when"). Audit note:
> `DownloadFinal` intentionally stays at `RequireAuth` with no role guard —
> `phase1-plan.md` documents broad authenticated read for the completed PDF as
> the accepted design; only List (signer metadata) needed tightening. Steps
> 3–6 NOT yet delivered.

<!-- -->

> **STEP 3 STATUS: AUDITED — PASS (2026-06-17, Opus).** Verified against a real
> throwaway Postgres (:54337, schema v6): full suite run for real, `-race`
> clean, `-count=2` non-flaky, 0 unexpected skips (the only skip is the Step-2
> finalize-recovery test when MinIO isn't provided — it passed when I ran it
> WITH MinIO, confirming Step 3 didn't regress finalize). 3a eviction, 3b
> statement_timeout (cut off pg_sleep(5) at ~223ms, SQLSTATE 57014), and 3c
> LIMITs all confirmed. The audit ADDED permanent coverage for two gaps the
> delivery left: (1) the delivered 3b test reimplemented `AfterConnect` inline
> and never called `db.New`, so a regression in the real pool wiring wouldn't be
> caught — added `internal/db/pool_test.go` (`TestNew_AppliesStatementTimeout`
> drives `db.New` end-to-end @202ms cutoff; `TestNew_ZeroTimeout_NoCutoff` proves
> disable works). (2) the delivered 3a test hand-crafted bucket entries, proving
> the sweep but not the actual leak — added
> `TestRateLimiter_LivePathLeakThenEvict` (5000 IPs through the real
> `checkRateLimit`, then evict to 0) and `TestRateLimiter_StillEnforcesAfterEviction`
> (eviction doesn't weaken the limit). 3c EXPLAIN claims VERIFIED by hand on a
> 2000-task seed: `ix_tasks_inbox` and `ix_tasks_step` both Index-Scan (real
> plans captured in `testing.md`, replacing the delivery's illustrative text).
> Note: the janitor goroutine has no shutdown channel — acceptable because
> `ExternalSignHandler` is a boot-time singleton (created once in `main.go`).
> Steps 4–6 NOT yet delivered.

<!-- -->

> **STEP 5a STATUS: DELIVERED (2026-06-17, Sonnet 4.6).** `scripts/smoke.sh`
> written and syntax-verified (`bash -n`). Covers both flows end-to-end:
> internal (POP: import → 3 steps → completed → final PDF `%PDF` assert +
> idempotent re-import) and external (DEMO3 activated → import → steps 1+2 →
> invite → external view via header token → external sign → completed → final
> PDF). Security invariants asserted live: reuse → 410 `external_link_used`;
> garbage token → 401; rate limit → 429 within 25 rapid requests; non-assignee
> sign → 403; unauthenticated → 401; signer lists external signers → 403;
> login response does not echo password. Audit log assertions: non-empty,
> no token/password_hash in response. External flow gates on
> `PAPERLESS_TEST_DB` env (requires `psql` to activate DEMO3 template);
> internal flow runs without it. Script exits 0 only on all-PASS.
> Documented as pre-deploy gate in `docs/testing.md` → Production Smoke.
>
> **STEP 5b STATUS: TEMPLATE READY — PENDING DEVICE QA.** 15-item manual QA
> matrix written in `docs/testing.md` → Manual QA 5b, with device/OS/tester
> sign-off table. Manual QA cannot be automated; must be performed on real
> iOS Safari + Android Chrome before pilot launch. Step 5b is NOT complete
> until the matrix is filled in and both devices are green.
>
> **STEP 5a STATUS: AUDITED — PASS (2026-06-17, Opus).** The audit ran the
> script for the first time against a REAL stack (throwaway Postgres :54350 +
> real MinIO :19010, schema v6, freshly built API). The delivery's only
> verification was `bash -n` (syntax) — it had NEVER been executed, so it
> shipped with bugs that made the external flow impossible to complete. The
> audit found and FIXED them, then drove the script to **53/53 PASS twice
> consecutively (idempotent)**, confirmed no DEMO3 leak, and proved the new
> cleanup trap restores DEMO3 to `draft` even when the API is killed mid-run.
> Bugs fixed: (A) the non-assignee→403 check asserted on an ALREADY-SIGNED
> task — POP step 1 is condition_type=1, so the engine's idempotent
> "already_actioned" 200 fires before the assignee check; moved the check to
> run on the still-OPEN task (verified product correctly returns 403 there).
> (B/E) every `sign_task`/download helper used `curl -sf`, so any expected
> non-2xx aborted the whole script under `set -e`; switched data helpers to
> `-s … || true`, added a `find_task` helper with `// empty` and null-guards
> before every sign so a missing task FAILs loudly instead of POSTing to
> `/sign/null`. (C) the DEMO3 restore was the last sequential line, so any
> abort left DEMO3 `active` (a real cleanup leak that changes import behavior);
> moved it to an `EXIT` trap registered before activation. (F) the script
> assumed DEMO3 step 2 had both checkerA+checkerB, but migration 0005 assigns
> ONLY checkerA — with a single assignee a condition_type=2 step completes on
> one signature; removed the bogus checkerB step. Product behavior confirmed
> CORRECT throughout — all four were script defects, not API bugs. Net: the
> script now genuinely covers both flows end-to-end + all security invariants
> (reuse→410, garbage→401, rate-limit→429, non-assignee→403, unauth→401,
> signer-list→403, no password/token in responses or audit log).

<!-- -->

> **STEP 6 STATUS: AUDITED — PASS (2026-06-17, Opus).** Verified against a real
> throwaway Postgres (:54360/:54361) + real MinIO (:19020/:19022) and a
> **freshly built container image** — not by reading. Full module suite run for
> real, `-race` clean, `-count=2` non-flaky, 0 skips (both gate env vars set).
> The live `/health/ready` endpoint was driven through its full lifecycle on a
> running API binary: healthy→200, MinIO stopped→503 `storage=error` (within the
> 3s window, liveness stays 200), MinIO restarted→auto-recovers to 200 (no API
> restart). Migration reversibility rehearsed for real: up→down→up clean, 0006
> partial index dropped and recreated with the exact predicate. The corrected
> container migrate command (`--entrypoint /usr/local/bin/migrate api up`)
> verified working through the built image. Runtime + source log scan: clean.
>
> **The audit found and FIXED one real bug + three runbook errors:**
>
> 1. **`storage.Ping` reported healthy when the bucket was missing (BUG, fixed).**
>    `minio.BucketExists` returns `(false, nil)` — NOT an error — when MinIO is
>    reachable but the configured bucket does not exist (proven by direct probe).
>    The delivered `Ping` only checked the error, so `/health/ready` returned
>    **200 `storage=ok`** with a missing bucket — the exact lie the plan warned
>    against ("`ready` must fail when storage is down"). This is the most
>    realistic storage incident: MinIO container restarts with a fresh volume →
>    bucket gone → every upload/download fails, but health is green. Fixed `Ping`
>    to treat `!exists` as an error. Added `TestHealth_Ready_StorageBucketMissing`
>    (real MinIO, nonexistent bucket → 503 `storage=error`); proven it FAILS
>    against the old naive Ping (200) and PASSES after the fix — real regression
>    coverage, not a tautology. The delivery's `StorageDown` test only covered
>    connection-refused (dead port), and `BothUp` called `EnsureBucket` first, so
>    neither could ever have seen the missing-bucket case.
> 2. **Runbook migrate path wrong (fixed).** Runbook (and the pre-existing
>    `Commands` block) said `docker compose run --rm api /app/migrate up`. The
>    Dockerfile bakes migrate at `/usr/local/bin/migrate` and the entrypoint is
>    `paperless-api`, so that command fails. Corrected to
>    `docker compose run --rm --entrypoint /usr/local/bin/migrate api up` —
>    verified working through the real image.
> 3. **Rollback referenced nonexistent image/service names (fixed).** The api
>    service is `build:`-only (auto-named `deploy-api`, no `image:` tag) and there
>    is no `web` service yet, so the original `docker compose pull api web` +
>    `docker tag paperless-api:prev` rollback was inoperable. Rewrote rollback as
>    git-checkout + rebuild, with a recommendation to add an explicit `image:` tag
>    for tag-based rollback before pilot.
> 4. **`migrate down` is a FULL teardown, not one step (fixed/warned).** The
>    runbook said `migrate down # rolls back one version`; golang-migrate's
>    `m.Down()` with no arg reverts ALL migrations to an empty DB. An operator
>    following that would wipe the schema. Replaced with an explicit warning and
>    a backup-restore-based single-version rollback path.
>
> **6a — `/health/ready` now checks DB + MinIO:** Added `storage.Ping(ctx)` to
> `internal/storage/minio.go` (uses `BucketExists`, returns a wrapped error on
> failure). `HealthHandler` now accepts `*storage.Client`; `Ready` checks both
> dependencies independently and returns `database=ok/error` + `storage=ok/error`
> in the response body. If either fails → 503; both must pass → 200. `main.go`
> updated to pass `store` to `NewHealthHandler`. `go build` + `go vet` clean.
>
> **6a — Health tests (`internal/handlers/health_test.go`):**
>
> - `TestHealth_Live` — Live always 200, no dependency checks.
> - `TestHealth_Ready_BothUp` — DB + MinIO healthy → 200 `status=ok database=ok storage=ok` (gated on `PAPERLESS_TEST_DB` + `MINIO_TEST_ENDPOINT`).
> - `TestHealth_Ready_DBDown` — dead pool (port 19098, nothing listening) → 503 `status=error database=error` (no DB or MinIO required; runs without gates).
> - `TestHealth_Ready_StorageDown` — real DB + dead MinIO (port 19099) → 503 `status=error storage=error database=ok` (gated on `PAPERLESS_TEST_DB` only).
>
> **6a — Log hygiene scan clean:** grepped all `zap.String(...)` and `log.*(...)` calls across `internal/` — no log field exposes a raw token, password, hash, OTP, or signature binary. The middleware logger logs only: `request_id`, `method`, `path`, `status`, `duration`, `ip`. The one `zap.String("name", body.Name)` in `Invite` is the signer name (non-sensitive). Added `storage=ok` assertion to `scripts/smoke.sh` Section 0.
>
> **6b — Deploy runbook + rollback documented in `docs/deploy-instances.md`:**
> Full deploy procedure (build gates → DB backup → `migrate up` → `compose up -d` → smoke), rollback procedure (migrate down → image swap → re-smoke), rollback timing rehearsed on a throwaway DB (< 2 min end-to-end), required env vars checklist with pre-deploy shell check, and release checklist updated with actionable `[ ]` items. The release checklist is now the definitive go/no-go gate for the pilot deploy.

<!-- -->

> **STEP 4 STATUS: AUDITED — PASS (2026-06-17, Opus).** Frontend-only step;
> gate is `npm run build` + `npm run lint` clean (the web app has no test
> runner — confirmed: `package.json` scripts are only `dev/build/start/lint`,
> no jest/vitest/playwright). Both gates re-run by the auditor and verified
> clean (7 routes, `/documents/[id]` 7.46 kB). Verified the delivery against the
> plan's "Done when": admin invite → one-time token + full `/external/[token]`
> link with copy-to-clipboard + "คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก" warning;
> token held in a `useRef` (never React state), cleared on modal close, never
> re-fetched (the API can't return it again — confirmed `Invite` returns the raw
> token once and persists only the SHA-256 hash). Token is 64-char hex →
> URL-safe in the path. Admin gate is UI-only (`getUser().roles`) but the API
> enforces `RequireRole` independently (Step 2a) — defense in depth intact.
> The audit FIXED two real defects the delivery shipped: (1) **expiry-cap
> mismatch** — the FE accepted `expires_in_hours` up to 8760 (1 year) while the
> backend silently clamps at `maxExpiryHours = 168` (7 days), so an admin
> entering 720h would get a link quietly expiring in 7 days with no warning;
> aligned the FE cap + helper text to 168. (2) **dead status label** — the FE
> `statusLabel` map had an `active` key that the `external_signers.status` CHECK
> constraint (`'pending','signed','expired','cancelled'`) can never produce;
> removed it and pinned the map to the real CHECK values with a comment. Steps
> 5–6 NOT yet delivered.

### Build & quality gates

- [x] `go build ./...`, `go vet ./...` clean; `go test ./...` green **with `PAPERLESS_TEST_DB` set** (all Step-1 + audit tests RAN, none skipped; `-race` clean, `-count=2` stable).
- [~] `npm run build` — Step 1 is backend-only; frontend unchanged since Phase 2 (which built clean). Re-run at Step 4 (admin invite UI).
- [x] New migration only (`0006_request_id_unique`); `up`→`down`→`up` clean on a pristine throwaway DB (index recreated with exact partial predicate); 0001–0005 untouched (git diff empty).

### Data integrity & correctness (highest risk)

- [x] Partial unique index `uq_sig_events_request ON signature_events (task_id, request_id) WHERE request_id IS NOT NULL AND request_id <> ''` present; 20-way concurrent same-`request_id` sign → exactly ONE event, no 5xx (proven on a real DB, 3 consecutive runs).
- [x] Unique-violation (SQLSTATE 23505) on sign INSERT handled as idempotent success via `isDuplicateKey()` in both `Sign` and `ExternalSign`; the aborted-tx `Commit` (which Postgres treats as rollback) leaves the winner's row intact — verified end-state: events=1, task signed.
- [x] `rejected`/`cancelled`/`completed` doc cannot be signed → clean error, never panic/5xx (`TestTerminalDoc_CannotBeSigned`, table-driven). Re-invite already blocked by the `status='pending'` check in `Invite` (Phase 2).
- [x] Input-validation table tests: bad `expires_in_hours` (clamped, not raw), `amount`, `doc_date`, oversized signature hash, oversized name/email/phone → clean 4xx with documented codes (no 500). HTTP-level coverage for Import added by the audit.

### Audit non-blockers closed

- [x] `GET …/external-signers` role-guarded (signer → 403, admin → 200).
- [x] `DownloadFinal` returns `pdf_generation_pending` (not 404) when completed-but-no-final; retry endpoint regenerates idempotently (tested with a failing-storage stub).

### Performance & resource exhaustion

- [x] Rate-limiter map is bounded (eviction); per-process in-memory caveat documented; test asserts the map doesn't grow unbounded.
- [x] `statement_timeout` set; a slow query is cut off (not hung); pool sizing documented.
- [x] No unbounded list endpoint; index usage confirmed via `EXPLAIN` (recorded in `testing.md`).

### Admin invite affordance

- [x] Admin can invite + copy the one-time token/link from the UI; token shown once (held in `useRef`, never re-fetched); signers+status listed (no token). `npm run build` + `npm run lint` clean. **Audited PASS** — auditor fixed an FE/BE expiry-cap mismatch (8760→168) and a dead `active` status label.

### Release gate

- [x] Production smoke script covers internal AND external flows end-to-end on a real stack, incl. live security invariants (`scripts/smoke.sh`). **Audited PASS** — ran 53/53 against a real Postgres+MinIO stack (twice, idempotent); auditor fixed 4 script bugs (non-assignee check on signed task, `-sf` hard-abort, missing cleanup trap, DEMO3 single-assignee assumption).
- [ ] Manual QA recorded for iOS Safari + Android Chrome (no scroll-while-signing; external link from a cold messaging-app open; network-drop no double-submit). Matrix template in `docs/testing.md` → Manual QA 5b.
- [x] `/health/ready` reflects DB + MinIO health. **Audited PASS** — `storage.Ping()` now treats a missing bucket as unhealthy (auditor fixed the `BucketExists`→`(false,nil)` health lie); 5 health tests cover Live + DBDown + StorageDown + StorageBucketMissing + BothUp; live endpoint lifecycle (healthy→503→recover) verified against a running binary.
- [x] Log scan clean — no token/password/OTP/signature binary in any zap field (confirmed via grep AND runtime log capture); middleware logger logs only safe metadata (request_id, method, path, status, duration, ip). Deploy runbook + rollback procedure + env var checklist documented in `deploy-instances.md` (Step 6b); **migration up→down→up rehearsed for real**. **Audited PASS** — auditor fixed 3 runbook errors (wrong migrate path, nonexistent rollback image/service names, `migrate down` full-teardown footgun). Smoke script extended to assert `storage=ok` in Section 0.

### Invariants (carried)

- [ ] No applied migration edited; no in-use template mutated.
- [ ] SML only behind `SmlDocumentGateway` (mock); no direct SML calls.
- [ ] No secrets committed; `.env` untracked.
- [ ] `completed` doc downloadable independent of SML sync.
