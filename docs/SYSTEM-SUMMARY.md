# PaperLess — สรุประบบทั้งหมด (Handoff)

> สรุปจากของจริงในโค้ด/migration/git ณ 2026-06-19 (commit `e8b6cc1`).
> ใช้เป็นเอกสารส่งต่อสำหรับทำเองต่อ. ทุกข้อ verified จาก source — ส่วนที่เป็น
> assumption/ยังไม่ทำ จะกำกับไว้ชัดเจน.

---

## 1. ระบบคืออะไร

PaperLess = ระบบ **เซ็นเอกสารดิจิทัล (digital signature workflow)** สำหรับเอกสารที่
ออกมาจาก **SML ERP**. flow หลัก:

```
SML สร้างเอกสาร (PO/PA/PB/PV...) → export PDF
  → admin upload เข้า PaperLess (import)
  → PaperLess เปิด workflow เซ็นตามลำดับขั้น (maker→checker→approver→external)
  → เซ็นครบ → document = completed
  → PaperLess sync กลับไป SML: lock เอกสาร (is_lock_record=1) กันแก้ไข
```

**System of record:** PaperLess เป็นเจ้าของ "การเซ็น"; SML เป็นเจ้าของ "ตัวเอกสารธุรกิจ".
**สำคัญ:** เอกสารที่ `completed` ต้องดาวน์โหลดได้เสมอ **ไม่ขึ้นกับว่า SML sync สำเร็จไหม**.

---

## 2. สถาปัตยกรรม / Stack

| ส่วน | เทคโนโลยี | หมายเหตุ |
|---|---|---|
| **Web** | Next.js 14 (App Router, standalone, PWA) + Tailwind | `apps/web` — client-side fetch ผ่าน Next proxy `/api/v1` |
| **API** | Go 1.24 / Gin / pgx + pgxpool | `apps/api` — REST, JWT auth |
| **DB** | PostgreSQL | schema ผ่าน migration (`apps/api/migrations`) |
| **Object store** | MinIO | เก็บไฟล์ PDF (original + final) |
| **SML bridge** | `sml-api-bybos` (Go gateway, แยก repo) | PaperLess **ห้ามต่อ SML DB ตรง** — ผ่าน gateway เท่านั้น |
| **Deploy** | Docker Compose, on-prem server `192.168.2.109` | local ไม่มี Docker — ทดสอบบน server |

**โครงโฟลเดอร์:**
```
apps/api/          Go backend
  cmd/server/      main.go (routes ทั้งหมดอยู่นี่)
  internal/
    handlers/      HTTP handlers (auth, documents, tasks, workflow_templates, external_*)
    workflow/      engine.go (signing state machine) + types.go
    sml/           client.go + worker.go (Phase 5 lock sync)
    auth/ middleware/ httpx/ pdf/ storage/ config/ db/
  migrations/      0001–0006 (.up/.down)
apps/web/          Next.js frontend
deploy/            docker-compose.prod.yml + Dockerfiles
docs/              เอกสารทั้งหมด (รวมไฟล์นี้)
```

---

## 3. Database Schema (13 ตาราง — migration 0001)

**Identity / Auth:**
- `users` (id, username UNIQUE, display_name, email, phone, status active|inactive)
- `roles` (code, name) — seed: system_admin, workflow_admin, document_admin, signer, auditor, integration
- `user_roles` (user_id, role_id) — many-to-many

**Workflow config:**
- `workflow_templates` (id, doc_format_code, name, version, status draft|active|inactive, effective_from, created_by) — UNIQUE(doc_format_code, version)
- `workflow_steps` (id, workflow_template_id→CASCADE, position_code, position_name, sequence_no, condition_type 1|2|3, signature_slot jsonb) — UNIQUE(template_id, position_code)
- `workflow_step_assignees` (id, workflow_step_id→CASCADE, user_id, display_order) — UNIQUE(step_id, user_id)

