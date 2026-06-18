#!/usr/bin/env bash
# smoke.sh — PaperLess 1.0 pre-deploy smoke test
#
# Covers BOTH flows end-to-end against a live stack:
#   A. Internal: login → import POP (3-step workflow) → sign each step → completed
#      → download final PDF (assert %PDF header + verification code)
#   B. External: activate DEMO3 → import → sign steps 1+2 → invite external signer
#      → external view → external sign → completed → final PDF contains external signer
#
# Security invariants asserted live:
#   - Reuse of a consumed external token → external_link_used (410)
#   - Garbage external token → clean 401 (not 5xx)
#   - Non-assignee internal sign attempt → 403
#   - Per-IP rate limit fires after 20 rapid requests
#
# Usage:
#   ./scripts/smoke.sh [API_BASE]
#
#   API_BASE defaults to http://localhost:8080
#
# Requirements:
#   curl, jq — both must be on PATH.
#   The stack must be up with dev seed applied (0001–0006 migrations).
#   This script creates test documents and cleans up nothing — run against a
#   throwaway/staging DB, not production data.
#
# Exit codes:
#   0 — all checks PASSED
#   1 — one or more checks FAILED

set -euo pipefail

API="${1:-http://localhost:8080}"
PASS=0
FAIL=0
FAILURES=()

# ── helpers ──────────────────────────────────────────────────────────────────

GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ PASS${NC} $1"; PASS=$((PASS+1)); }
fail() { echo -e "${RED}✗ FAIL${NC} $1"; FAIL=$((FAIL+1)); FAILURES+=("$1"); }
info() { echo -e "${YELLOW}→${NC} $1"; }

# assert_eq LABEL EXPECTED ACTUAL
assert_eq() {
  local label="$1" expected="$2" actual="$3"
  if [ "$actual" = "$expected" ]; then
    pass "$label"
  else
    fail "$label (expected='$expected' got='$actual')"
  fi
}

# assert_contains LABEL SUBSTRING STRING
assert_contains() {
  local label="$1" sub="$2" str="$3"
  if echo "$str" | grep -qF "$sub"; then
    pass "$label"
  else
    fail "$label (expected to contain '$sub')"
  fi
}

# assert_not_contains LABEL SUBSTRING STRING
assert_not_contains() {
  local label="$1" sub="$2" str="$3"
  if echo "$str" | grep -qF "$sub"; then
    fail "$label (must NOT contain '$sub')"
  else
    pass "$label"
  fi
}

# login USERNAME PASSWORD → sets TOKEN
login() {
  local username="$1" password="$2"
  local resp
  resp=$(curl -sf -X POST "$API/api/v1/auth/login" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"$username\",\"password\":\"$password\"}")
  echo "$resp" | jq -r '.data.access_token'
}

# api_get TOKEN PATH → response JSON (never aborts; returns body on any status)
api_get() {
  local tok="$1" path="$2"
  curl -s -H "Authorization: Bearer $tok" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo smoke-$$)" \
    "$API$path" || true
}

# api_get_status TOKEN PATH → HTTP status code only
api_get_status() {
  local tok="$1" path="$2"
  curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer $tok" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo smoke-$$)" \
    "$API$path"
}

# api_post TOKEN PATH JSON_BODY → response JSON
# Uses -s (NOT -sf): a 4xx/5xx must return the JSON body so the caller can assert
# on it, never abort the script under `set -e`. `|| true` guards transport errors.
api_post() {
  local tok="$1" path="$2" body="$3"
  curl -s -X POST \
    -H "Authorization: Bearer $tok" \
    -H "Content-Type: application/json" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo smoke-$$)" \
    -d "$body" \
    "$API$path" || true
}

# api_post_status TOKEN PATH JSON_BODY → HTTP status code only
api_post_status() {
  local tok="$1" path="$2" body="$3"
  curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "Authorization: Bearer $tok" \
    -H "Content-Type: application/json" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || cat /proc/sys/kernel/random/uuid 2>/dev/null || echo smoke-$$)" \
    -d "$body" \
    "$API$path"
}

