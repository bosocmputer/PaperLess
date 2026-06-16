# Current State

Last updated: 2026-06-16

## Latest Handoff

- Current production/dev state: **Phase 0 — planning/scaffold.** Repo initialized from `template-vibe-code`. Requirements, architecture, domain rules, DB schema, API contract, and SML integration notes are written. **No application code yet.**
- Last completed change: authored `docs/` (architecture, domain, db-schema, api-contract, sml-integration-notes, testing) and `AGENTS.md`.
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
- In progress: documentation/scaffold complete; next is repo skeleton (apps/api, apps/web, workers, migrations, compose).
- Blocked: SML confirm/lock fields (Phase 3 only).
- Next safest step: scaffold `apps/api` (Go, mirror sml-api-bybos layout) + first migration + Docker Compose, then health/ready green before any feature.

## Known Gaps

- Testing: no tests yet; gates defined in `docs/testing.md`.
- Security: auth/RBAC not implemented; append-only DB roles to be set in migrations.
- Observability: logging/metrics plan in blueprint §18, not wired.
- UX: no UI; error states enumerated in `docs/requirements/`.
- Documentation: ADRs not yet written (start with: queue choice = River, SML access via gateway only).
