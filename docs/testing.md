# Testing

## Required Gates

```bash
# Backend (Go) — full gates including workflow DB integration tests
cd apps/api
PAPERLESS_TEST_DB="postgres://postgres:paperless@localhost:5432/paperless_test?sslmode=disable" \
  go test ./... && go vet ./...

# Without PAPERLESS_TEST_DB set, workflow/* tests are SKIPPED (not failed).
# CI MUST provide this env var — a run without it does NOT satisfy the gate.
# The test DB must have migrations applied (0001–0005 up) before running.

# Frontend (Next.js)
cd apps/web && npm run build

# Integration / smoke (Docker Compose up, then)
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/health/ready   # checks DB + MinIO (both must be ok)
```

### Setting up the test database (one-time, local dev)

```bash
# 1. Create the test DB
createdb paperless_test   # or: psql -c "CREATE DATABASE paperless_test"

# 2. Apply migrations (seeds included)
cd apps/api
DATABASE_URL="postgres://postgres:paperless@localhost:5432/paperless_test?sslmode=disable" \
  go run ./cmd/migrate up

# 3. Run full test suite
PAPERLESS_TEST_DB="postgres://postgres:paperless@localhost:5432/paperless_test?sslmode=disable" \
  go test ./... -v -count=1
```

### CI requirement

Any CI pipeline (GitHub Actions, etc.) must:

1. Spin up a PostgreSQL service (postgres:16).
2. Run `go run ./cmd/migrate up` against it.
3. Export `PAPERLESS_TEST_DB` before `go test ./...`.

Without these steps, the workflow engine tests (race condition, condition 1/2/3, sequence gate) are skipped and the gate is **not satisfied**.

## Must-not-skip Workflow Tests (from requirements §31)

- **condition 1:** user A signs → users B/C tasks become `skipped`.
- **condition 1 race:** A and B submit concurrently → exactly one succeeds; the other gets "step already actioned".
- **condition 2:** A signs → step still incomplete until B signs (progress 1/2 then 2/2).
- **condition 3:** external token expired → cannot sign.
- **sequence gate:** step 2 cannot open while step 1 incomplete.
- **reject:** requires a reason; writes audit; returns to defined step.
- **idempotency:** same key + same hash re-import = no duplicate; same key + different hash = revision conflict (409).
- **double-tap:** same `request_id` twice on sign = one signature event.

## Security Tests

- Non-assignee calling sign API → rejected.
- Completed document → cannot sign again.
- External link reuse / after-expiry → rejected.
- File download for a document the caller cannot access → rejected.
- Audit log / logs contain no token, password, or raw signature binary.

## Acceptance Scenarios

- **Happy path:** import POP → 3-step workflow → sign each → completed → final PDF downloadable.
- **Empty state:** signer with no open tasks sees a clear "no pending documents".
- **Permission failure:** signer opens a task not theirs → clear "not allowed".
- **External API timeout:** SML confirm times out → document stays `completed`, sync shows `sync_pending`/`sync_unknown` (never "synced").
- **Duplicate/retry/idempotency:** retried import does not create a second document.
- **Migration rollback:** `down` migration restores prior schema from an empty-DB baseline.

## Performance Tests

- Inbox with 10,000 documents loads first page < 2s (server-side pagination).
- Search by doc_no / format / status < 2s at 10k–50k docs.
- Upload a 5 MB PDF; first-page preview 2–4s on normal network.
- Submit signature on iOS Safari and Android Chrome.
- Final PDF gen failure → worker retries.

### EXPLAIN index usage (Phase 3 Step 3c — captured 2026-06-17, Opus audit, schema v6)

Real `EXPLAIN (ANALYZE, COSTS OFF)` output captured against a seeded DB
(2000 documents, 2000 tasks, 666 `open`), after `ANALYZE signature_tasks`:

**Inbox query** (`GET /signature-tasks/inbox`):

```text
Limit (actual time=0.644..0.654 rows=20 loops=1)
  ->  Index Scan using ix_tasks_inbox on signature_tasks st (rows=20)
        Index Cond: ((assigned_user_id = 1) AND (status = 'open'::text))
```

`ix_tasks_inbox` is used ✅ — no Seq Scan even with `enable_seqscan=on`.

**Workflow step query** (`GET /documents/:id/workflow-status`):

```text
Index Scan using ix_tasks_step on signature_tasks st (rows=1)
  Index Cond: (document_id = $0)
```

`ix_tasks_step` is used ✅.

**Bounded list endpoints** (no unbounded result set):

| Endpoint | LIMIT | Rationale |
| --- | --- | --- |
| `GET /documents/:id/attachments` | 200 | Max practical attachments per doc |
| `GET /documents/:id/external-signers` | 100 | Max external signers per doc |
| `GET /documents/:id/audit-logs` (audit_logs) | 500 | Per-doc audit trail |
| `GET /documents/:id/audit-logs` (signature_events) | 500 | Per-doc events |
| `GET /signature-tasks/inbox` | 20 (existing) | Paginated ✅ |

### EXPLAIN index usage — `GET /documents` list (Phase 4 Step 1 — captured 2026-06-17, Opus audit, schema v6)

Captured against a seeded DB with `enable_seqscan=off` to force index consideration.

**Filtered path** (`?doc_format_code=…&status=…` — the common dashboard filter):

```text
Limit
  ->  Sort  (Sort Key: created_at DESC, id DESC)
        ->  Index Scan using ix_documents_search on documents
              Index Cond: ((doc_format_code = …) AND (status = …))
```

