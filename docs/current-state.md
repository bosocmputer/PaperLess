# Current State

Last updated: 2026-06-16

## Latest Handoff

- Current production/dev state: **Phase 1 complete.** All 7 steps of `docs/phase1-plan.md` are implemented and green.
- Last completed change: Full Phase 1 build — migration runner, auth+RBAC, workflow engine, document import, sign/reject API, attachments+audit viewer, final PDF generation, Next.js PWA frontend.
- Current branch/release: `main`
- Known broken or risky areas: SML Confirm/Lock table/field unknown — blocks Phase 3 sync, not Phase 1/2 (mock gateway). Final PDF uses evidence-page approach (not coordinate stamping) — Phase 3 enhancement once SML answers Q8.

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

- Goal: await Opus audit (uses checklist in `docs/phase1-plan.md`) and SML team answers (`docs/sml-questions.md`).
- In progress: none — Phase 1 delivered.
- Blocked: SML confirm/lock fields (Phase 3 only).
- Next safest step: run Opus audit against `docs/phase1-plan.md` Audit Checklist; then start Phase 2 (versioned config UI, notifications) or Phase 3 (SML sync) once answers arrive.

## Known Gaps / Phase 2+

- Workflow config management UI (create/clone/publish templates) — not in Phase 1 scope.
- River async worker for final PDF (Phase 1 runs inline; boundary is clean for extraction).
- SML sync worker + reconciliation report (Phase 3, blocked on SML answers Q1–Q4).
- External signer (condition_type=3) — engine + DB ready; OTP flow + email link not wired.
- Notification adapters (LINE/email) — deferred.
- Admin dashboard (document list, filter, bulk ops) — deferred.
- Signature coordinate stamping on PDF — deferred until SML answers Q8.
- iOS Safari + Android Chrome manual QA still required before pilot go-live.