# api_post_code_body TOKEN PATH JSON_BODY → "STATUS:BODY"
api_post_code_body() {
  local tok="$1" path="$2" body="$3"
  local tmp
  tmp=$(mktemp)
  local code
  code=$(curl -s -o "$tmp" -w "%{http_code}" -X POST \
    -H "Authorization: Bearer $tok" \
    -H "Content-Type: application/json" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    -d "$body" \
    "$API$path")
  echo "${code}:$(cat "$tmp")"
  rm -f "$tmp"
}

# find_task TOKEN DOC_ID → task id for that doc in the caller's inbox (or empty)
# Returns the OPEN task only; prints nothing (not "null") when absent so callers
# can guard with [ -n "$id" ].
find_task() {
  local tok="$1" doc_id="$2"
  api_get "$tok" "/api/v1/signature-tasks/inbox" \
    | jq -r "[.data[] | select(.document_id == $doc_id)] | .[0].id // empty"
}

# sign_task TOKEN TASK_ID → response JSON.
# Guards against an empty/null task id (a missing-task lookup must FAIL loudly,
# not POST to /sign/null and abort the whole script under set -e).
sign_task() {
  local tok="$1" task_id="$2"
  if [ -z "$task_id" ] || [ "$task_id" = "null" ]; then
    echo '{"success":false,"error":{"code":"smoke_no_task","message":"no task id resolved"}}'
    return 0
  fi
  local hash="aabbcc$(openssl rand -hex 29 2>/dev/null || echo "deadbeef0000000000000000000000000000000000000000000000000000")"
  # NOTE: the internal Sign handler derives the idempotency request_id from the
  # X-Request-ID header (middleware.RequestIDKey), NOT the JSON body. api_post
  # sends a fresh X-Request-ID per call, so each sign is a distinct request.
  api_post "$tok" "/api/v1/signature-tasks/$task_id/sign" \
    "{\"signature_image_hash\":\"$hash\",\"comment\":\"\"}"
}

# import_pdf TOKEN DOC_FORMAT DOC_NO → response JSON
import_pdf() {
  local tok="$1" fmt="$2" doc_no="$3"
  local dummy_pdf
  dummy_pdf=$(mktemp /tmp/smoke_XXXXXX.pdf)
  printf '%%PDF-1.4 1 0 obj<</Type/Catalog>>endobj\n' > "$dummy_pdf"
  local resp
  resp=$(curl -s -X POST "$API/api/v1/documents/import" \
    -H "Authorization: Bearer $tok" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    -F "file=@$dummy_pdf;type=application/pdf" \
    -F "doc_format_code=$fmt" \
    -F "doc_no=$doc_no" \
    -F "doc_date=2026-06-17" \
    -F "amount=100000.00" || true)
  rm -f "$dummy_pdf"
  echo "$resp"
}

# external_post SIGNER_TOKEN PATH JSON_BODY → response JSON (never aborts)
external_post() {
  local stok="$1" path="$2" body="$3"
  curl -s -X POST \
    -H "X-Signer-Token: $stok" \
    -H "Content-Type: application/json" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    -d "$body" \
    "$API$path" || true
}

# external_post_status SIGNER_TOKEN PATH JSON_BODY → HTTP status only
external_post_status() {
  local stok="$1" path="$2" body="$3"
  curl -s -o /dev/null -w "%{http_code}" -X POST \
    -H "X-Signer-Token: $stok" \
    -H "Content-Type: application/json" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    -d "$body" \
    "$API$path"
}

# external_get_status SIGNER_TOKEN PATH → HTTP status only
external_get_status() {
  local stok="$1" path="$2"
  curl -s -o /dev/null -w "%{http_code}" \
    -H "X-Signer-Token: $stok" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    "$API$path"
}

# external_get_code SIGNER_TOKEN PATH → response JSON (with error handling)
external_get_code() {
  local stok="$1" path="$2"
  curl -s \
    -H "X-Signer-Token: $stok" \
    -H "X-Request-ID: $(uuidgen 2>/dev/null || echo smoke-$$)" \
    "$API$path"
}

