# Phase 2 Work Plan — External Signer Flow + Frontend Hardening (risk-ordered)

Phase 2 closes the one functional gap the Phase 1 audit found (condition_type=3
external signer is engine-ready but has no import/link/sign wiring) and hardens
the web client so the POP pilot is signable on a real phone. **No SML dependency**
— SML stays mocked behind `SmlDocumentGateway`; Phase 3 is still blocked on
`docs/sml-questions.md`.

**Read first:** `AGENTS.md`, `docs/domain.md` (§condition_type=3 — the spec),
`docs/db-schema.md` (`external_signers` table), `docs/current-state.md` (audit
results + known scope boundary), `apps/api/internal/workflow/engine.go`
(`ExternalSign` already exists and is tested). **Mirror existing conventions**
exactly: zap, request-id, `httpx` envelope, `strconv.FormatInt` for id→text (NOT
`$N::text` — see the Phase 1 audit fixes), table-driven tests, `go test ./...`.

### What already works (do NOT rebuild)

- `workflow.Engine.ExternalSign(ctx, taskID, tokenHash, in)` — validates token,
  checks expiry (`ErrExternalTokenExpired`), row-locks the signer (`FOR UPDATE OF es`),
  marks signed, writes the event, advances the sequence, completes the doc.
  Tested by `TestExternalToken_Expired`.
- `external_signers` table: `token_hash` (UNIQUE), `token_expires_at`,
  `otp_verified_at`, `status` (`pending|signed|expired|cancelled`).
- `createTasksForSequence` currently **errors** when a sequence has an external
  step (the Phase 1 guard). Step 1 changes that to create the external task.

Work in this order. Backend before UI; the external-sign endpoint must be tested
before the public sign page is built.

---

## Step 1 — Import creates the external-signer task

The Phase 1 guard in `createTasksForSequence` aborts when a sequence yields zero
assignee tasks. Replace "abort" with "create an external task" for
`condition_type=3` steps.

**Decision (do NOT re-litigate):** the `external_signers` row is created at
**invite** time (Step 2), not at import. At import/sequence-open, the external
step's `signature_tasks` row is created with status **`waiting`** and
`external_signer_id` NULL. The invite (Step 2) sets `external_signer_id` and
flips the task `waiting`→`open`. This keeps "who the external signer is" out of
the import payload and makes invite the single place a signer identity is born.

- For each `condition_type=3` step in the opening sequence, create **one**
  `signature_tasks` row: `assigned_user_id` NULL, `external_signer_id` NULL,
  `condition_type=3`, status `waiting`.
- Keep the Phase 1 invariant: a non-external sequence that yields zero tasks
  still errors (never silently completes). Only `condition_type=3` is exempt —
  it produces a `waiting` task instead of erroring.
- **Document-completion impact:** `isDocumentComplete` must NOT treat a `waiting`
  external task as terminal (it already excludes non-terminal statuses — confirm
  `waiting` is excluded so an un-invited external step keeps the doc pending).
- **Done when:** importing a doc whose active template has an external step no
  longer 500s/false-completes; the external step sits `waiting` (doc stays
  `pending`) until invited; an integration test (gated on `PAPERLESS_TEST_DB`)
  covers it. Use a **test-only** template with an external step (or activate
  `DEMO3` inside the test tx) — do NOT make the POP pilot template external.

## Step 2 — Token issuance + invite (internal API)  ← SECURITY-SENSITIVE

- `POST /documents/:id/external-signers` (auth: document_admin): body =
  `{name, email?, phone?, expires_in_hours?}`. Generates a **cryptographically
  random** token (≥32 bytes), stores **only its SHA-256 hash** in
  `external_signers.token_hash`, returns the **raw token once** in the response
  (for the admin to deliver). Default expiry 72h; cap it.
- Opens/links the external `signature_tasks` row to this `external_signers.id`.
- Audit the invite (`external_signer` entity). **Never log the raw token.**
- `GET /documents/:id/external-signers` (auth): list signers + status (never the
  token or its hash).
- **Done when:** invite returns a one-time raw token; DB stores only the hash;
  re-reading the signer never exposes the token; test covers hash-only storage.

## Step 3 — Public external-sign endpoints (NO JWT — token-authenticated)  ← HIGHEST RISK

These are the only unauthenticated routes. Treat every input as hostile.

