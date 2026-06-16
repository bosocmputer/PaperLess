# Deploy Instances

Do not store real passwords, tokens, or private keys here. Secrets live in the deploy environment's `.env` (untracked) only.

## Environments

| Environment | URL | Deploy path | Branch | Notes |
| --- | --- | --- | --- | --- |
| local | `http://localhost:3000` (web), `:8080` (api) | `/Users/nontawatwongnuk/dev_bos/paperless` | `main` | Docker Compose; MinIO + Postgres local |
| production | `http://{lan-host}:3000` (TBD) | TBD (on-prem) | `main` | Same LAN as `sml-api-bybos` (`192.168.2.109:8200`) and SML (`192.168.2.248:5432`) |

## Required env (names only — values in untracked .env)

```text
# api
APP_PORT=8080
DATABASE_URL=postgres://...           # PaperLess DB (NOT SML)
JWT_SECRET=...
MINIO_ENDPOINT=... MINIO_ACCESS_KEY=... MINIO_SECRET_KEY=... MINIO_BUCKET=paperless
SML_API_BASE_URL=http://192.168.2.109:8200
SML_API_KEY=...                        # X-Api-Key for sml-api-bybos
SML_TENANT=...                         # X-Tenant
```

## Commands (planned, mirror sml-api-bybos)

```bash
# build + run locally
docker compose up -d --build

# health check
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/health/ready

# migrate
docker compose run --rm api ./migrate up      # reversible; ./migrate down to roll back
```

## Release Checklist

- Worktree clean before deploy.
- `go test ./...` + `npm run build` passed.
- Migrations reviewed, reversible (`up`/`down`), dry-run on a DB copy.
- DB backup taken before migrating production.
- Required env vars/secrets present in deploy environment (none committed).
- Rollback path known (previous image via Compose; `migrate down` + restore backup).
- Smoke test defined (`docs/testing.md` → Production smoke).
- Logs verified to contain no token/password/signature binary.