**Documents / Signing:**
- `documents` (id, doc_format_code, doc_no, revision, doc_date, amount, source_doc_no, **workflow_template_id**, workflow_version, status imported|pending|rejected|completed|cancelled, **sync_status** not_required|sync_pending|synced|sync_failed|sync_unknown, idempotency_key UNIQUE, source_hash)
- `document_files` (id, document_id, file_type original_pdf|final_pdf, object_key, file_hash, mime_type, size_bytes, uploaded_by_user_id)
- `signature_tasks` (id, document_id, sequence_no, condition_type, **assigned_user_id**, status, opened_at...) — งานเซ็นต่อคนต่อขั้น
- `signature_events` (id, task_id, document_id, signer info, action, signed_at) — audit ของการเซ็น
- `external_signers` (id, document_id, name, email, phone, status pending|signed|expired|cancelled, token hash, otp_verified, expires_at) — ผู้เซ็นภายนอก (ไม่มี user account)

**Audit / Integration:**
- `audit_logs` (id, actor_type, actor_id, action, entity_type, entity_id, reason, created_at)
- `sml_sync_jobs` (id, document_id→CASCADE, job_type update_confirm|update_lock, status pending|running|succeeded|failed|retry, attempt_count, max_attempts, request/response_payload, error_message, next_retry_at) — คิว sync ไป SML

**Invariant สำคัญ:** `documents.workflow_template_id` ผูกตอน import และ engine อ่าน steps
จาก template id นั้นตลอด lifecycle → **ห้ามแก้ steps ของ template ที่มี document วิ่งอยู่**
→ template active = immutable, แก้ได้เฉพาะ draft (clone→edit→publish).

---

## 4. API Endpoints ทั้งหมด (จาก main.go — verified)

> envelope: success = `{success:true, data, meta?}`; error = `{success:false, error:{code, message}}`.
> auth = JWT ใน `Authorization: Bearer`. ไฟล์ download รับ `?token=` ได้ (GET เท่านั้น).

### Public (ไม่ต้อง JWT)
- `GET /health`, `GET /health/ready`
- `POST /api/v1/auth/login`, `/auth/refresh`, `/auth/logout`
- **External sign** (token ใน `X-Signer-Token` header): `GET /external/document`, `GET /external/document/file/original`, `POST /external/sign`

### Auth required
- `GET /auth/me`
- **Documents:**
  - `GET /documents` (admin/auditor — list + filter/search/paginate)
  - `POST /documents/import` (multipart: file PDF + doc_no + doc_format_code + optional)
  - `GET /documents/:id`
  - `POST|GET /documents/:id/attachments`, `DELETE /attachments/:id`
  - `GET /documents/:id/audit-logs`, `GET /documents/:id/workflow-status`
  - `POST|GET /documents/:id/external-signers`, `.../:signerId/cancel`, `.../:signerId/resend`
  - `POST /documents/:id/finalize` (สร้าง final PDF)
  - `GET /documents/:id/file/original`, `/file/final` (รับ ?token=)
- **Workflow templates** (workflow_admin/system_admin):
  - `GET /workflow-templates`, `GET /:id`
  - `POST /:id/clone`, `POST /:id/publish`, `POST /:id/deactivate`
  - ❌ **ยังไม่มี: create, update, edit-steps** (ดู §7 "ยังขาด")
- **Signature tasks:**
  - `GET /signature-tasks/inbox` (กล่องงานเซ็นของฉัน)
  - `GET /:id`, `POST /:id/sign`, `POST /:id/reject`

### ❌ ไม่มีเลย (ต้องสร้างถ้าต้องการ)
- `GET /users`, user CRUD, role management
- workflow template create/edit/steps
- SML reconciliation/drift report

---

## 5. Workflow Engine (หัวใจการเซ็น) — `internal/workflow/engine.go`

- เอกสารมีหลาย **step** เรียงตาม `sequence_no`. แต่ละ step มี **condition_type:**
  - `1` = คนใดคนหนึ่งเซ็นพอ (any-one)
  - `2` = ทุกคนต้องเซ็น (all)
  - `3` = ผู้เซ็นภายนอก (external — เชิญผ่าน token/OTP, ไม่มี user account)
- เซ็นครบ step ปัจจุบัน → เปิด step ถัดไปอัตโนมัติ. ครบทุก step → document `completed`.
- ถ้ามีคน reject → document `rejected`, workflow หยุด.
- **Concurrency:** ใช้ row lock + ordered sibling lock กัน deadlock (เคยมีบั๊ก 40P01
  condition-1 sibling-skip — fixed 2026-06-18). duplicate request (request_id) =
  idempotent (Postgres 23505 → rollback ที่ถูกต้อง).
