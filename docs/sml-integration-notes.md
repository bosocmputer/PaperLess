# SML Integration Notes

PaperLess integrates with SML **only** through `sml-api-bybos` (Go/Gin/pgx gateway, `:8200`, multi-tenant). We extend that service; we do not connect PaperLess to the SML database.

## What already exists in sml-api-bybos (reusable)

- `GET /api/v1/ic/doc-formats?screen_code=...` → reads `erp_doc_format`. This is the source of `doc_format_code` for workflow config. Supports `PO`, `SR`, `SI`, `EE`.
- Conventions to mirror in new endpoints: API-key + tenant headers, zap structured logging, `X-Request-ID`, `{success,data,meta}` envelope, optional `erp_logs` write with graceful `log_status:"warning"` fallback.
- Deploy target: server `192.168.2.109:8200`; SML PostgreSQL `192.168.2.248:5432`.

## What we must add (new endpoints)

| Endpoint | Purpose | Blocked on |
| --- | --- | --- |
| `GET /api/v1/paperless/documents/:doc_no` | fetch metadata (+ PDF reference) to import a document | needs: which SML tables hold the doc + how the PDF is produced |
| `POST /api/v1/paperless/documents/:doc_no/confirm` | set Confirm status in SML after signing | needs: exact table/field + write rule |
| `POST /api/v1/paperless/documents/:doc_no/lock` | lock document against edits | needs: exact table/field + write rule |

## Open questions for the SML team (send early — these gate Phase 3)

> A ready-to-send, fill-in form of these questions (Thai, with priority + impact + answer fields) lives in **`docs/sml-questions.md`**. Send that to the SML team and commit their answers back.

1. **How will SML deliver the PDF + metadata to PaperLess?** Options: (a) sml-api reads existing SML tables and PaperLess pulls on demand; (b) SML renders/stores a PDF we can fetch; (c) watched folder; (d) scheduled push; (e) manual upload only. We assume (a)/(e) until told otherwise.
2. **Which SML table/field represents "Confirm"?** (e.g. a flag on `ic_trans` / `ap_ar_trans`?) What is the exact write (column, value, any timestamp/user fields)?
3. **Which SML table/field represents "Lock"?** Same detail. Is lock idempotent (safe to write twice)?
4. **Document chain source.** Excel shows POP → PUP → PBV → PVV. Which SML field links a document to its predecessor (`source_doc_no`?) so PaperLess can render the chain?
5. **Signature placement.** Does each `doc_format_code` define signature coordinates, or does the admin set them per template? (Until answered, final PDF uses an appended signature-evidence page — see `docs/domain.md`.)
6. **Idempotency / revision.** Does SML expose a revision/version for a document so a re-sent document is distinguishable from a retry? (We compute `source_hash` as a fallback.)
7. **Confirm/Lock failure semantics.** If PaperLess pushes Confirm and times out, is re-sending safe? Is there a way to read back current Confirm/Lock state for reconciliation?

## Working assumptions until confirmed

- SML is PostgreSQL (confirmed via sml-api-bybos).
- Confirm and Lock are two separate, idempotent writes.
- PaperLess remains the system of record for signing; SML remains system of record for the business document.
- A document is fully usable in PaperLess once `completed`, independent of SML sync success.

## Reconciliation (Phase 3)

A scheduled job + report must surface:

- Documents `completed` in PaperLess but not `synced` to SML.
- Documents SML reports as Confirmed/Locked but PaperLess shows not-synced (drift).

Until confirm/lock fields are known, the sync worker runs against a mock `SmlDocumentGateway` so the rest of the system is fully testable.
