# Pilot Prep Plan — merge Phase 5 + close seed-password blocker (Opus plan, Sonnet implements)

**Goal:** get PaperLess pilot-ready WITHOUT SML. Two pieces:
1. **B — Merge** `docs/sml-followup-answers` → `main` (7 commits, already pushed, no conflicts).
2. **C1 — Seed-password blocker:** prod must NOT carry the committed dev hash
   (`password123`). Add migration `0007` that clears the dev hash, AND a deploy
   script that lets the operator set a real admin password (bcrypt, never in git).

**Out of scope (do NOT touch):** SSR-500 (already fixed 2026-06-18 — signer page
is `"use client"`, no SSR fetch), cloudflared (infra runbook, not code), device QA
(human-run), anything needing SML answers (G3 chain, PDF samples, Q8 coords).

**Read first:** `docs/current-state.md` (deploy path), this file.
**Conventions:** migrations are append-only — `0001`–`0006` are FROZEN, never edit
them; new file is `0007`. `down.sql` must cleanly reverse `up.sql`. bcrypt via the
same lib the API uses (`golang.org/x/crypto/bcrypt`). No secrets committed; the
script reads the password interactively or from an env var, never hardcodes it.

---

## หลักฐาน (verified โดย Opus — Sonnet ไม่ต้องค้นซ้ำ)

**Seed password — the blocker** (`apps/api/migrations/0004_seed_dev_passwords.up.sql`):
```sql
-- DEV ONLY. Password for all accounts: password123
UPDATE users SET password_hash = '$2a$10$HLSosIQLc/83FArbeaXMh.V91QFOHRgk/8Nd7HcE3V9OPye8faFFO'
WHERE username IN ('admin', 'maker', 'checkerA', 'checkerB', 'approver');
```
This bcrypt hash IS committed to git. Any prod that runs migrations up to `0004`
has all 5 accounts logging in with `password123`. **This is the pilot blocker.**

**Migration numbering — `0007` is next.** `0006` is ALREADY USED
(`0006_request_id_unique`). Latest files:
```
0004_seed_dev_passwords.{up,down}.sql
0005_seed_pop_external_step.{up,down}.sql
0006_request_id_unique.{up,down}.sql   ← highest existing
```
→ New migration MUST be `0007_*`. Do NOT reuse 0006.

**Migrate tooling** (`apps/api/cmd/migrate/`): golang-migrate style, numbered
`up.sql`/`down.sql` pairs. `0007_clear_dev_passwords.down.sql` should restore the
known dev hash (so `migrate down` on a dev box re-enables `password123` for UAT) —
mirror the exact UPDATE from `0004` in the down file.

**bcrypt lib** — the API already depends on `golang.org/x/crypto/bcrypt` (used in
`internal/auth`). Reuse it for the password-set tool. Cost factor 10 matches the
existing seed hash (`$2a$10$`).

**Branch state:** `docs/sml-followup-answers` is `ahead 1` of origin already
pushed; `git log main..docs/sml-followup-answers` = 7 commits; `git merge-tree`
shows NO conflicts with `main`. Working tree clean.

**The 5 seed users** are dev fixtures (maker/checkerA/checkerB/approver) + `admin`.
For pilot, only `admin` needs a real login; the 4 workflow fixtures are POP-demo
users — clearing their hash to NULL is fine (a NULL hash must fail login, see
"verify" below — confirm the login path rejects NULL/empty hash, do not assume).

---

## ไฟล์ที่ต้องแก้/สร้าง

### B — Merge (do this FIRST, before C1, so C1 lands on main)
1. `git checkout main && git pull` (fast-forward to origin/main).
2. `git merge --no-ff docs/sml-followup-answers` — keep a merge commit documenting
   the Phase 5 + SML-followup work. Message:
   `Merge Phase 5 (SML lock sync) + SML follow-up docs`.
3. Do NOT delete the branch yet (G3/PDF follow-ups may still append there).
4. `git push origin main`.
5. **Before pushing:** run the full suite green on `main` (see Done-when). If merge
   surfaces anything, STOP and report — do not force.

### C1 — Seed-password blocker (on main, after merge)
2. **`apps/api/migrations/0007_clear_dev_passwords.up.sql`** (new):
   ```sql
   -- 0007: prod safety — clear the committed dev password hash so no account
   -- ships with the known password123. Operator sets a real admin password
   -- post-deploy via scripts/set-admin-password.sh. Dev/UAT can `migrate down`
   -- one step to restore the demo hash (see .down.sql).
   BEGIN;
   UPDATE users SET password_hash = NULL
   WHERE username IN ('admin','maker','checkerA','checkerB','approver');
   COMMIT;
   ```
