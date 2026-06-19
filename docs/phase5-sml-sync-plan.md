# Phase 5 Plan — PaperLess → SML confirm/lock sync (Opus plan, Sonnet implements)

**Goal:** when a document reaches `completed`, PaperLess must lock it back in SML
by calling the `sml-api-bybos` lock endpoint — durably, with retry, idempotent,
and WITHOUT blocking the sign request. The completed document must stay
downloadable even if SML sync never succeeds (existing invariant — do not break).

**Read first:** `docs/current-state.md` (engine completion path), this file.
Conventions to mirror exactly: `zap` logging (error category + counters, **never**
token/secret/PII), `strconv.FormatInt` for id→text (NOT `$N::text`), table-driven
tests that RUN against a real DB (`PAPERLESS_TEST_DB`), `httpx` envelope.
**A timeout is NEVER success.** Testing runs on server `192.168.2.109` (local has
no Docker) — Go tests still run locally against remote Postgres.

---

## หลักฐาน (verified โดย Opus — Sonnet ไม่ต้องค้นซ้ำ)

**Schema — `sml_sync_jobs` พร้อมแล้ว** (`apps/api/migrations/0001_init.up.sql`):
```
id, document_id FK→documents(ON DELETE CASCADE),
job_type    CHECK IN ('update_confirm','update_lock'),
status      CHECK IN ('pending','running','succeeded','failed','retry') DEFAULT 'pending',
attempt_count int DEFAULT 0, max_attempts int DEFAULT 5,
request_payload jsonb, response_payload jsonb, error_message text,
next_retry_at timestamptz, created_at, updated_at
INDEX ix_sync_queue (status, next_retry_at, attempt_count)
```
→ For lock we use `job_type='update_lock'`. **No new migration needed.**
`documents.sync_status` CHECK = `not_required,sync_pending,synced,sync_failed,sync_unknown`.

**Completion point — where to enqueue** (`apps/api/internal/workflow/engine.go`):
- Internal sign: **line 201–211** — inside the sign `tx`, after
  `UPDATE documents SET status='completed'` + `writeAuditLog`. Enqueue the lock job
  **in the same tx** so completion+enqueue are atomic (no lost job if the process
  dies). `tx` is `pgx.Tx`; `task.DocumentID` is the id.
- External sign: same pattern near **line 618–623** (the external completion path) —
  find the matching `status='completed'` block in `ExternalSign` and enqueue there too.
- The doc_no to send to SML = `documents.doc_no` (lock endpoint takes doc_no). The
  worker reads it; the enqueue only needs `document_id`.

**SML lock endpoint — DONE + verified on the sml-api-bybos side** (real DB):
`POST {SML.BaseURL}/api/v1/documents/{doc_no}/lock`
headers `X-Api-Key: {SML.APIKey}`, `X-Tenant: {SML.Tenant}`
- success `200 {"success":true,"data":{"doc_no","table","trans_flag","is_lock_record":1,"already_locked":bool}}`
- already locked → still `200` `already_locked:true` (idempotent — treat as success)
- missing doc → `404 {"success":false,"error":{"code":"document_not_found"}}`
- POST is the only method; GET-by-querytoken does NOT apply here.

**Config — wired already** (`apps/api/internal/config/config.go:38-42, 65-67`):
`cfg.SML.BaseURL` (default `http://192.168.2.109:8200`), `cfg.SML.APIKey`,
`cfg.SML.Tenant`. **No gateway/worker code exists yet** (grep: no `SmlGateway`,
no worker goroutine — `main.go:161` is only the HTTP server goroutine).

**Invariant (do NOT break):** final PDF download works independent of sync — the
download handler reads `document_files` (file_type='final_pdf'), never `sync_status`.
Sync failure must leave the doc `completed` + downloadable.

---

## ไฟล์ที่ต้องแก้/สร้าง

