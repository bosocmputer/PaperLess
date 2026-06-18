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

## Database pool & timeout settings (Phase 3 Step 3b)

| Env var | Default | Notes |
| --- | --- | --- |
| `DB_MAX_CONNS` | `10` | Pool maximum. At 20–100 concurrent signers (pilot scale), 10 conns is comfortable — each sign transaction completes in < 100ms. Raise to 20–30 at scale-out. |
| `DB_MIN_CONNS` | `2` | Kept warm to avoid cold-connection latency on the first request after a quiet period. |
| `DB_STATEMENT_TIMEOUT_MS` | `5000` | Server-side statement_timeout applied via AfterConnect (`SET statement_timeout = N`). Kills stuck OLTP queries before they pin a connection indefinitely. Set to `0` to disable (not recommended in production). FinalizeDocument (PDF generation) does application-level work, not a single long SQL statement, so this timeout does not affect it. |

## Rate-limiter caveat (Phase 3 Step 3a)

The public external-sign endpoints (`/external/*`) use a **per-process in-memory** IP rate limiter (20 req/min per IP). This means:

- **Single-instance pilot**: effective limit is 20 req/min per IP. ✅ Acceptable.
- **Multi-instance behind a load balancer**: effective limit is `N × 20` req/min per IP (each instance tracks its own buckets, not shared). If you scale out, replace the in-memory map with a Redis-backed shared counter (e.g. `INCR + EXPIRE` on a `ratelimit:{ip}` key).

A background janitor evicts stale buckets every 2 minutes so the map doesn't grow unbounded.

## Commands (mirror sml-api-bybos)

```bash
# build + run locally
docker compose up -d --build

# health check
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/health/ready

# migrations (uses golang-migrate, source: apps/api/migrations/)
# dev: run from apps/api/ with DATABASE_URL in env or .env
go run ./cmd/migrate up          # apply all pending migrations
go run ./cmd/migrate down        # roll back all
go run ./cmd/migrate version     # show current schema version
go run ./cmd/migrate force 2     # force version (use after a failed partial migration)

# docker: the api image bakes the migrate binary at /usr/local/bin/migrate and
# bundles migrations at /app/migrations (ENV MIGRATIONS_PATH already set in image).
# Override the entrypoint to run migrate instead of the server:
docker compose run --rm --entrypoint /usr/local/bin/migrate api up

# optional MIGRATIONS_PATH override (for custom deploy layout)
MIGRATIONS_PATH=/path/to/migrations go run ./cmd/migrate up
```

> **Image naming note (verified against `deploy/docker-compose.yml` + Dockerfile):**
> The `api` service uses `build:` only — it has no `image:` tag, so Compose
> auto-names the built image `deploy-api`. There is **no `web` service yet**
> (compose header: "Web (apps/web) is added once the frontend lands"), so the
> rollback below is **git-checkout-based** (rebuild the previous commit), not an
> image-swap. The migrate binary is baked at `/usr/local/bin/migrate`; the
> server entrypoint is `/usr/local/bin/paperless-api`, so migrate runs must
> override the entrypoint. **Recommended hardening before pilot:** add an
> explicit `image: paperless-api:${TAG:-latest}` to the api service so you can
> pin/roll back by tag instead of by git commit.

```bash
# 0. On build machine — gates must be green
cd apps/api && PAPERLESS_TEST_DB=... go test ./... -count=1
cd apps/web && npm run build

# 1. Record the current commit (rollback target), then pull the new code
cd /Users/nontawatwongnuk/dev_bos/paperless     # deploy root
PREV_COMMIT=$(git rev-parse HEAD) && echo "rollback target: $PREV_COMMIT"
git pull origin main

# 2. Take DB backup BEFORE any schema change
cd deploy
docker compose exec -T postgres pg_dump -U postgres paperless \
  | gzip > /var/backups/paperless-$(date +%Y%m%d-%H%M%S).sql.gz

# 3. Build the new api image (no registry pull — image is built locally)
docker compose build api

# 4. Apply pending migrations against the live DB (override entrypoint → migrate)
#    DATABASE_URL + MIGRATIONS_PATH come from the api service env / image.
docker compose run --rm --entrypoint /usr/local/bin/migrate api up
docker compose run --rm --entrypoint /usr/local/bin/migrate api version  # verify

# 5. Bring up new containers (depends_on health gates: postgres + minio healthy)
docker compose up -d

# 6. Smoke test
sleep 5
curl -fsS http://localhost:8080/health/ready   # must be {"status":"ok","database":"ok","storage":"ok"}
../scripts/smoke.sh http://localhost:8080      # must exit 0
```

## Rollback procedure (revert a deploy that failed smoke or is misbehaving)

