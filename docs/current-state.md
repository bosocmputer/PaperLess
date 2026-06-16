# Current State

Last updated: 2026-06-16

## Latest Handoff

- Current production/dev state: **Phase 0 — scaffold landed.** Repo initialized from `template-vibe-code`; docs written; Go API skeleton compiles and serves `/health` + `/health/ready`; core schema migration + dev seed verified (up/down) against a local Postgres.
- Last completed change: scaffolded `apps/api` (Go/Gin/pgx, mirroring sml-api-bybos conventions: zap, request-id, `{success,data,meta}` envelope), `migrations/0001_init` + `0002_seed_dev` (POP workflow from the Excel example), `deploy/docker-compose.yml`, Dockerfile. Verified: migrations apply + reverse cleanly, partial-unique "one active version per doc format" enforced, `/health/ready` returns DB ok.
- Current branch/release: `main` (fresh `git init`, not yet committed).
- Known broken or risky areas: SML Confirm/Lock table/field unknown — blocks Phase 3 sync, not Phase 1/2 (mock gateway).

## Runtime Snapshot

- Local path: `/Users/nontawatwongnuk/dev_bos/paperless`
- Server/deploy path: TBD (on-prem, same LAN as `sml-api-bybos` @ `192.168.2.109`)
- Public URL: none (on-prem LAN only)
- Ports (planned): web `3000`, api `8080`, postgres `5432`, MinIO `9000/9001`
- Containers/services (planned): `web`, `api`, `worker`, `postgres`, `minio`

## Active Work

- Goal: stand up Phase 1 pilot — manual upload, workflow config (POP/INV), inbox, mobile signing (condition 1/2/3), audit, final PDF.
- In progress: API rail is up. Next: pick a migration runner (e.g. golang-migrate) and wire `migrate up/down`; then auth + RBAC; then document import (idempotency_key + source_hash) and the workflow engine.
- Blocked: SML confirm/lock fields (Phase 3 only).
- Next safest step: add the migration runner + a `go test` for the workflow condition-1 race (highest-risk logic), then build import → inbox → sign vertically against the POP seed.

## Known Gaps

- Testing: no tests yet; gates defined in `docs/testing.md`.
- Security: auth/RBAC not implemented; append-only DB roles to be set in migrations.
- Observability: logging/metrics plan in blueprint §18, not wired.
- UX: no UI; error states enumerated in `docs/requirements/`.
- Documentation: ADRs not yet written (start with: queue choice = River, SML access via gateway only).