- เซ็นครบ → engine **enqueue `update_lock` job** ลง `sml_sync_jobs` + set
  `sync_status='sync_pending'` (Phase 5).

---

## 6. SML Integration (ส่วนที่คุณสนใจ) — verified facts

> **กฎเหล็ก:** PaperLess **ห้ามต่อ SML DB ตรง**. ทุก read/write ผ่าน `sml-api-bybos`
> gateway. เหตุผล: multi-tenant, Nexflow/BillFlow ใช้ gateway เดียวกัน, security boundary.
> (ADR: `docs/adr/0001-sml-access-via-gateway.md`)

### 6.1 Confirm/Lock = field เดียว (verified กับ sml1_2026 จริง)
- **Confirm = Lock = `UPDATE ic_trans SET is_lock_record=1 WHERE doc_no=...`**
  — integer field เดียว. ไม่มี approve_status/user/date ให้เขียน.
  Verified: PO26060001 (unlocked) vs PO26060002 (locked) ต่างกันแค่ field นี้.
- **idempotent overwrite** — ส่ง lock ซ้ำ = เขียนทับ. UI ต้องเตือน "ล็อกแล้ว, ยืนยัน
  re-save?" ก่อนส่งซ้ำ. **timeout ไม่ถือว่าสำเร็จ** (ต้อง retry/อ่าน state กลับ).
- **`is_lock_record` nullable** — มี 129 row เป็น NULL → UPDATE ต้องรองรับ NULL→1.
- **ap_ar side (PB/PV)** ใช้ `ap_ar_trans` (ไม่ใช่ ic_trans).

### 6.2 Document chain (สำหรับแสดงสายเอกสาร)
- **chain = `ic_trans_detail.ref_doc_no`** (ไม่ใช่ `ic_trans.doc_ref` — header field
  นั้นว่าง/เป็น token). Verified: PA26060001 detail มี ref_doc_no=PO26060001.
- ap_ar ใช้ `ap_ar_trans_detail`.

### 6.3 Doc-type catalog
- **`erp_doc_format.code`** (PO/PA/PB/PV/INV/SO + ชื่อไทย). ไม่มี trans_flag ในตารางนี้.
- bridge code→trans_flag→table อยู่ใน sml-api-bybos: `internal/handlers/doc_no.go` +
  `internal/models/transaction.go`.
- มี endpoint เดิม `GET /api/v1/ic/doc-formats?screen_code=...` ใช้ดึง doc_format ได้.

### 6.4 Import / PDF / Signature
- **Import = manual upload เท่านั้น** (Phase 1). SML สร้าง PDF → user save จาก SML →
  upload เข้า PaperLess. SML ไม่มี versioning → PaperLess เป็นเจ้าของ revision/source_hash.
- **Signature stamp:** แต่ละ doc_format มี signature block ตายตัว (PO = 4 ช่อง:
  ผู้ตรวจ/ผู้บันทึก/ผู้เสนอ/ผู้อนุมัติ ท้ายหน้าสุดท้าย). stamp-in-place ไม่ใช่หน้าแนบ.
  **ยังขาด: พิกัด pt ที่แน่นอนต่อ format** จาก SML.

### 6.5 PaperLess SML client + worker (Phase 5 — DONE) — `internal/sml/`
- **`client.go`**: `POST {baseURL}/api/v1/documents/{docNo}/lock`, header `X-Api-Key` +
  `X-Tenant`. 200 = success/already_locked; 404 = ErrDocNotFound; อื่น = retryable.
  **ไม่ log header/token.** NewClient คืน nil ถ้าไม่ตั้ง SML_API_KEY (worker disabled).
- **`worker.go`**: poll `sml_sync_jobs` ด้วย `FOR UPDATE SKIP LOCKED`, exponential
  backoff (30s/1m/2m/5m/15m, max 5 ครั้ง), recover stale-running job (>2m).
  Pattern 2-transaction: claim+mark-running+commit → call SML → record outcome.
- **Deploy:** lock endpoint รันเป็น **container แยก `sml-api-bybos-paperless:8201`**
  (ไม่แตะ shared `sml-api-bybos:8200` ที่ Nexflow/BillFlow ใช้).
- **Proven e2e (2026-06-19):** import PO26060001 → sign 4 คน → completed → worker lock
  ใน sml1_2026 จริง ภายใน 2 วินาที → is_lock_record=1, sync_status=synced.