The planner picks **either** `ix_documents_search` (doc_format_code, doc_no,
status, created_at) **or** `ix_documents_sync` (status, sync_status) depending
on row distribution — both avoid a Seq Scan. The test asserts "an index scan",
not a specific index. ✅

**`q` substring path** (`?q=…` → `doc_no ILIKE '%…%'`): **Seq Scan** — a
leading-wildcard `ILIKE` cannot use a btree index. Acceptable at pilot scale.

**No-filter browse path** (default dashboard landing, no params): **Seq Scan +
top-N heapsort** — `ORDER BY created_at DESC` cannot use `ix_documents_search`
(which leads with `doc_format_code`). At 2000 rows this executes in **~0.5ms**
(`EXPLAIN ANALYZE`, top-N heapsort, 27 kB). Acceptable for pilot; if document
volume grows past ~100k, add `CREATE INDEX ix_documents_created ON documents
(created_at DESC, id DESC)` to serve the default list.

> **`q` is a LITERAL substring match.** LIKE metacharacters (`%`, `_`, `\`) in
> `q` are escaped (`escapeLike` + `ESCAPE '\'`), so searching `PO_2567` matches
> a literal underscore — not "any character". Regression test:
> `TestDocumentList_QLiteralSubstring`.

## Production Smoke (pre-deploy gate — Phase 3 Step 5a)

Run `scripts/smoke.sh` against the live stack **before every deployment**.

```bash
# Stack must be up: cd deploy && docker compose up -d
# Then:
./scripts/smoke.sh http://localhost:8080

# To include the external flow (DEMO3 template), also set:
PAPERLESS_TEST_DB="postgres://postgres:paperless@localhost:5432/paperless?sslmode=disable" \
  ./scripts/smoke.sh http://localhost:8080
```

The script exits 0 only when ALL checks pass. It covers:

| Section | What is checked |
| --- | --- |
| 0. Health | `/health` → 200, `/health/ready` status=ok, database=ok, storage=ok |
| 1. Auth | Login for admin, maker, checkerA, checkerB, approver; `/auth/me` |
| 2. Internal flow | Import POP → sign step 1 (maker, cond 1) → sign step 2 (checkerA + checkerB, cond 2) → sign step 3 (approver, cond 1) → completed → download final PDF (assert `%PDF` + 200) → idempotent re-import |
| 3. External flow | (requires `PAPERLESS_TEST_DB`) Activate DEMO3 → import → steps 1+2 → invite external signer → external view (header token) → external sign → completed → external final PDF; restore DEMO3 to draft |
| 4. Security (external) | Reuse consumed token → 410; garbage token → 401; rate limit fires → 429 within 25 rapid requests |
| 5. Security (internal) | Non-assignee sign → 403; non-existent doc → 404; unauthenticated → 401; signer lists external signers → 403; admin lists → 200; login response does not echo password/password_hash |
| 6. Audit log | `/audit-logs` succeeds; has entries; no raw token/password/token_hash in response |

## Manual QA

### 5b Matrix — required before pilot launch (Phase 3 Step 5b)

Record results in this table. Each row must be signed off with tester name and date.
Step 5b is **Done when** both flows are green on a real iOS and a real Android device.

| # | Test | iOS Safari | Android Chrome | Notes |
| --- | --- | --- | --- | --- |
| 1 | Login → inbox loads | ☐ | ☐ | |
| 2 | Open doc from inbox (portrait) | ☐ | ☐ | |
| 3 | Open doc from inbox (landscape) | ☐ | ☐ | |
| 4 | PDF viewer: zoom + pan | ☐ | ☐ | |
| 5 | **Page does NOT scroll while signing** (internal) | ☐ | ☐ | Critical: signature canvas must capture touch, not scroll the page |
| 6 | Draw signature → clear → redraw → preview → submit | ☐ | ☐ | |
| 7 | Network-drop during sign submit → "กำลังตรวจสอบสถานะ" → no double-submit | ☐ | ☐ | Kill network mid-request, restore, verify DB has exactly 1 signature_event |
| 8 | Reject flow → reason required → submit | ☐ | ☐ | |
| 9 | External link opened cold from messaging app (no app shell, no login) | ☐ | ☐ | Test by sharing link via LINE/iMessage, tap from there |
| 10 | **Page does NOT scroll while signing** (external `/external/[token]`) | ☐ | ☐ | Critical |
| 11 | External sign → consent checkbox → submit → done screen | ☐ | ☐ | |
| 12 | Reuse of same external link → "เอกสารนี้ได้รับการเซ็นแล้ว" error state | ☐ | ☐ | |
| 13 | Expired external link → "ลิงก์เซ็นเอกสารนี้หมดอายุแล้ว" error state | ☐ | ☐ | |
| 14 | Admin: "เชิญผู้เซ็นภายนอก" button visible on doc with waiting external task | ☐ | ☐ | |
| 15 | Admin invite modal → fill form → copy link → warning shown | ☐ | ☐ | Confirm token not re-shown after modal close |

**Device/OS record (fill in before sign-off):**

| Role | Device | OS Version | Browser Version | Tester | Date |
| --- | --- | --- | --- | --- | --- |
| iOS tester | | | | | |
| Android tester | | | | | |

### Previous guidance

- **Browser:** iOS Safari, Android Chrome (signing is finger-on-glass); desktop Chrome/Edge.
- **Admin flow:** create workflow → publish version → import → verify tasks created in order.
- **Production smoke:** run `scripts/smoke.sh` (see section above).