```bash
cd /Users/nontawatwongnuk/dev_bos/paperless

# 1. Roll back the schema FIRST if (and only if) this deploy added a migration.
#    WARNING: `migrate down` with no argument reverts ALL migrations to an empty
#    DB (golang-migrate m.Down()). The migrate binary exposes no "down one step".
#    To revert exactly one migration safely, restore from the pre-deploy backup
#    (step 3) instead, OR hand-run the specific .down.sql for that version.
#    For Step-1's 0006 (drop one partial index) the down is safe and isolated:
cd deploy
docker compose run --rm --entrypoint /usr/local/bin/migrate api force 6   # if dirty
# (do NOT run bare `migrate down` in prod unless you intend a full teardown)

# 2. Rebuild + run the PREVIOUS code
cd /Users/nontawatwongnuk/dev_bos/paperless
git checkout "$PREV_COMMIT"        # the commit recorded in deploy step 1
cd deploy
docker compose build api
docker compose up -d

# 3. Verify
curl -fsS http://localhost:8080/health/ready
../scripts/smoke.sh http://localhost:8080

# 4. If DB data is corrupt (a data migration went wrong): restore from backup.
#    This is the safest rollback for any non-trivial schema/data change.
docker compose stop api
gunzip -c /var/backups/paperless-<TIMESTAMP>.sql.gz \
  | docker compose exec -T postgres psql -U postgres -d paperless
docker compose up -d
```

**Rollback timing (rehearsed 2026-06-17, Opus, on a throwaway Postgres+MinIO stack):**

- `/health/ready` flips to 503 `storage=error` within the 3s check window when MinIO is stopped; recovers to 200 automatically when MinIO restarts (no API restart needed) — verified live.
- `migrate` up→down→up on schema v6 (0006 partial index): each step < 1s.
- Image rebuild (`docker compose build api`, cached layers): ~5–15s.
- Smoke re-run: ~30s.
- Total realistic rollback (git checkout + rebuild + smoke): ~2–3 min. Backup restore adds ~1 min per 100 MB of dump.

## Required env vars (production checklist)

All values live in the deploy environment's `.env` (untracked). Confirm each is
set before deploying. None should ever be committed to git.

```text
# REQUIRED — deploy will not start without these
DATABASE_URL=postgres://...           # PaperLess DB (NOT SML)
JWT_SECRET=...                        # ≥ 32 random bytes, hex or base64
MINIO_ACCESS_KEY=...
MINIO_SECRET_KEY=...

# REQUIRED for SML gateway (mock if SML not yet live)
SML_API_BASE_URL=http://192.168.2.109:8200
SML_API_KEY=...
SML_TENANT=...

# OPTIONAL (have defaults; tune for production)
APP_PORT=8080
MINIO_ENDPOINT=minio:9000             # default: localhost:9000
MINIO_BUCKET=paperless                # default: paperless
MINIO_USE_SSL=false                   # set true if MinIO behind TLS terminator
DB_MAX_CONNS=10                       # raise to 20–30 at scale-out
DB_MIN_CONNS=2
DB_STATEMENT_TIMEOUT_MS=5000          # 5s default; set to 0 to disable (not recommended)
```

**Pre-deploy env check (run on server before deploying):**

```bash
# Verifies all required vars are present in the api container's environment.
# Must override the entrypoint (the image entrypoint is paperless-api, not a shell).
cd deploy
for var in DATABASE_URL JWT_SECRET MINIO_ACCESS_KEY MINIO_SECRET_KEY \
           SML_API_BASE_URL SML_API_KEY SML_TENANT; do
  val=$(docker compose run --rm --entrypoint printenv api "$var" 2>/dev/null)
  [ -n "$val" ] && echo "✅ $var" || echo "❌ MISSING: $var"
done
```

## Release Checklist

- [ ] Worktree clean before deploy (`git status` clean).
- [ ] `go test ./...` (with `PAPERLESS_TEST_DB`) + `npm run build` both green.
- [ ] New migration reviewed: `up`/`down` dry-run on a copy of prod DB, schema version correct.
- [ ] DB backup taken immediately before `migrate up` on production.
- [ ] All required env vars present in deploy environment — run env check above.
- [ ] No secrets committed; `.env` is untracked (`git ls-files .env` returns empty).
- [ ] Rollback target recorded: `PREV_COMMIT=$(git rev-parse HEAD)` captured before `git pull` (the api image is built locally, not pulled — rollback is git-checkout + rebuild).
- [ ] `/health/ready` returns `{"status":"ok","database":"ok","storage":"ok"}` after deploy.
- [ ] `scripts/smoke.sh` exits 0 after deploy (pre-deploy gate, see `docs/testing.md`).
- [ ] Log scan clean: no token/password/OTP/signature binary in API logs.