### 6.6 SML Blockers ที่ยังเหลือ (gate การ lock ครบ 5 ชนิด)
1. **sml-api-bybos ขาด trans_flags PA(12), PB(213), PV(19)** ใน doc_no.go + transaction.go
   — 3 ใน 5 ชนิดที่ต้อง lock. PB/PV อยู่ใน ap_ar_trans. → งานฝั่ง sml-api-bybos team.
2. **ap_ar_trans lock path ยังไม่ได้พิสูจน์** (ไม่มีตัวอย่าง locked ใน test DB).
3. **พิกัด signature block ต่อ format** ยังไม่ได้จาก SML.

---

## 7. สถานะงาน: ทำเสร็จ vs ยังขาด

### ✅ ทำเสร็จแล้ว (verified/deployed)
- Auth (login/refresh/logout/JWT/RBAC 6 roles)
- Import เอกสาร (multipart) + dedup (idempotency_key + source_hash)
- Workflow engine ครบ (condition 1/2/3, sequential, reject, deadlock-safe)
- Internal signing (inbox/sign/reject) + external signing (token/OTP/expiry)
- Final PDF generation + download (original/final, ?token= auth)
- Admin: document list/detail, audit logs, workflow-status, external-signer mgmt
- Workflow template: list/get/clone/publish/deactivate (read + lifecycle)
- **Phase 5 SML lock sync** (queue + worker + client, e2e proven)
- Admin UI shell + nav (Phase A — deployed 2026-06-19)
- Role-based landing (admin→documents, signer→inbox)

### ❌ ยังขาด (ถ้าจะทำต่อ)
| งาน | รายละเอียด | แผนที่เขียนไว้ |
|---|---|---|
| **Workflow create/edit** | สร้าง template ใหม่ + แก้ steps/ผู้เซ็นผ่านหน้าจอ — ตอนนี้ทำไม่ได้เลย (ไม่มี backend endpoint) | `docs/workflow-config-plan.md` Phase B |
| **User management** | ไม่มี `/users` endpoint เลย; user มาจาก seed อย่างเดียว | `docs/workflow-config-plan.md` Phase C |
| **SML confirm/lock ครบ 5 ชนิด** | PA/PB/PV trans_flags + ap_ar path | §6.6 (งาน sml-api-bybos) |
| **SML reconciliation report** | หา drift: completed-แต่-ไม่-synced, หรือ SML-locked-แต่-PaperLess-ไม่รู้ | `docs/sml-integration-notes.md` §Reconciliation |
| **Signature coordinate per format** | พิกัด stamp ที่แน่นอน | จาก SML team |
| **Password setup สำหรับ pilot** | clear dev password + ตั้ง prod password | `docs/pilot-prep-plan.md` |
| **cloudflared tunnel ถาวร** | ตอนนี้ใช้ trycloudflare ชั่วคราว (URL rotate) | — |
| **Device QA** | iOS Safari + Android Chrome (manual) | — |

---

## 8. Deploy / Server

- **Server:** `192.168.2.109` (Ubuntu, user `bosscatdog`). local ไม่มี Docker.
- **Ports:** web `3070`, api `8080`, postgres `54320`, minio `9000/9001`,
  sml-lock `8201` (container แยก).
- **SML DB (อ่าน-อย่างเดียวตอน verify):** `192.168.2.248:5432`, db `sml1_2026`, user
  postgres. **PaperLess ห้ามต่อตรง** — ผ่าน gateway.
- **Deploy pattern:** rsync/scp source → server → `docker compose -f deploy/docker-compose.prod.yml up -d --build`. ไม่ใช่ git-based.
- **Public access:** trycloudflare tunnel (ชั่วคราว) — grep `~/cloudflared-paperless.log`
  หา URL ปัจจุบัน. (browser บังคับ https, server ไม่มี TLS → ต้องใช้ tunnel)
- **Secrets:** `.env` ไม่ commit; `CREDENTIALS.md` gitignored; token เก็บเป็น SHA-256
  hash; ห้าม log token/PII/signed URL.

---

## 9. เอกสารอ้างอิงในโปรเจกต์ (มีอยู่แล้ว)

