# Domain Model

This is the authoritative description of PaperLess workflow rules. Source: customer Excel spec (`docs/requirements/`) + confirmed assumptions. Code must match this file; if code and this file disagree, this file is the intended behavior — fix the code or update this file deliberately.

## Product Vocabulary

| Term | Meaning | Source of truth |
| --- | --- | --- |
| `doc_format_code` | Document type code from SML, e.g. `POP`, `INV`, `PUP`, `PBV`, `PVV`. Maps a document to a workflow template. | SML `erp_doc_format` (read via `sml-api-bybos`) |
| Position | A signing role within a workflow, e.g. ผู้จัดทำ / ผู้ตรวจสอบ / ผู้อนุมัติ / ลูกค้า. | PaperLess workflow config |
| `ลำดับ` (sequence_no) | Order of a position in the workflow. Sequence N+1 does not open until N is complete. | PaperLess workflow config |
| `เงื่อนไข` (condition_type) | Completion rule for a position: 1=any-one, 2=all, 3=external. | PaperLess workflow config |
| `User01/User02/User03` | Assignees / eligible signers for a position (also used for notifications & reports). | PaperLess workflow config |
| Workflow template version | An immutable snapshot of a workflow's steps. A document binds to one version for life. | PaperLess |
| Signature task | One unit of "this user must act on this step of this document". | PaperLess |
| Signature event | The recorded fact of a sign/reject/skip, with legal evidence. Append-only. | PaperLess |
| External signer | A non-internal person (customer) who signs via a one-time, expiring secure link. | PaperLess |
| Final PDF | The completed document with signature evidence, legal text, and verification code. | PaperLess (MinIO) |
| Confirm / Lock | Status updates pushed back to SML once a document is completed. | SML (write via `sml-api-bybos`) |

## Workflow Completion Rules (condition_type) — most critical logic

### condition_type = 1 — any-one

- Open a task for every assignee in the position.
- The **first** signer whose transaction commits completes the step.
- All other tasks in the same step are marked `skipped` in the **same transaction**, under a row lock on the step.
- A signer who submits afterward gets: "ขั้นตอนนี้มีผู้ดำเนินการแล้ว" (step already actioned) — not an error, a clear state.
- Concurrency: use `SELECT ... FOR UPDATE` on the step row (or a version/optimistic-lock column). Never trust frontend state — re-check current status from the DB inside the transaction.

### condition_type = 2 — all

- Open a task for every assignee in the position.
- The step completes only when **every** assignee's task is `signed`.
- UI shows progress (e.g. 1/2, 2/2). Order among the co-signers does not matter.

### condition_type = 3 — external

- Create an `external_signers` record per document.
- Issue a one-time, expiring token (stored hashed). Optionally require OTP.
- The step completes only when the external signer signs with a valid, unused, unexpired token.
- Do not reuse a global shared temp user — each external signing is its own auditable record (name, contact, evidence, expiry).

### sequence gate (sequence_no)

- Tasks for sequence N+1 are created/opened only after sequence N is complete.
- A document with steps `[1,1,2,3]` (Excel example) means: two positions at sequence 1 (both open together), then sequence 2, then sequence 3.

## Document State Machine

```text
[*] → Imported → Pending ─┬─► Completed → SyncPending ─┬─► Synced → [*]
                          │                            └─► SyncFailed ─► SyncPending
                          ├─► Rejected ─► Pending   (return to a defined step)
                          └─► Cancelled → [*]
```

- **Imported:** received, deduped, workflow version locked, not yet routed.
- **Pending:** signature tasks active.
- **Completed:** all steps signed; final PDF generated. **Usable immediately** (downloadable) regardless of SML sync.
- **Rejected:** a signer rejected with a reason; returns to a defined step (or terminal, per config).
- **Cancelled:** terminal; no further signing.
- **SyncPending / Synced / SyncFailed:** SML Confirm/Lock lifecycle — decoupled from document usability.

## Refinements over the original blueprint (decided 2026-06-16)

1. **`source_hash` alongside idempotency key.** `idempotency_key` dedupes retries; `source_hash` (hash of PDF + canonical metadata) distinguishes a true revised document from a duplicate retry. Same key + same hash = retry (skip). Same key + different hash = a revision was sent → reject/flag for admin, do not silently overwrite.
2. **Final PDF defaults to an appended signature-evidence page**, not stamping at exact coordinates. The evidence page lists each signer + timestamp + legal text + verification code, and works for any `doc_format_code` without knowing signature coordinates (still an open SML question). Exact-coordinate stamping is a later enhancement, not a Phase 1 blocker.
3. **`completed` is independent of `synced`.** A document is fully usable on completion; SML sync is a separate, retryable lifecycle. This keeps Phase 1/2 usable while SML confirm/lock fields are still unconfirmed.

## User Roles

| Role | Can do | Cannot do |
| --- | --- | --- |
| System Admin | Manage users, roles, system settings, view system logs | Sign on behalf of others |
| Workflow Admin | Create/clone/publish workflow templates (doc format, positions, users, sequence, condition) | Mutate an in-use template version; sign |
| Document Admin | Import documents, view status, reprocess/retry failed jobs (with reason + audit) | Edit signatures or audit history |
| Signer | View their open tasks, sign or reject (with reason) | Sign tasks not assigned/open to them; edit a submitted signature |
| External Signer | Open one document via secure link, verify (OTP if required), sign | Access any other document; reuse the link |
| Auditor | View document history, audit trail, final PDF | Modify anything |
| Integration Service | Service account for `sml-api-bybos` calls / background jobs | Interactive signing |

Principle: no shared accounts for real signing — every signature must trace to one identity.

## Integrations

| Integration | Purpose | Credentials location | Failure behavior |
| --- | --- | --- | --- |
| `sml-api-bybos` (read) | Pull `doc_format_code` list; (Phase 3) pull document + metadata for import | API key + tenant header in local/deploy secret source | Import path shows clear error; manual upload remains available |
| `sml-api-bybos` (write) | (Phase 3) push Confirm/Lock back to SML | same | Retry with backoff; on timeout → `sync_unknown`/`sync_pending`, never assume success; surfaced in reconciliation report |
| MinIO | Store original/final PDF, signatures, thumbnails | local/deploy secret source | Upload retried; download requires permission check every time |
