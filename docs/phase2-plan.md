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

- On import (or when `openNextSequence` reaches an external step), for each
  `condition_type=3` step create **one** `signature_tasks` row with
  `external_signer_id` set and `assigned_user_id` NULL, status `open`.
- The `external_signers` row itself is created when the external signer is
  **invited** (Step 3), not at import — so at import the external task may be
  `waiting` until invited, OR import accepts external-signer contact fields and
  creates the row immediately. **Pick one and document it in `docs/domain.md`.**
  Recommended: create the `external_signers` row at invite time; the external
  task opens (`waiting`→`open`) when the signer is invited and the sequence is reached.
- Keep the Phase 1 invariant: a non-external sequence that yields zero tasks
  still errors (never silently completes). Only `condition_type=3` is exempt.
- **Done when:** importing a doc whose active template has an external step no
  longer 500s/false-completes; the external step sits pending until invited; an
  integration test (gated on `PAPERLESS_TEST_DB`) covers it. Re-activate a
  template with an external step in a **test-only** seed — do NOT make the POP
  pilot template external.

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

- `GET /external/sign/:token` → resolve by `sha256(token)`; return the document
  view payload (doc metadata + original PDF access) **only** if the token is
  valid, unused, unexpired. On bad/expired/used token return a clear,
  non-enumerable error (same shape for "not found" vs "expired" is fine, but the
  UI needs to tell the signer it expired — return a stable `code`).
- `POST /external/sign/:token` → body = `{signature_image_hash, consent_text}`;
  calls `engine.ExternalSign`. Enforce: token valid+unexpired+unused, signature
  present, `request_id` idempotency (reuse the engine's event-dedup).
- Optional OTP gate (behind a flag): `POST /external/sign/:token/otp` issues an
  OTP to the signer's phone/email; `ExternalSign` is refused until
  `otp_verified_at` is set. **Phase 2 may stub OTP delivery** (log to server,
  not to client) but must enforce the verified-gate if the flag is on. Default
  flag OFF for the pilot.
- Rate-limit the public routes (per-token + per-IP) to blunt token brute-force.
- A used or expired token must return the same "cannot sign" state the engine
  raises — never a 500.
- **Done when:** a valid token signs exactly once; reuse → rejected; expired →
  rejected; tampered/long/garbage token → rejected without a stack trace;
  tests cover all four; raw token never appears in logs.

## Step 4 — Final PDF includes the external signer

- The evidence page already lists signers from `signature_events`. Confirm an
  external sign produces a `signature_events` row with `signer_type='external'`
  and the signer's name, so the evidence page renders it. Add the signer's
  verification method (token / OTP) to the evidence row.
- **Done when:** a doc completed with an external signer yields a final PDF whose
  evidence page shows that signer with `external` type + verification method.

## Step 5 — Frontend: public external-sign page (mobile-first PWA)

- Route `/external/[token]`: no app shell / no login. Loads via
  `GET /external/sign/:token`; shows doc PDF (pdf.js, lazy), `SignaturePad`
  (reuse the Phase 1 component — touch-safe, no scroll-while-signing),
  preview-before-submit, consent checkbox with the พ.ร.บ. 2544 text.
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
- [ ] Token stored as SHA-256 hash only; raw token returned once; never in logs (grep).
- [ ] Public sign endpoints carry NO JWT requirement; all other routes still guarded.
- [ ] Valid token signs exactly once; reuse → rejected; expired → rejected; garbage/oversized token → rejected with a clean error (no 500/stack trace).
- [ ] `request_id` idempotency on external sign → one signature_event.
- [ ] Token validation + state re-checked from DB inside the tx; signer row-locked.
- [ ] Rate limiting present on public routes.
- [ ] OTP gate (if flag on) actually blocks signing until verified; delivery not leaked to client.

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