| ไฟล์ | เนื้อหา |
|---|---|
| `docs/architecture.md` | สถาปัตยกรรมรวม |
| `docs/db-schema.md` | schema อธิบายละเอียด |
| `docs/api-contract.md` | API contract |
| `docs/domain.md` | domain model / business rules |
| `docs/current-state.md` | สถานะระบบ + audit history |
| `docs/sml-integration-notes.md` | SML integration (gateway, endpoints, open Q) |
| `docs/sml-questions.md` | คำถาม-คำตอบกับทีม SML |
| `docs/adr/*` | decision records (gateway, queue) |
| `docs/requirements/*` | requirement เดิม (Excel + blueprint) |
| `docs/phase{1..5}-*.md` | แผนแต่ละเฟส |
| `docs/workflow-config-plan.md` | แผน admin overhaul (Phase A done, B/C ยัง) |

---

## 10. คำเตือนเรื่อง security/data (ห้ามพลาด)

1. **PaperLess ห้ามต่อ SML DB ตรง** — gateway เท่านั้น.
2. **timeout ≠ success** — lock ที่ timeout ต้อง retry/อ่าน state กลับ ไม่ mark synced.
3. **completed document ต้องดาวน์โหลดได้แม้ SML sync ล้มเหลว**.
4. **ห้ามแก้ steps ของ template ที่ไม่ใช่ draft** — พัง workflow ที่กำลังวิ่ง.
5. **ห้าม log token/credential/PII/signed URL**. token เก็บ hash, raw คืนครั้งเดียว.
6. **migration เพิ่มไฟล์ใหม่เท่านั้น** — ห้ามแก้ 0001–0006.
7. **POST ?token= ต้องไม่สำเร็จ** — ?token= ใช้กับ GET file เท่านั้น (security invariant).

---
---

# ภาคผนวก (รายละเอียดเชิงลึก)

## ภาคผนวก A — Sequence Diagram: Import → Sign → Lock (flow เต็ม)

```
ADMIN            PaperLess-API        Postgres         MinIO        Worker        sml-api-bybos     SML-DB
  |                   |                  |                |            |                |              |
  |--POST /import---->|                  |                |            |                |              |
  |  (PDF+doc_no)     |--BEGIN tx------->|                |            |                |              |
  |                   |--check dedup---->| (idempotency_key UNIQUE)    |                |              |
  |                   |--find active template (status='active')        |                |              |
  |                   |--INSERT document (status='pending')            |                |              |
  |                   |--Put PDF------------------------>|             |                |              |
  |                   |--INSERT document_files           |             |                |              |
  |                   |--OpenFirstSequence (เปิด task seq=1)            |                |              |
  |                   |--audit document_imported                       |                |              |
  |                   |--COMMIT--------->|                |            |                |              |
  |<--201 {id,pending}|                  |                |            |                |              |
  |                   |                  |                |            |                |              |
SIGNER (maker)        |                  |                |            |                |              |
  |--POST /signature-tasks/:id/sign----->|                |            |                |              |
  |  (X-Request-Id)   |--BEGIN tx (row lock task)         |            |                |              |
  |                   |--INSERT signature_event (uq request_id)        |                |              |
  |                   |--mark task signed; engine เปิด step ถัดไป       |                |              |
  |                   |  (ครบ step → document 'completed' + enqueue)   |                |              |
  |                   |--ถ้า completed: INSERT sml_sync_jobs(update_lock,pending)        |              |
  |                   |    + UPDATE documents.sync_status='sync_pending'|                |              |
  |                   |--COMMIT--------->|                |            |                |              |
  |<--200 signed------|                  |                |            |                |              |
  |    (วนซ้ำจน signer ครบทุก step)        |                |            |                |              |
  |                   |                  |                |            |                |              |
  |                   |  [completed]     |                |  ticker 5s |                |              |
  |                   |                  |<--claim job (FOR UPDATE SKIP LOCKED)---------|              |
  |                   |                  |  mark running, COMMIT (ปล่อย lock)           |              |
  |                   |                  |                |            |--POST /documents/PO.../lock-->|
  |                   |                  |                |            |  (X-Api-Key,X-Tenant)         |
  |                   |                  |                |            |                |--UPDATE ic_trans
  |                   |                  |                |            |                |  is_lock_record=1
  |                   |                  |                |            |<--200 {is_lock_record:1}------|
  |                   |                  |<--tx2: job 'succeeded', sync_status='synced' |              |
  |                   |                  |    + audit document_synced  |                |              |
```