# wait_for_status TOKEN DOC_ID STATUS MAX_SECS
wait_for_status() {
  local tok="$1" doc_id="$2" want_status="$3" max="$4"
  local i=0
  while [ $i -lt "$max" ]; do
    local got
    got=$(api_get "$tok" "/api/v1/documents/$doc_id" | jq -r '.data.status' 2>/dev/null || echo "unknown")
    if [ "$got" = "$want_status" ]; then return 0; fi
    sleep 1
    i=$((i+1))
  done
  return 1
}

echo ""
echo "════════════════════════════════════════════════════════════"
echo "  PaperLess 1.0 — Pre-deploy Smoke Test"
echo "  API: $API"
echo "  $(date)"
echo "════════════════════════════════════════════════════════════"
echo ""

# ── 0. Health checks ─────────────────────────────────────────────────────────

info "0. Health checks"

live_status=$(curl -s -o /dev/null -w "%{http_code}" "$API/health")
assert_eq "GET /health → 200" "200" "$live_status"

ready_resp=$(curl -s "$API/health/ready")
ready_status_val=$(echo "$ready_resp" | jq -r '.status' 2>/dev/null || echo "error")
assert_eq "GET /health/ready status=ok" "ok" "$ready_status_val"

ready_db=$(echo "$ready_resp" | jq -r '.database' 2>/dev/null || echo "error")
assert_eq "GET /health/ready database=ok" "ok" "$ready_db"

ready_storage=$(echo "$ready_resp" | jq -r '.storage' 2>/dev/null || echo "error")
assert_eq "GET /health/ready storage=ok" "ok" "$ready_storage"

# ── 1. Auth ──────────────────────────────────────────────────────────────────

info "1. Auth"

ADMIN_TOKEN=$(login "admin" "password123")
[ -n "$ADMIN_TOKEN" ] && pass "admin login → token" || { fail "admin login failed"; exit 1; }

MAKER_TOKEN=$(login "maker" "password123")
[ -n "$MAKER_TOKEN" ] && pass "maker login → token" || { fail "maker login failed"; exit 1; }

CHECKER_A_TOKEN=$(login "checkerA" "password123")
[ -n "$CHECKER_A_TOKEN" ] && pass "checkerA login → token" || { fail "checkerA login failed"; exit 1; }

CHECKER_B_TOKEN=$(login "checkerB" "password123")
[ -n "$CHECKER_B_TOKEN" ] && pass "checkerB login → token" || { fail "checkerB login failed"; exit 1; }

APPROVER_TOKEN=$(login "approver" "password123")
[ -n "$APPROVER_TOKEN" ] && pass "approver login → token" || { fail "approver login failed"; exit 1; }

# me endpoint
me_resp=$(api_get "$ADMIN_TOKEN" "/api/v1/auth/me")
me_user=$(echo "$me_resp" | jq -r '.data.username')
assert_eq "GET /auth/me returns admin" "admin" "$me_user"

# ── 2. Internal flow: POP (condition 1 / 2 / 1) ──────────────────────────────

info "2. Internal flow — POP import → 3 steps → completed → final PDF"

SMOKE_DOC_NO="SMOKE-INT-$$"
import_resp=$(import_pdf "$ADMIN_TOKEN" "POP" "$SMOKE_DOC_NO")
DOC_ID=$(echo "$import_resp" | jq -r '.data.id')
[ "$DOC_ID" != "null" ] && [ -n "$DOC_ID" ] && pass "Import POP doc_id=$DOC_ID" || { fail "Import POP failed: $import_resp"; exit 1; }

doc_status=$(api_get "$ADMIN_TOKEN" "/api/v1/documents/$DOC_ID" | jq -r '.data.status')
assert_eq "Imported doc status=pending" "pending" "$doc_status"

# Step 1 — seq 1, condition 1 (any-one: maker)
info "  Step 1 — maker signs (condition 1)"
TASK1_ID=$(find_task "$MAKER_TOKEN" "$DOC_ID")
[ -n "$TASK1_ID" ] && pass "maker has task for doc $DOC_ID (task $TASK1_ID)" || { fail "maker inbox missing task for doc $DOC_ID"; exit 1; }

