# AGENTS.md — PaperLess 1.0

Short index for this repository. Keep under 10KB; details live in `docs/`.

## Project Shape

- Product: PaperLess 1.0 — e-signature document workflow that sits between people and the SML ERP. Receives documents (PO/INV/etc.) from SML as PDF + metadata, routes them through a configurable signing workflow, produces a legally-stamped final PDF, then syncs Confirm/Lock status back to SML.
- Audience: internal signers (mostly on mobile/tablet, signing with a finger), workflow admins, document admins, auditors, and external signers (customers).
- Core workflow: SML creates doc → import into PaperLess (idempotent) → pick workflow template by `doc_format_code` + active version → create signature tasks by position/sequence/condition → signers sign in order → all steps complete → generate final PDF (signatures + e-transaction-act text + verification code) → sync Confirm/Lock back to SML.
- Runtime type: on-premise (customer LAN, `192.168.2.x`). Web app + Go API + Postgres + MinIO + background worker, all via Docker Compose.
- Critical integrations: **SML ERP — accessed ONLY through the existing `sml-api-bybos` Go service** (PaperLess never touches the SML database directly).

## Stack

- Backend: Go 1.24 + Gin + pgx/v5 (mirrors `sml-api-bybos` conventions: zap logging, request-id middleware, `{success,data,meta}` envelope).
- Frontend: Next.js (App Router) + TypeScript, PWA; `pdf.js` viewer, `signature_pad` for touch signing.
- Database: PostgreSQL 16 (PaperLess owns its own DB, separate from SML).
- Queue/worker: River (Postgres-backed job queue) — no Redis in phase 1.
- Object storage: MinIO (S3-compatible, on-prem) for original/final PDFs and signature images.
- Deploy: Docker Compose on-prem, same network as SML + `sml-api-bybos`.
- AI/model usage: none in product runtime.

## Related Repos

- `sml-api-bybos` (`/Users/nontawatwongnuk/dev_bos/sml-api-bybos`) — existing Go/Gin/pgx gateway to SML PostgreSQL. **We extend it** with paperless import + confirm/lock endpoints; we do NOT build a new SML integration. See `docs/sml-integration-notes.md`.

## Read First

- Current state: `docs/current-state.md`
- Architecture: `docs/architecture.md`
- Domain model + workflow rules: `docs/domain.md`
- DB schema: `docs/db-schema.md`
- API contract: `docs/api-contract.md`
- SML integration (open questions / blockers): `docs/sml-integration-notes.md`
- Deploy/runtime: `docs/deploy-instances.md`
- Testing: `docs/testing.md`
- ADRs: `docs/adr/`
- Original requirements (Excel + customer spec): `docs/requirements/`

Read only the files needed for the current task.

## Non-Negotiable Rules

- Never commit real passwords, API keys, tokens, private keys, customer exports, or DB dumps.
- PaperLess must NOT connect to the SML database directly. All SML reads/writes go through `sml-api-bybos`.
- A document is bound to one workflow template **version** for its entire life. Config changes = new version (clone), never mutate an in-use version.
- Audit log is append-only at the system level (app DB role has no UPDATE/DELETE on audit tables).
- For SML calls: design idempotency, retries with backoff, rate-limit handling, partial-failure behavior, and request/response logging. A timeout is never treated as success.
- A document is usable (final PDF downloadable) the moment it is `completed` — it must NOT depend on SML sync succeeding.
- For migrations: define rollback, backup, validation, and production impact.
- For UI: handle empty/loading/error/disabled states clearly; signing must work on iOS Safari + Android Chrome.
- Never log tokens, passwords, or raw signature image binary.

## Domain Invariants (most critical — see docs/domain.md)

- `condition_type = 1` (any-one): first signer to commit wins; other tasks in that step become `skipped` under a row lock.
- `condition_type = 2` (all): step completes only when every assignee has signed.
- `condition_type = 3` (external): an external signer (customer) completes the step via a one-time, expiring token.
- `sequence_no` gates the next step: step N+1 does not open until step N is complete.
- A signature task can be signed at most once; a user may sign only an `open` task assigned to them.

## Graphify Auto-Lite

Use Graphify as a context map for cross-subsystem work, not source of truth. Always open source files before editing; if Graphify disagrees with code/docs, code/docs win. `graphify-out/` stays untracked.

```bash
bash scripts/graphify-update.sh
bash scripts/graphify-query.sh "question or symbol"
```

## Validation

See `docs/testing.md`. Typical gates:

```bash
# Backend
cd apps/api && go test ./...

# Frontend
cd apps/web && npm run build
```

## Default Skill Routing

- Product / PRD / roadmap: `shared-pm-skills`
- Dev / review / refactor / migration / release: `production-engineering`
- UI polish: `impeccable`
