# Testing

## Required Gates

```bash
# Backend (Go)
cd apps/api && go test ./... && go vet ./...

# Worker
cd workers && go test ./...

# Frontend (Next.js)
cd apps/web && npm run lint && npm run build

# Integration / smoke (Docker Compose up, then)
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/health/ready   # checks DB + MinIO
```

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

## Manual QA

- **Browser:** iOS Safari, Android Chrome (signing is finger-on-glass); desktop Chrome/Edge.
- **Admin flow:** create workflow → publish version → import → verify tasks created in order.
- **Mobile/responsive:** portrait + landscape; PDF zoom/pan; clear-signature confirm; preview before submit; page does not scroll while signing.
- **Production smoke:** health/ready green; login; import; sign; download final PDF; view audit trail.