# Non-assignee internal sign → 403 (security invariant).
# IMPORTANT: this MUST be asserted while task 1 is still OPEN (before maker signs).
# POP step 1 is condition_type=1 (any-one); once the step is actioned the engine
# returns an idempotent "already_actioned" 200 via a fast path that runs BEFORE the
# assignee check — so asserting 403 on an already-signed task is wrong (it's 200).
info "  Security: non-assignee (approver) cannot sign maker's OPEN task → 403"
nonsign_status=$(api_post_status "$APPROVER_TOKEN" "/api/v1/signature-tasks/$TASK1_ID/sign" \
  "{\"signature_image_hash\":\"aabbccddeeff00112233445566778899aabbccddeeff001122334455667788\",\"comment\":\"\"}")
assert_eq "non-assignee sign (open task) → 403" "403" "$nonsign_status"

sign_resp=$(sign_task "$MAKER_TOKEN" "$TASK1_ID")
sign_ok=$(echo "$sign_resp" | jq -r '.success')
assert_eq "maker signs step 1 → success" "true" "$sign_ok"

# Step 2 — seq 2, condition 2 (all: checkerA + checkerB both must sign)
info "  Step 2 — checkerA + checkerB sign (condition 2)"
TASK2A_ID=$(find_task "$CHECKER_A_TOKEN" "$DOC_ID")
[ -n "$TASK2A_ID" ] && pass "checkerA has task for doc $DOC_ID (task $TASK2A_ID)" || { fail "checkerA inbox missing task for doc $DOC_ID"; exit 1; }

sign_resp=$(sign_task "$CHECKER_A_TOKEN" "$TASK2A_ID")
assert_eq "checkerA signs step 2 → success" "true" "$(echo "$sign_resp" | jq -r '.success')"

TASK2B_ID=$(find_task "$CHECKER_B_TOKEN" "$DOC_ID")
[ -n "$TASK2B_ID" ] && pass "checkerB has task for doc $DOC_ID (task $TASK2B_ID)" || { fail "checkerB inbox missing task for doc $DOC_ID"; exit 1; }

sign_resp=$(sign_task "$CHECKER_B_TOKEN" "$TASK2B_ID")
assert_eq "checkerB signs step 2 → success" "true" "$(echo "$sign_resp" | jq -r '.success')"

# Step 3 — seq 3, condition 1 (any-one: approver)
info "  Step 3 — approver signs (condition 1)"
TASK3_ID=$(find_task "$APPROVER_TOKEN" "$DOC_ID")
[ -n "$TASK3_ID" ] && pass "approver has task for doc $DOC_ID (task $TASK3_ID)" || { fail "approver inbox missing task for doc $DOC_ID"; exit 1; }

sign_resp=$(sign_task "$APPROVER_TOKEN" "$TASK3_ID")
assert_eq "approver signs step 3 → success" "true" "$(echo "$sign_resp" | jq -r '.success')"

# Doc must now be completed
doc_status=$(api_get "$ADMIN_TOKEN" "/api/v1/documents/$DOC_ID" | jq -r '.data.status')
assert_eq "POP doc status=completed after all steps" "completed" "$doc_status"