**Token transport:** the token is a bearer credential — do NOT put it in the URL
path or query string (it leaks into access logs, proxies, browser history,
Referer headers). The browser holds it in memory after the first load and sends
it as a header: `X-Signer-Token: <raw>`. The first navigation can use a path
(`/external/[token]` page route, Step 5) but every **API** call carries the token
in the header, not the path. So the endpoints are:

- `GET /external/document` (header `X-Signer-Token`) → resolve by
  `sha256(token)`; return the document view payload (doc metadata + a way to view
  the original PDF — see below) **only** if the token is valid, unused,
  unexpired. On bad/expired/used token return a stable machine `code`
  (`external_link_expired` / `external_link_used` / `external_link_invalid`) with
  a generic message; do not reveal whether a doc exists.
- **Original PDF for the external signer:** add
  `GET /external/document/file/original` (header `X-Signer-Token`) that streams
  the original PDF **only** after the same token check. Do NOT reuse the
  auth-only `/documents/:id/file/original`, and do NOT expose a public
  doc-id route. (If you prefer a short-lived MinIO presigned URL instead of
  streaming, that is acceptable — but the presign must be minted only after the
  token check and expire in minutes.)
- `POST /external/sign` (header `X-Signer-Token`) → body =
  `{signature_image_hash, consent_text}`; calls `engine.ExternalSign`. Enforce:
  token valid+unexpired+unused, signature present, `request_id` idempotency
  (reuse the engine's event-dedup).
- Optional OTP gate (behind a flag): `POST /external/sign/otp` (header
  `X-Signer-Token`) issues an OTP to the signer's phone/email; `ExternalSign` is
  refused until `otp_verified_at` is set. **Phase 2 may stub OTP delivery** (log
  to server, **never** return the OTP in the response) but must enforce the
  verified-gate if the flag is on. Default flag OFF for the pilot.
- **Rate-limit the public routes** (per-token + per-IP). Token lookup is by
  `sha256(token)` against a UNIQUE index — constant-ish, but still cap attempts
  per IP (e.g. simple in-memory token-bucket or a `failed_attempts` counter) so a
  stolen-link-guessing client is throttled. A simple per-IP limiter is enough for
  the pilot; note it explicitly rather than leaving the routes unbounded.
- A used or expired token must return the same "cannot sign" state the engine
  raises — never a 500.
- **Done when:** a valid token signs exactly once; reuse → rejected; expired →
  rejected; tampered/long/garbage token → rejected without a stack trace;
  rate-limit triggers under repeated bad tokens; tests cover all of these; raw
  token never appears in logs or any response body.

## Step 4 — Final PDF includes the external signer

- The evidence page already lists signers from `signature_events`. Confirm an
  external sign produces a `signature_events` row with `signer_type='external'`
  and the signer's name, so the evidence page renders it. Add the signer's
  verification method (token / OTP) to the evidence row.
- **Done when:** a doc completed with an external signer yields a final PDF whose
  evidence page shows that signer with `external` type + verification method.

## Step 5 — Frontend: public external-sign page (mobile-first PWA)

- Route `/external/[token]`: no app shell / no login. On load, read the token
  from the path **once**, keep it in component state/memory, and send it on every
  API call as the `X-Signer-Token` header (calls hit `/external/document`,
  `/external/document/file/original`, `/external/sign`). Do not echo the token
  into other URLs. Shows doc PDF (pdf.js, lazy), `SignaturePad` (reuse the Phase
  1 component — touch-safe, no scroll-while-signing), preview-before-submit,
  consent checkbox with the พ.ร.บ. 2544 text.
- Explicit error states (reuse `ErrorState`): `external_link_expired`,
  `external_link_used`, `external_signer_info_missing`, `signature_required`,
  network-drop-during-submit (show "กำลังตรวจสอบสถานะ", rely on `request_id`).
- **Done when:** the external flow is signable end-to-end on a real phone from a
  link alone; each error state is reachable.

## Step 6 — Frontend hardening (internal app) + contract reconciliation

- Wire the web client against the **running** API (not mocks). Fix any
  request/response contract drift found (the audit already flagged
  `StepProgress` — re-verify all envelopes match `lib/api.ts`).
- Confirm the 13 Phase 1 error states are each reachable; add the 4 external
  ones.
- Manual QA matrix: iOS Safari + Android Chrome, portrait + landscape; page must
  not scroll while signing; PDF zoom/pan; clear-signature confirm; preview
  before submit. Record results in `docs/testing.md` → Manual QA.
- **Done when:** the POP internal flow AND the external flow are both green on a
  real iOS and a real Android device; `npm run build` clean.

---

## Guardrails (carried from Phase 1 — non-negotiable)

- Public external routes are the only unauthenticated endpoints. Everything else
  stays behind `RequireAuth`/`RequireRole`.
- Store only the **hash** of the external token; return the raw token once, never
  log it, never store it.
- A timeout / used / expired token is never a 500 and never a silent success.
- id→text conversions use `strconv.FormatInt` in Go, never `$N::text` (Phase 1
  audit bug). Read-path timestamptz/numeric → cast `::text` in SQL when scanning
  into `*string`.
- Re-check token + task state from the DB inside the transaction; never trust the
  client. The engine already row-locks the signer — keep it.
- Don't edit an applied migration; add a new one. Don't mutate an in-use
  template version. SML stays behind `SmlDocumentGateway` (mock).
- After each step: `go build ./...`, `go vet ./...`, and `go test ./...` **with
  `PAPERLESS_TEST_DB` set** (a skipped suite is NOT a pass — Phase 1 audit
  lesson). Update `docs/current-state.md`.

## Deferred to Phase 3 (still waiting on `docs/sml-questions.md`)

- Real SML confirm/lock sync + reconciliation (Q1–Q4, Q7).
- Real OTP delivery provider (Q7, Q10) — Phase 2 stubs delivery behind a flag.
- Coordinate-stamped signatures on the PDF (Q8) — evidence page remains default.
- Document chain rendering (Q5).

---

## Audit Checklist (Opus runs this against the delivered code — real DB, not skipped)

### Build & quality gates
- [ ] `go build ./...`, `go vet ./...` clean.
- [ ] `go test ./...` green **with `PAPERLESS_TEST_DB` set** (paste output; confirm external tests ran, not skipped).
- [ ] `npm run build` clean.
- [ ] Migrations up→down→up clean on a throwaway DB; new migration only (0001–0005 untouched).

### External signer — security (highest risk)
- [ ] Token stored as SHA-256 hash only; raw token returned once; never in logs/response bodies (grep).
- [ ] Token carried in `X-Signer-Token` header, NOT in the API URL path/query (won't leak to access logs/Referer). Page route `/external/[token]` is the only place the raw token sits in a URL.
- [ ] Public original-PDF endpoint streams only after the token check; the auth-only `/documents/:id/file/original` is NOT reused and no public doc-id route exists.
- [ ] Public sign endpoints carry NO JWT requirement; all other routes still guarded.
- [ ] Valid token signs exactly once; reuse → rejected; expired → rejected; garbage/oversized token → rejected with a clean error (no 500/stack trace).
- [ ] `request_id` idempotency on external sign → one signature_event.
- [ ] Token validation + state re-checked from DB inside the tx; signer row-locked.
- [ ] Rate limiting present on public routes (per-IP at minimum); triggers under repeated bad tokens.
- [ ] OTP gate (if flag on) actually blocks signing until verified; OTP never returned in a response.

### External signer — correctness
- [ ] Import no longer false-completes a doc with an external step (Phase 1 bug stays fixed); non-external zero-task sequence still errors.
- [ ] External task created with `external_signer_id` set, `assigned_user_id` NULL.
- [ ] Completing a doc via external sign advances the sequence and completes correctly.
- [ ] Evidence page / final PDF shows the external signer with `external` type + verification method.

### Frontend
- [ ] `/external/[token]` signable end-to-end on a real phone from a link alone.
- [ ] All error states reachable (4 external + 13 internal).
- [ ] No request/response contract drift vs `lib/api.ts` (re-verify after StepProgress fix).
- [ ] Manual QA recorded for iOS Safari + Android Chrome (no scroll-while-signing).

### Invariants (carried)
- [ ] No applied migration edited; no in-use template mutated.
- [ ] SML only behind `SmlDocumentGateway` (mock); no direct SML calls.
- [ ] No secrets committed; `.env` untracked.
- [ ] `completed` doc downloadable independent of SML sync.
