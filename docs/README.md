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
- `requirements/` — original customer Excel spec + design docs
- `adr/` — architecture decisions

## Context Rules

- Do not store real secrets in docs.
- Prefer stable facts over chat/session transcripts.
- Move old handoff notes into dated sections inside `current-state.md`.
- Keep docs accurate after behavior, deploy, or schema changes.