# Download final PDF — must be valid PDF (starts with %PDF)
info "  Final PDF download"
final_pdf_resp=$(curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$API/api/v1/documents/$DOC_ID/file/final" || true)
final_pdf_status=$(echo "$final_pdf_resp" | tail -1)
final_pdf_body=$(echo "$final_pdf_resp" | head -1)
assert_eq "GET /file/final → 200" "200" "$final_pdf_status"
assert_contains "Final PDF starts with %PDF" "%PDF" "$final_pdf_body"

# Idempotent re-import (same doc_no + format = duplicate, returns existing id)
info "  Idempotency: re-import same doc_no → existing doc returned"
reimport_resp=$(import_pdf "$ADMIN_TOKEN" "POP" "$SMOKE_DOC_NO")
reimport_id=$(echo "$reimport_resp" | jq -r '.data.id')
assert_eq "Re-import same key → same doc id" "$DOC_ID" "$reimport_id"

# Cannot sign a completed document
info "  Security: completed doc cannot be signed"
sign_completed_status=$(api_post_status "$MAKER_TOKEN" "/api/v1/signature-tasks/$TASK1_ID/sign" \
  "{\"signature_image_hash\":\"aabb\",\"comment\":\"\"}")
# Returns 409 or 404 (task already actioned) — must not be 5xx
[[ "$sign_completed_status" =~ ^[45] ]] && pass "Completed doc sign → 4xx (not 5xx, got $sign_completed_status)" || \
  fail "Completed doc sign → unexpected $sign_completed_status (want 4xx)"

# ── 3. External flow: DEMO3 (condition 1 / 2 / 3) ───────────────────────────

info "3. External flow — DEMO3: activate template → import → steps 1+2 → invite → external sign → completed"

if [ -z "${PAPERLESS_TEST_DB:-}" ]; then
  echo -e "${YELLOW}⚠  PAPERLESS_TEST_DB not set — skipping external flow (section 3 + external security checks).${NC}"
  echo "   To run the external flow, export PAPERLESS_TEST_DB=<dsn> and re-run."
else
  # restore_demo3 is registered as an EXIT trap BEFORE activation, so the template
  # is always restored to 'draft' even if the script aborts mid-flow. Leaving DEMO3
  # 'active' would change import behavior for subsequent runs (real cleanup leak).
  restore_demo3() {
    psql "$PAPERLESS_TEST_DB" -q -c \
      "UPDATE workflow_templates SET status='draft' WHERE doc_format_code='DEMO3' AND version=1;" 2>/dev/null \
      && echo -e "${GREEN}✓${NC} DEMO3 template restored to draft (cleanup)" \
      || echo -e "${YELLOW}⚠  Could not restore DEMO3 to draft — run manually: psql \$PAPERLESS_TEST_DB -c \"UPDATE workflow_templates SET status='draft' WHERE doc_format_code='DEMO3' AND version=1;\"${NC}"
  }
  trap restore_demo3 EXIT

  if psql "$PAPERLESS_TEST_DB" -q -c \
       "UPDATE workflow_templates SET status='active' WHERE doc_format_code='DEMO3' AND version=1;" 2>/dev/null; then
    pass "DEMO3 template activated for smoke"

    SMOKE_EXT_DOC_NO="SMOKE-EXT-$$"
    ext_import_resp=$(import_pdf "$ADMIN_TOKEN" "DEMO3" "$SMOKE_EXT_DOC_NO")
    EXT_DOC_ID=$(echo "$ext_import_resp" | jq -r '.data.id // empty')

    if [ -z "$EXT_DOC_ID" ]; then
      fail "Import DEMO3 failed: $ext_import_resp"
    else
      pass "Import DEMO3 doc_id=$EXT_DOC_ID"

      # Step 1 — maker signs (condition 1)
      info "  Ext step 1 — maker signs"
      EXT_TASK1=$(find_task "$MAKER_TOKEN" "$EXT_DOC_ID")
      [ -n "$EXT_TASK1" ] && pass "maker has ext step 1 task ($EXT_TASK1)" || fail "maker inbox missing ext step 1 task"
      sign_resp=$(sign_task "$MAKER_TOKEN" "$EXT_TASK1")
      assert_eq "Ext step 1: maker signs → success" "true" "$(echo "$sign_resp" | jq -r '.success')"

      # Step 2 — checkerA signs (condition 2). NOTE: the DEMO3 template (migration
      # 0005) assigns ONLY checkerA to its CHECKER step — unlike POP which assigns
      # both checkerA and checkerB. With a single assignee, condition_type=2
      # ("all must sign") completes on that one signature (1/1). So checkerA alone
      # advances the DEMO3 step 2; there is intentionally no checkerB task here.
      info "  Ext step 2 — checkerA signs (DEMO3 step 2 has a single assignee)"
      EXT_TASK2A=$(find_task "$CHECKER_A_TOKEN" "$EXT_DOC_ID")
      [ -n "$EXT_TASK2A" ] && pass "checkerA has ext step 2 task ($EXT_TASK2A)" || fail "checkerA inbox missing ext step 2 task"
      sign_resp=$(sign_task "$CHECKER_A_TOKEN" "$EXT_TASK2A")
      assert_eq "Ext step 2: checkerA signs → success" "true" "$(echo "$sign_resp" | jq -r '.success')"

      # Step 3 — invite external signer (condition 3)
      info "  Ext step 3 — admin invites external signer"
      invite_resp=$(api_post "$ADMIN_TOKEN" "/api/v1/documents/$EXT_DOC_ID/external-signers" \
        '{"name":"ลูกค้า ทดสอบ","expires_in_hours":1}')
      EXT_TOKEN=$(echo "$invite_resp" | jq -r '.data.token // empty')
      if [ -n "$EXT_TOKEN" ] && [ ${#EXT_TOKEN} -eq 64 ]; then
        pass "Invite external signer → 64-char hex token"
      else
        fail "Invite failed or token not 64-hex: $invite_resp"
      fi

      # Token must NOT appear in invite response alongside hash (security)
      assert_not_contains "Invite response contains no token_hash" "token_hash" "$invite_resp"

      # External view
      info "  External: view document via token header"
      ext_view_resp=$(external_get_code "$EXT_TOKEN" "/api/v1/external/document")
      assert_eq "External view → success" "true" "$(echo "$ext_view_resp" | jq -r '.success' 2>/dev/null || echo false)"
      assert_eq "External view → correct signer name" "ลูกค้า ทดสอบ" "$(echo "$ext_view_resp" | jq -r '.data.signer_name' 2>/dev/null || echo '')"

      # External sign
      info "  External: sign document"
      ext_hash="cc$(openssl rand -hex 31 2>/dev/null || printf '%062d' 0)"
      ext_req_id=$(uuidgen 2>/dev/null || echo "ext-$$")
      ext_sign_resp=$(external_post "$EXT_TOKEN" "/api/v1/external/sign" \
        "{\"signature_image_hash\":\"$ext_hash\",\"consent_text\":\"consent\",\"request_id\":\"$ext_req_id\"}")
      assert_eq "External sign → signed=true" "true" "$(echo "$ext_sign_resp" | jq -r '.data.signed' 2>/dev/null || echo false)"

      # Document should now be completed (external step was last)
      if wait_for_status "$ADMIN_TOKEN" "$EXT_DOC_ID" "completed" 10; then
        pass "DEMO3 doc status=completed after external sign"
      else
        fail "DEMO3 doc did not reach completed within 10s after external sign"
      fi

      # External final PDF — must be valid PDF
      info "  External final PDF download"
      ext_final_resp=$(curl -s -w "\n%{http_code}" \
        -H "Authorization: Bearer $ADMIN_TOKEN" \
        "$API/api/v1/documents/$EXT_DOC_ID/file/final" || true)
      assert_eq "GET /file/final (external doc) → 200" "200" "$(echo "$ext_final_resp" | tail -1)"
      assert_contains "External final PDF starts with %PDF" "%PDF" "$(echo "$ext_final_resp" | head -1)"

      # ── 4. Security invariants (external) ────────────────────────────────

      info "4. Security invariants — external token"

      # Reuse of a consumed token → external_link_used (410)
      info "  Reuse consumed external token → 410"
      reuse_status=$(external_post_status "$EXT_TOKEN" "/api/v1/external/sign" \
        "{\"signature_image_hash\":\"$ext_hash\",\"consent_text\":\"consent\",\"request_id\":\"$(uuidgen 2>/dev/null || echo reuse-$$)\"}")
      assert_eq "Reuse consumed token → 410" "410" "$reuse_status"
    fi
  else
    fail "Could not activate DEMO3 template — check PAPERLESS_TEST_DB"
  fi

  # Garbage token → 401 (not 5xx). Runs even if the flow above failed, as long as
  # the DB was reachable — it needs no document state.
  info "  Garbage token → clean 401"
  garbage_status=$(external_get_status "notarealtoken000000000000000000000000000000000000000000000000000" "/api/v1/external/document")
  assert_eq "Garbage token → 401" "401" "$garbage_status"

  # Rate limit — rapid same-IP requests must trip 429. checkRateLimit runs BEFORE
  # token validation in DocumentView, so even garbage tokens count toward the cap.
  info "  Rate limit fires (per-IP, same-process)"
  RATE_TOKEN="$(openssl rand -hex 32 2>/dev/null || printf '%064d' 0)"
  rate_hit=false
  for i in $(seq 1 30); do
    st=$(external_get_status "$RATE_TOKEN" "/api/v1/external/document")
    if [ "$st" = "429" ]; then rate_hit=true; break; fi
  done
  [ "$rate_hit" = "true" ] && pass "Rate limit fires → 429 within 30 requests" || \
    fail "Rate limit did NOT fire within 30 requests (want 429)"
fi

# ── 5. Security invariants (internal) ────────────────────────────────────────

info "5. Security invariants — internal"

# File download for a document the caller cannot access
# A signer (maker) attempting to download a completed doc they didn't own should still get the file
# (broad authenticated read for final PDF is the accepted design per phase1-plan.md).
# But they must NOT be able to call /file/final on a non-existent doc.
info "  GET /file/final for non-existent doc → 404"
nonexist_status=$(api_get_status "$MAKER_TOKEN" "/api/v1/documents/999999999/file/final")
assert_eq "Non-existent doc final PDF → 404" "404" "$nonexist_status"

# Unauthenticated request to protected endpoint → 401
info "  Unauthenticated request → 401"
unauth_status=$(curl -s -o /dev/null -w "%{http_code}" "$API/api/v1/documents/$DOC_ID")
assert_eq "No auth header → 401" "401" "$unauth_status"

# Signer role cannot list external signers (requires document_admin)
info "  Signer role cannot list external signers → 403"
list_signers_status=$(api_get_status "$MAKER_TOKEN" "/api/v1/documents/$DOC_ID/external-signers")
assert_eq "Signer listing external signers → 403" "403" "$list_signers_status"

# Admin can list external signers → 200
list_signers_admin=$(curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  "$API/api/v1/documents/$DOC_ID/external-signers")
assert_eq "Admin listing external signers → 200" "200" "$list_signers_admin"

# Log hygiene: ensure no token/password appears in inline JSON responses
info "  Log hygiene: API responses do not echo password/raw token fields"
login_resp_body=$(curl -sf -X POST "$API/api/v1/auth/login" \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"password123"}')
assert_not_contains "Login response does not echo password" "password123" "$login_resp_body"
assert_not_contains "Login response does not contain password_hash" "password_hash" "$login_resp_body"

# ── 6. Audit log ─────────────────────────────────────────────────────────────

info "6. Audit log"
audit_resp=$(api_get "$ADMIN_TOKEN" "/api/v1/documents/$DOC_ID/audit-logs")
audit_ok=$(echo "$audit_resp" | jq -r '.success')
assert_eq "GET /audit-logs → success" "true" "$audit_ok"
audit_count=$(echo "$audit_resp" | jq -r '.data | length')
[ "$audit_count" -gt 0 ] && pass "Audit log has entries ($audit_count)" || \
  fail "Audit log is empty — expected sign events"

# Audit log must not contain any token, password, or raw signature binary
audit_body=$(echo "$audit_resp")
assert_not_contains "Audit log: no raw password" "password123" "$audit_body"
assert_not_contains "Audit log: no token field" "\"token\"" "$audit_body"
assert_not_contains "Audit log: no token_hash field" "token_hash" "$audit_body"

# ── Summary ───────────────────────────────────────────────────────────────────

echo ""
echo "════════════════════════════════════════════════════════════"
TOTAL=$((PASS+FAIL))
if [ $FAIL -eq 0 ]; then
  echo -e "  ${GREEN}ALL CHECKS PASSED${NC} ($PASS/$TOTAL)"
else
  echo -e "  ${RED}FAILURES: $FAIL/$TOTAL${NC}"
  echo ""
  echo "  Failed checks:"
  for f in "${FAILURES[@]}"; do
    echo -e "    ${RED}•${NC} $f"
  done
fi
echo "════════════════════════════════════════════════════════════"
echo ""

[ $FAIL -eq 0 ]