1. **`apps/api/internal/sml/client.go`** (new) — HTTP client for sml-api-bybos.
   - `type Client struct { baseURL, apiKey, tenant string; http *http.Client }`
   - `NewClient(cfg)` with `http.Client{Timeout: 10s}`.
   - `func (c *Client) Lock(ctx, docNo string) (LockResult, error)` — POST the lock
     URL with the two headers; decode the envelope. **Map outcomes:**
     `200 success:true` (incl. already_locked) → success; `404` → permanent
     failure `ErrDocNotFound` (do NOT retry — the doc isn't in SML); other non-2xx
     / decode error / **timeout** → retryable error. Never log headers/apikey.

2. **`apps/api/internal/sml/worker.go`** (new) — polling worker.
   - `type Worker struct { pool, client, log, interval }`
   - `Run(ctx)`: ticker loop (interval e.g. 5s). Each tick: claim one due job with
     `SELECT ... FOR UPDATE SKIP LOCKED` from `sml_sync_jobs WHERE status IN
     ('pending','retry') AND (next_retry_at IS NULL OR next_retry_at<=now())
     ORDER BY id LIMIT 1`, set `status='running'`. (SKIP LOCKED is correct here —
     unlike the engine — because each job is independent; skipping a locked job is
     fine.)
   - Look up `documents.doc_no` for the job's document_id.
   - Call `client.Lock`. On success: `status='succeeded'`, set
     `documents.sync_status='synced'`, write audit `document_synced` (entity_type
     `sync` is in the audit CHECK set). On `ErrDocNotFound`: `status='failed'`,
     `sync_status='sync_failed'`, record error (no retry). On retryable error:
     `attempt_count++`; if `attempt_count>=max_attempts` → `status='failed'`,
     `sync_status='sync_failed'`; else `status='retry'`,
     `next_retry_at=now()+backoff(attempt_count)` (exponential: 30s,1m,2m,5m,15m).
   - Always store `error_message`/`response_payload` (safe metadata only).
   - Honour `ctx` cancellation for clean shutdown.

3. **`apps/api/internal/workflow/engine.go`** — enqueue on completion.
   - At **line ~207** (after audit log, still in tx) and in the external path
     (~line 618+): `INSERT INTO sml_sync_jobs (document_id, job_type, status)
     VALUES ($1,'update_lock','pending')` + `UPDATE documents SET
     sync_status='sync_pending' WHERE id=$1`. Same `tx`. Add a guard so a doc that
     already has a pending/running lock job isn't enqueued twice (a `WHERE NOT
     EXISTS` or unique-ish check) — completion happens once, but be safe.

4. **`apps/api/cmd/server/main.go`** — boot the worker.
   - After building `store`/`pool`, construct `sml.NewClient(cfg)` +
     `sml.NewWorker(...)` and `go worker.Run(ctx)` with a context cancelled on
     shutdown (mirror the existing signal handling at main.go:146+). Gate on
     `cfg.SML.APIKey != ""` — if SML isn't configured (pilot without SML), do NOT
     start the worker (jobs just queue as `pending`; nothing locks). Log which mode.

5. **Tests (real DB, `-race -count=2`, 0 skips):**
   - `internal/sml/client_test.go` — `httptest.Server` faking sml-api-bybos:
     200 success, 200 already_locked, 404→ErrDocNotFound, 500→retryable,
     timeout→retryable. Assert headers sent (X-Api-Key/X-Tenant) and NONE logged.
   - `internal/sml/worker_test.go` — seed a `sml_sync_jobs` row + a doc, point the
     client at an `httptest.Server`: success→`succeeded`+`sync_status=synced`;
     500 with max_attempts=1→`failed`+`sync_failed`; retryable mid-way→`retry`
     with `next_retry_at` set; 404→`failed` no retry. SKIP LOCKED: two workers,
     one job, exactly one claims it.
   - `internal/workflow/engine_test.go` — completing a doc enqueues exactly ONE
     `update_lock` job + sets `sync_status='sync_pending'`; re-running completion
     does not duplicate.

---

## Decisions ตัดสินแล้ว (Sonnet ห้ามเปลี่ยน)

- **Enqueue inside the sign tx** (atomic with completion) — NOT a post-commit call.
  A lost job after a flaky commit is a correctness hole.
- **Polling worker, not River/queue lib** — schema already models the queue; a
  simple ticker + `FOR UPDATE SKIP LOCKED` matches pilot scale (10k–50k docs).
  SKIP LOCKED is correct here (independent jobs) — different from the engine.
- **`already_locked:true` = success**, not an error (idempotent lock per SML F2).
- **404 = permanent failure**, no retry (doc not in SML — retrying won't help).
- **Timeout = retryable, never success.**
- **Worker off when `SML.APIKey==""`** — pilot without SML keeps queuing jobs
  harmlessly; no lock attempts.
- **doc stays `completed` + downloadable on sync failure** — only `sync_status`
  changes; never touch `documents.status` or the final PDF from the worker.

## Done when

- Complete a doc → exactly one `update_lock` job enqueued, `sync_status=sync_pending`.
- Worker locks via the real endpoint → job `succeeded`, `sync_status=synced`,
  audit `document_synced` written. (Verify on server against sml1_2026; restore
  test doc lock state after, like the lock-endpoint verification did.)
- 404 → `failed`+`sync_failed`, no retry; 500/timeout → `retry` with backoff;
  exhausted attempts → `failed`+`sync_failed`.
- Two workers never double-process one job (SKIP LOCKED proven by test).
- Final PDF still downloads when sync is `sync_failed`.
- `go build/vet` clean; full suite `-race -count=2` green, 0 skips; `npm run build`
  unaffected (no FE change in this phase).

## ห้าม / ระวัง (invariants)

- PaperLess must NOT connect to the SML DB directly — only via sml-api-bybos.
- Never log `X-Api-Key`, tenant secrets, tokens, or full request/response bodies
  that could carry them. Log: job_id, document_id, attempt, status, error category.
- Do NOT edit migrations 0001–0006; no new migration is needed for this phase.
- Worker must not panic the process — recover per-tick; a bad job marks itself
  `failed`/`retry`, never crashes the loop.
- Backoff must be bounded; `max_attempts` (default 5) is the stop — no infinite retry.
- Enqueue must be idempotent at completion (no duplicate jobs if completion logic
  is ever re-entered).