3. **`apps/api/migrations/0007_clear_dev_passwords.down.sql`** (new) — restore the
   exact dev hash from `0004` (copy the literal hash + the same WHERE clause).

4. **`scripts/set-admin-password.sh`** (new) — operator runs once after deploy.
   - Reads password from `$ADMIN_PASSWORD` env if set, else `read -s` prompt
     (never echo it; never pass on the command line where it lands in `ps`/history).
   - Computes a bcrypt hash and UPDATEs `users` for a target username (default
     `admin`, overridable by `$1`). Two options for hashing — pick the one that
     keeps the hash off disk and out of logs:
     - **Preferred:** a tiny Go helper invoked by the script
       (`apps/api/cmd/hashpw/main.go`, new) that reads the password from stdin,
       prints ONLY the bcrypt hash to stdout. Script pipes the read password in
       and captures the hash. Reuses `golang.org/x/crypto/bcrypt` (cost 10).
     - Avoid `htpasswd`/`python` deps — the box already has Go.
   - UPDATE via `psql "$DATABASE_URL"` (the deploy env already exports it).
     Use a parameterized one-shot: `psql -v hash="$HASH" -v u="$USER" -c
     "UPDATE users SET password_hash = :'hash' WHERE username = :'u';"` so the
     hash isn't string-concatenated into SQL.
   - Print a success line WITHOUT the hash or password. Exit non-zero on any failure.
   - `set -euo pipefail`; guard that `DATABASE_URL` is set.

5. **`apps/api/cmd/hashpw/main.go`** (new, if taking the preferred path) — reads
   one line from stdin, `bcrypt.GenerateFromPassword([]byte(pw), 10)`, prints the
   hash. No flags that echo the password. Trim the trailing newline from stdin.

6. **`docs/current-state.md`** — add a short "Pilot deploy: set admin password"
   note pointing at `scripts/set-admin-password.sh`, and mark the seed-password
   blocker resolved. Keep it factual; no secrets.

---

## Decisions ตัดสินแล้ว (Sonnet ห้ามเปลี่ยน)

- **User chose BOTH** approaches: migration `0007` clears the dev hash (prod has no
  `password123`) AND a deploy script sets a real admin password. Do both.
- **Migration is `0007`** — `0006` is taken. Never edit `0001`–`0006`.
- **`down.sql` restores the dev hash** so a UAT box can revert one step and keep
  using `password123` for demos. Prod simply never runs `down`.
- **Password never touches git, logs, argv, or disk.** stdin or env only; bcrypt
  via the Go lib; parameterized psql.
- **Merge with `--no-ff`** (preserve a merge commit); do NOT delete the branch.
- **Merge BEFORE writing 0007** so the new migration lands on `main` in one place.
- **Do NOT change the login/auth code** unless the NULL-hash verify (below) shows
  a NULL hash is treated as a valid/empty password — if so, that IS a real bug:
  fix the login path to reject NULL/empty `password_hash` (fail closed) and add a
  test. Report it explicitly either way.

## Done when

- `git log main..` shows the merge landed; full suite on `main` green:
  `cd apps/api && PAPERLESS_TEST_DB=<dsn> go test -race -count=2 ./...` (0 skips on
  gated paths), `go build ./...`, `go vet ./...`, and `cd apps/web && npm run build`
  all clean. (DSN: server Postgres @ `192.168.2.109:54320` — operator provides;
  local has no Docker, tests run against the remote DB — see deploy notes.)
- `migrate up` to `0007` then a fresh login attempt with `password123` for `admin`
  **fails** (verify against the test DB; restore afterward). `migrate down` one
  step then `password123` works again.
- `scripts/set-admin-password.sh` with `ADMIN_PASSWORD=<temp>` sets a hash; logging
  in with `<temp>` succeeds and with `password123` fails. The hash/password appears
  nowhere in script output, shell history, or logs.
- NULL-hash login behavior verified and documented (rejected = good; accepted =
  bug fixed + test added).

## ห้าม / ระวัง (invariants)

- Never edit migrations `0001`–`0006`. New work is `0007` only.
- No secret (password, hash) in git, argv, logs, or committed files. The committed
  `0004` hash is dev-only and stays as historical migration — do NOT delete `0004`.
- Login must fail closed on NULL/empty `password_hash` — confirm, don't assume.
- The script must be idempotent and safe to re-run (re-setting the password is fine).
- Do NOT start on C2 (cloudflared) or device QA — out of scope for this plan.
- If the merge or any test is not green, STOP and report; do not push a red `main`.