**จุดสำคัญ:**
- ระหว่าง tx1 (claim+running) กับ tx2 (outcome) ถ้า process ตาย → job ค้าง 'running' →
  worker อื่น recover หลัง `runningTimeout=2m`.
- lock call เกิด **นอก transaction** (ไม่ถือ DB lock ระหว่างรอ HTTP) → ไม่ block worker อื่น.
- timeout/error → job กลับเป็น 'retry' + `next_retry_at` ตาม backoff; ไม่ mark synced.

---

## ภาคผนวก B — Workflow State Machine (สถานะเอกสาร + task)

### documents.status
```
imported ──(import เปิด task)──> pending ──(เซ็นครบทุก step)──> completed ──(enqueue lock)
   │                                │
   │                                └──(มีคน reject)──> rejected
   └──(ยกเลิก)──> cancelled
```
> หมายเหตุ: import handler ปัจจุบัน INSERT ด้วย status='pending' โดยตรง (ข้าม imported)
> เพราะเปิด first-sequence tasks ทันที. 'imported' มีไว้สำหรับ flow ที่ import แล้วยังไม่
> เริ่ม workflow (เผื่ออนาคต).

### signature_tasks.status
```
waiting ──(step ก่อนหน้าเสร็จ)──> open ──(เซ็น)──> signed
   │                               │
   │                               ├──(reject)──> rejected
   │                               └──(condition 1: คนอื่นเซ็นแล้ว)──> skipped
   └──(เอกสารถูกยกเลิก)──> cancelled
```
- **condition_type=1 (any-one):** คนแรกที่เซ็น → task ตัวเอง 'signed', sibling tasks 'skipped'.
- **condition_type=2 (all):** ทุก task ต้อง 'signed' ก่อน step ถัดไปเปิด.
- **condition_type=3 (external):** task ผูก `external_signer_id` แทน `assigned_user_id`.

### documents.sync_status (SML)
```
not_required (ไม่ต้อง sync) | sync_pending (queue แล้ว) | synced (สำเร็จ)
                            | sync_failed (หมด retry) | sync_unknown (drift/reconcile)
```

### sml_sync_jobs.status
```
pending ──claim──> running ──success──> succeeded
   ^                  │
   │                  ├──retryable error (attempt<max)──> retry ──(next_retry_at ถึง)──> claim
   │                  └──fatal / attempt>=max──> failed
   └──(stale running >2m)── recover ──┘
```

---

## ภาคผนวก C — External Signer Flow (ผู้เซ็นภายนอก) — รายละเอียด

1. **admin เชิญ:** `POST /documents/:id/external-signers` {name, email?, phone?, expires_in_hours?}
   → สร้าง row ใน `external_signers`, gen **raw token 64-hex** → เก็บ **เฉพาะ SHA-256 hash**
   (`token_hash` UNIQUE) → **คืน raw token ครั้งเดียว** (admin เอาไปส่งให้ผู้เซ็น). ผูกกับ task.
2. **ผู้เซ็นเปิดลิงก์:** `GET /external/document` (header `X-Signer-Token: <raw>`)
   → API hash แล้วหา row → เช็ค status + `token_expires_at`. คืน metadata + เปิดดู PDF
   (`GET /external/document/file/original`).
3. **เซ็น:** `POST /external/sign` {signature_image_hash, consent_text, request_id}
   → บันทึก `signature_event` (signer_type='external') → engine ดัน step ต่อ.
4. **token state:** `checkTokenState()` → ถ้าหมดอายุ/ใช้แล้ว/ยกเลิก คืน code ที่เหมาะสม.
5. **rate limit:** มี in-memory limiter (`rateLimitWindow=1m`, janitor 2m) กัน brute-force token.
6. **resend:** `POST .../resend` → gen token ใหม่ (raw คืนครั้งเดียว, เก็บใน useRef ฝั่ง FE
   ไม่ลง state). cancel: `POST .../cancel`.

**Security:** token ไม่เคยอยู่ใน URL (header เท่านั้น), เก็บ hash, มี OTP field
(`otp_verified_at`) เตรียมไว้แต่ Phase 1 ยังไม่บังคับ OTP flow เต็ม.

---

## ภาคผนวก D — JWT / RBAC รายละเอียด

