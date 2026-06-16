# Vibe Code Project Template Usage

## New Project Setup

1. Create a new repository from this template.
2. Fill `AGENTS.md` with a short project index.
3. Fill `docs/current-state.md`, `docs/architecture.md`, `docs/domain.md`, `docs/deploy-instances.md`, and `docs/testing.md`.
4. Install Graphify if needed:

   ```bash
   uv tool install graphifyy==0.8.35
   ```

5. Build the first local graph:

   ```bash
   bash scripts/graphify-update.sh
   bash scripts/graphify-query.sh "main architecture"
   ```

## Rules

- Keep `AGENTS.md` short and stable.
- Put long-lived facts in docs.
- Keep generated `graphify-out/` local-only.
- Never store real secrets in tracked files.
- Commit context/tooling changes separately from product code changes.
