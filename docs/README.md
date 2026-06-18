# Project Docs Index

Use docs as on-demand context. Keep `AGENTS.md` short and point here for details.

## Start Here

- `current-state.md` — latest handoff, runtime state, known gaps
- `architecture.md` — components, data flow, boundaries
- `domain.md` — product concepts, workflow rules (condition 1/2/3), state machine
- `db-schema.md` — tables, indexes, constraints (schema contract)
- `api-contract.md` — PaperLess API + sml-api-bybos extensions
- `sml-integration-notes.md` — open questions / blockers for the SML team
- `deploy-instances.md` — environments, ports, deploy commands
- `testing.md` — test commands and acceptance criteria
- `phase1-plan.md` — Phase 1 build plan + audit checklist (done, audited PASS)
- `phase2-plan.md` — Phase 2 build plan: external signer flow + frontend hardening (done, audited PASS)
- `phase3-pilot-readiness.md` — Pilot track: prod hardening (data integrity, perf, ops) + admin invite UI (next; no SML dependency)
- `sml-questions.md` — fill-in form for the SML team (gates Phase 3)
- `requirements/` — original customer Excel spec + design docs
- `adr/` — architecture decisions

## Context Rules

- Do not store real secrets in docs.
- Prefer stable facts over chat/session transcripts.
- Move old handoff notes into dated sections inside `current-state.md`.
- Keep docs accurate after behavior, deploy, or schema changes.