- **Access token:** JWT, TTL **15 นาที** (`AccessTokenTTL`). claims = {UserID, Username, Roles[]}.
- **Refresh token:** TTL **7 วัน** (`RefreshTokenTTL`), เก็บ **hash** ใน `refresh_tokens`
  (migration 0003), one row ต่อ session. logout = ลบ row.
- **RBAC:** `RequireRole(...)` = ผ่านถ้ามี **อย่างน้อย 1** role ที่ตรง (ไม่ใช่ทั้งหมด),
  ไม่ผ่าน → `403 forbidden`. `ClaimsFrom(c)` ดึง claims ที่ middleware เก็บไว้.

### Role × Endpoint matrix (verified จาก main.go)
| Endpoint | system_admin | workflow_admin | document_admin | auditor | signer |
|---|:-:|:-:|:-:|:-:|:-:|
| GET /documents (list) | ✅ | — | ✅ | ✅ | — |
| POST /import | ✅ | ✅ | ✅ | ✅ | ✅ (auth พอ) |
| external-signers invite/cancel/resend | ✅ | — | ✅ | — | — |
| external-signers list | ✅ | — | ✅ | ✅ | — |
| finalize | ✅ | — | ✅ | — | — |
| workflow-templates (ทั้งหมด) | ✅ | ✅ | — | — | — |
| signature-tasks (inbox/sign/reject) | ✅ | ✅ | ✅ | ✅ | ✅ (auth พอ) |
> หมายเหตุ: import + signature-tasks ใช้แค่ `requireAuth` (ไม่ได้ล็อก role) — ทุก user ที่
> login ได้เรียกได้. ถ้าต้องการจำกัดเฉพาะ role ต้องเพิ่ม RequireRole.

---

## ภาคผนวก E — sml-api-bybos endpoints ที่ต้องเพิ่ม (สำหรับทำ SML ต่อ)

> repo: `https://github.com/bosocmputer/sml-api-bybos.git`. PaperLess เรียกผ่าน HTTP.
> Convention ของ gateway: `X-Api-Key` auth, `X-Tenant` (tenant middleware →
> `h.dbm.Get(ctx, middleware.TenantKey)`), zap log, `X-Request-ID`, envelope
> `{success,data,meta}`, optional `erp_logs` write (graceful `log_status:"warning"`).

| Endpoint | สถานะ | รายละเอียด |
|---|---|---|
| `POST /api/v1/documents/:doc_no/lock` | ✅ **ทำแล้ว** (container `-paperless:8201`) | `locateDocForLock()` auto-discover ic_trans/ap_ar_trans → `UPDATE is_lock_record=1`. คืน {is_lock_record, already_locked}. 404 ถ้าไม่เจอ doc. |
| `GET /api/v1/ic/doc-formats?screen_code=` | ✅ มีเดิม | อ่าน `erp_doc_format` → doc_format_code catalog |
| `GET /api/v1/paperless/documents/:doc_no` | ❌ ยัง | ดึง metadata + PDF ref เพื่อ import auto (ตอนนี้ import = manual upload) |
| (reconcile) อ่าน is_lock_record กลับ | ❌ ยัง | สำหรับ drift report — อ่าน state ปัจจุบันมาเทียบ |

**Blockers ฝั่ง gateway (ดู §6.6):** trans_flags PA(12)/PB(213)/PV(19) ใน `doc_no.go` +
`transaction.go`; ap_ar_trans path ยังไม่พิสูจน์.

---

## ภาคผนวก F — Error Codes (machine-readable, FE map → ไทย)

| code | HTTP | ความหมาย |
|---|---|---|
| `invalid_request` | 400 | input ผิด (validation) |
| `invalid_form` | 400 | parse multipart ไม่ได้ |
| `invalid_file_type` | 400 | ไม่ใช่ PDF |
| `unauthorized` | 401 | token หมด/ไม่มี |
| `forbidden` | 403 | role ไม่พอ |
| `not_found` | 404 | ไม่เจอ resource |
| `revision_conflict` | 409 | doc_no เดิม + ไฟล์ต่าง (import) |
| `version_conflict` | 409 | race ตอน clone/create template |
| `not_draft` *(Phase B)* | 409 | แก้ template ที่ไม่ใช่ draft |
| `workflow_config_missing` | 422 | ไม่มี active template สำหรับ doc_format นี้ |
| `no_steps` *(Phase B)* | 422 | publish template ที่ไม่มี step |
| `file_too_large` | 413 | PDF > 50MB |
| `internal_error` | 500 | error ฝั่ง server |
| `parse_error` | (FE) | decode response ไม่ได้ |

---

## ภาคผนวก G — Environment Variables (config.go)

| ENV | default | ใช้ทำอะไร |
|---|---|---|
| `DATABASE_URL` | postgres://...localhost:5432/paperless | **required** |
| `JWT_SECRET` | (ว่าง) | sign/verify JWT — ต้องตั้ง |
| `MINIO_ENDPOINT` | localhost:9000 | object store |
| `MINIO_ACCESS_KEY` / `SECRET_KEY` | (ว่าง) | creds MinIO |
| `MINIO_BUCKET` | paperless | bucket |
| `MINIO_USE_SSL` | false | |
| `SML_API_BASE_URL` | http://192.168.2.109:8200 | **ตั้งเป็น `http://sml-api-bybos-paperless:8201`** บน prod |
| `SML_API_KEY` | (ว่าง) | **ถ้าว่าง = worker disabled** (NewClient คืน nil) |
| `SML_TENANT` | (ว่าง) | เช่น `sml1_2026` |
> prod compose ต้อง forward ทั้ง 3 ตัว SML ใน `environment:` block (ไม่ใช่แค่ใน .env)
> ไม่งั้น worker stays disabled. (บทเรียนจาก Phase 5 deploy.)

---

## ภาคผนวก H — PDF Finalize (สถานะปัจจุบัน vs เป้าหมาย)

- **ปัจจุบัน (Phase 1):** `FinalizeDocument()` สร้าง **หน้า evidence แนบท้าย** (appended
  page) — รวมรายชื่อผู้เซ็น + signature hash + timestamp + original PDF hash.
  **idempotent** (มี final_pdf แล้วคืนตัวเดิม). เก็บ `signature_image_hash` (sha-256)
  **ไม่เก็บรูปลายเซ็นจริง** ใน evidence page.
- **เป้าหมาย (จาก SML facts):** stamp-in-place ในช่องลายเซ็นของเอกสาร (PO = 4 ช่อง
  ท้ายหน้าสุดท้าย) — **ยังไม่ทำ**, รอพิกัด pt ต่อ doc_format จาก SML.
- **ข้อควรรู้:** final PDF download (`/file/final`) ต้อง status='completed'; original
  download ไม่ต้อง. ทั้งคู่เช็ค per-document access (`canAccessDocument`).

---

## ภาคผนวก I — Migrations (ประวัติ schema)

| ไฟล์ | เนื้อหา |
|---|---|
| `0001_init` | 13 ตารางหลัก |
| `0002_seed_dev` | seed users (admin/maker/checkerA/checkerB/approver) + roles + POP template **(DEV)** |
| `0003_add_auth_fields` | `users.password_hash` + ตาราง `refresh_tokens` |
| `0004_seed_dev_passwords` | bcrypt `password123` ให้ทุก seed user **(DEV — ต้อง clear ก่อน prod, ดู pilot-prep-plan)** |
| `0005_seed_pop_external_step` | template `DEMO3` (draft) โชว์ condition 1/2/3 **(DEV)** |
| `0006_request_id_unique` | partial unique index `(task_id, request_id)` — idempotency ระดับ DB |
> **กฎ:** เพิ่มไฟล์ใหม่เท่านั้น (0007+). ห้ามแก้ 0001–0006. seed DEV (0002/0004/0005)
> ห้ามรันใน prod — pilot ต้อง clear dev password (migration 0007 ตาม `pilot-prep-plan.md`).

---

## ภาคผนวก J — Seed Users (DEV เท่านั้น)

| username | display_name | roles | password (dev) |
|---|---|---|---|
| `admin` | ผู้ดูแลระบบ | system_admin, workflow_admin, document_admin | password123 |
| `maker` | ผู้จัดทำ | signer | password123 |
| `checkerA` | ผู้ตรวจสอบ A | signer | password123 |
| `checkerB` | ผู้ตรวจสอบ B | signer | password123 |
| `approver` | ผู้อนุมัติ | signer | password123 |
> POP template (active) ใช้ maker→checker→approver. **ต้องเปลี่ยน/ลบรหัสก่อน pilot.**
