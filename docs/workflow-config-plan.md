# Admin Overhaul — Master Plan (Opus plans, Sonnet implements)

**ขอบเขต (ผู้ใช้เลือก): "ยกเครื่อง admin ทั้งระบบ"** — ไม่ใช่แค่ workflow config.
ทำเป็น **3 phase เรียงลำดับ** เพื่อให้ deploy/ทดสอบได้ทีละก้อน ไม่ต้องรอจบทั้งหมด:

- **Phase A — Admin shell + navigation** (เล็ก, ทำก่อน, แก้ "งง" ทันที)
- **Phase B — Workflow config editor** (ใหญ่สุด, แก้ "กำหนดอะไรไม่ได้")
- **Phase C — User management** (สร้าง/แก้ user + role; ตอนนี้ user seed ใน DB เพิ่มไม่ได้)

> **แนะนำ implement ทีละ phase แล้ว build/commit ก่อนขึ้น phase ถัดไป.** Opus จะ
> audit + deploy + ทดสอบจริงท้ายแต่ละ phase. ถ้าเวลาจำกัด ทำ A→B ก่อน, C ตามได้.

---

## สิ่งที่มี vs ขาด (inventory — verified จาก main.go + migrations 0001/0002)

**Backend routes ที่มีจริง:** auth(login/refresh/logout/me); documents(list/import/get/
attachments/audit-logs/workflow-status/external-signers invite·list·cancel·resend/finalize/
file download); workflow-templates(**list/get/clone/publish/deactivate เท่านั้น**);
signature-tasks(inbox/get/sign/reject).

**ขาดหายในระบบ (ต้องสร้างใหม่):**
- ❌ create/update workflow template + steps + assignees (Phase B)
- ❌ list/create/update users, assign roles (Phase C) — **ไม่มี `/users` เลย**
- ❌ admin navigation shell / landing (Phase A)

**Roles ที่ seed ไว้ (0002):** system_admin, workflow_admin, document_admin, signer,
auditor, integration.

**หน้า FE ที่มี:** login, inbox, documents/[id], admin/documents (+[id] — มี invite/
cancel/resend/audit/download ครบแล้ว), admin/workflows (read-only), external/[token].

---

## Problem เดิม (ที่จุดประกายงานนี้)

"ADMIN ควรทำได้ทุกอย่าง แต่หน้า Config เอกสารกำหนดอะไรไม่ได้"
Admin login มาเจอหน้า inbox (หน้าของ user) ไม่มี navigation, และหน้า Workflow
Templates เป็น **read-only** — clone/publish/deactivate ได้ แต่ **สร้าง template ใหม่
ไม่ได้ และแก้ขั้นตอน/ผู้เซ็นไม่ได้เลย**.

**Root cause (verified จาก source):** backend **ไม่มี endpoint** สำหรับ create/update
template, create/update steps, set assignees, หรือ list users. มีแค่
`GET list`, `GET /:id`, `POST /:id/clone`, `POST /:id/publish`, `POST /:id/deactivate`
(main.go:135–143). Template "POP" ที่ใช้อยู่ถูก seed เข้า DB ตอน migration — ไม่ได้
สร้างผ่านหน้าจอ. ดังนั้นงานนี้คือ **backend ใหม่ + frontend editor ใหม่ + admin shell**,
ไม่ใช่แค่แต่ง UI.

**Read first (source of truth — Sonnet ต้องอ่านก่อน):**
- `apps/api/cmd/server/main.go:110–146` (routes)
- `apps/api/internal/handlers/workflow_templates.go` (List/Get + response structs)
- `apps/api/internal/handlers/workflow_templates_lifecycle.go` (Clone/Publish/Deactivate — pattern ของ tx, error envelope, version bump)
- `apps/api/migrations/0001_init.up.sql` (3 ตาราง — ดู schema ด้านล่าง)
- `apps/web/src/app/(app)/admin/workflows/page.tsx` (read-only UI เดิม)
- `apps/web/src/lib/api.ts` (client pattern, `request()`)
- this file

---

## หลักฐาน / Invariants (verified โดย Opus — ห้ามเปลี่ยน, ห้าม assume เป็นอย่างอื่น)

### Schema (0001_init.up.sql) — สามตาราง
```
workflow_templates(
  id PK, doc_format_code text, name text, version int,
  status text CHECK IN ('draft','active','inactive') DEFAULT 'draft',
  effective_from timestamptz, created_by bigint→users, created_at,
  UNIQUE(doc_format_code, version))

workflow_steps(
  id PK, workflow_template_id bigint→templates ON DELETE CASCADE,
  position_code text, position_name text, sequence_no int,
  condition_type smallint CHECK IN (1,2,3), signature_slot jsonb,
  UNIQUE(workflow_template_id, position_code))

workflow_step_assignees(
  id PK, workflow_step_id bigint→steps ON DELETE CASCADE,
  user_id bigint→users, display_order smallint,
  UNIQUE(workflow_step_id, user_id))

users(id PK, username UNIQUE, display_name, email, phone,
      status CHECK IN ('active','inactive') DEFAULT 'active', ...)
```

### Invariant ที่สำคัญที่สุด — **EDIT ได้เฉพาะ draft**
`documents.workflow_template_id` ผูกกับ template ตอน import (documents.go:202) และ
engine อ่าน steps จาก template id นั้น **ตลอด lifecycle** ของการเซ็น
(engine.go:341,398,414,445,470). **ถ้าแก้ steps ของ template ที่มี document กำลังวิ่งอยู่
= ทำลาย workflow ที่ค้างอยู่กลางคัน.** ดังนั้น:
- **create/update steps/assignees ทำได้เฉพาะ template ที่ `status='draft'`** เท่านั้น.
- template `active`/`inactive` = **immutable** → ถ้าจะแก้ ต้อง **Clone (→ draft ใหม่,
  version +1) → แก้ draft → Publish** (มี endpoint clone/publish อยู่แล้ว).
- ทุก write endpoint ต้องเช็ค `status='draft'` ก่อน, ไม่ใช่ → `409 not_draft`.

### condition_type (semantic — ห้ามเปลี่ยนความหมาย)
- `1` = คนใดคนหนึ่งเซ็น (any-one) — ต้องมี assignees ≥1
- `2` = ทุกคนต้องเซ็น (all) — ต้องมี assignees ≥1
- `3` = ผู้เซ็นภายนอก (external) — **ไม่มี** assignees (เชิญตอน import/runtime)

### Publish invariant (มีอยู่แล้วใน Publish handler — อย่าทำซ้ำผิด)
Publish จะ set `status='active'` + `effective_from=now()` และ deactivate version
active เดิมของ doc_format_code เดียวกัน. **ก่อน publish ควรกันไม่ให้ publish template
ที่ steps ว่าง** — ใส่ validation นี้ใน create-flow/publish (ดู "Backend งานที่ 5").

### Error envelope (httpx) — ใช้ pattern เดิมเป๊ะ
`httpx.OK(c, status, data)` → `{success:true, data}`
`httpx.Error(c, status, code, message)` → `{success:false, error:{code, message}}`
codes ต้องเป็น snake_case machine-readable (เช่น `not_draft`, `invalid_request`,
`not_found`, `version_conflict`). FE map code → ข้อความไทย.

### api.ts client — มี `request<T>(path, opts, token)` ที่ hardcode JSON Content-Type
(บรรทัด 11). endpoints ใหม่ทั้งหมดเป็น JSON → **ใช้ `request()` ได้ตรง ๆ** (ต่างจาก
import multipart). ใส่ method ใน `api` object (บรรทัด ~208).

---

---

# PHASE A — Admin shell + navigation (ทำก่อน, เล็ก)

แก้อาการ "งง ไม่มีเมนู" ทันที โดยไม่ต้องรอ backend ใหม่. **Frontend ล้วน.**

### A1. Admin layout (nav shell)
- ไฟล์ใหม่ `apps/web/src/app/(app)/admin/layout.tsx` — top-nav bar ครอบทุกหน้า admin:
  - ลิงก์: **เอกสาร** (`/admin/documents`), **ตั้งค่า Workflow** (`/admin/workflows`),
    และ (เตรียมไว้สำหรับ Phase C) **ผู้ใช้** (`/admin/users`).
  - แสดง display_name + role + ปุ่ม **ออกจากระบบ** (logout: เรียก `api.logout` +
    clear token ผ่าน `@/lib/auth`, แล้ว `router.replace('/login')`).
  - active state ตาม `usePathname()`.
  - **mobile-friendly top-bar** (โปรเจกต์เป็น PWA, หน้าเดิมใช้ sticky header +
    max-w-3xl) — ห้ามทำ desktop-only sidebar ที่พังบนมือถือ.
- เอา per-page header ปุ่มกระโดด (เช่นปุ่ม "Workflow Templates"/"เอกสาร" ที่มุมขวาของ
  แต่ละหน้า) ออก/ลดรูป เพราะ nav กลางทำหน้าที่นี้แทน — แต่ **อย่าลบ header ทั้งหมด**
  (ปุ่ม "นำเข้าเอกสาร" ในหน้า documents ยังต้องอยู่).

### A2. Landing หลัง login แยกตาม role
- `apps/web/src/app/page.tsx` ตอนนี้เป็น server component ที่ `redirect('/inbox')` เสมอ.
- เปลี่ยนเป็น **client component**: อ่าน token + role (`getAccessToken`/`getUser`).
  - มี role admin (system_admin/workflow_admin/document_admin/auditor) → `replace('/admin/documents')`.
  - เป็น signer อย่างเดียว → `replace('/inbox')`.
  - ไม่มี token → `replace('/login')`.
  - ระหว่างตัดสินใจแสดง spinner (กัน flash). **อย่าพึ่ง localStorage บน server** —
    ทำเป็น client redirect.

### Phase A gate: `cd apps/web && npm run build` ผ่าน. Manual: admin login → ไป
/admin/documents เห็น nav bar; signer login → ไป /inbox.

---

# PHASE B — Workflow config editor (ใหญ่สุด)

## BACKEND — endpoints ใหม่ (Go, package handlers)

> ทุก endpoint อยู่ใต้ guard เดิม: `requireAuth` + `RequireRole("workflow_admin","system_admin")`
> (เหมือน wfTmplG group, main.go:135). ใช้ `middleware.ClaimsFrom(c)` หา UserID.
> ทุก write ใช้ transaction + `defer tx.Rollback(ctx)` ตาม pattern ใน lifecycle.go.

### 1. `GET /users` — list ผู้ใช้ (สำหรับเลือก assignee)
- ไฟล์ใหม่ `internal/handlers/users.go` (+ `users_test.go`).
- Register ใน main.go ใต้ guard เดียวกับ workflow templates (workflow_admin/system_admin
  เห็นรายชื่อ user ได้ — หรือถ้าจะกว้างกว่านั้นให้แค่ requireAuth ก็ได้ แต่ default ให้
  ตรงกับ workflow_admin/system_admin เพื่อ least-privilege).
- Query: `SELECT id, username, display_name, status FROM users WHERE status='active' ORDER BY display_name`.
- Response: `httpx.OK(c, 200, []{id(string via strconv), username, display_name, status})`.
- **id เป็น string** (FormatInt) ให้ตรงกับ pattern ที่ FE ใช้ (TemplateAssignee.user_id เป็น string).

### 2. `POST /workflow-templates` — สร้าง draft template เปล่า
- ไฟล์: เพิ่ม handler `Create` ใน `workflow_templates_lifecycle.go` (หรือไฟล์ใหม่
  `workflow_templates_edit.go` — แล้วแต่ Sonnet, แต่ให้รวม edit ทั้งหมดไว้ที่เดียว).
- Body JSON: `{doc_format_code: string (req, ≤50, upper-trim), name: string (req, ≤200)}`.
- Validate: required + length. trim + uppercase doc_format_code (ให้ตรงกับ import/list).
- version = `MAX(version)+1` ของ doc_format_code นั้น (เริ่มที่ 1 ถ้ายังไม่มี) — reuse
  logic จาก Clone (lifecycle.go:67–78).
- INSERT `status='draft', created_by=claims.UserID`. duplicate (doc_format_code,version)
  → `409 version_conflict` (ใช้ `isDuplicateKeyHandler`).
- audit: `INSERT audit_logs(... action='template_created', entity_type='workflow_template', entity_id=newID)`.
- Response `httpx.OK(c, 201, {id, doc_format_code, name, version, status:'draft'})`.

### 3. `PUT /workflow-templates/:id` — แก้ชื่อ template (draft เท่านั้น)
- Body: `{name: string (req, ≤200)}`. (doc_format_code/version ห้ามแก้ — เป็น identity).
- เช็ค `SELECT status ... FOR UPDATE`; ถ้าไม่เจอ → 404; ถ้า status≠'draft' → `409 not_draft`.
- UPDATE name. audit `template_updated`. Response OK 200 {id, name}.

### 4. `PUT /workflow-templates/:id/steps` — **แทนที่ steps ทั้งชุด** (draft เท่านั้น)
> วิธีที่ทนทานที่สุด: replace-all ใน 1 transaction (ลบ steps เดิม → insert ใหม่ตามที่ส่งมา).
> ง่ายต่อ FE (ส่งทั้ง array) และตัด edge case ของ partial update.
- Body JSON:
  ```
  { steps: [
      { position_code: string (req, ≤?), position_name: string (req),
        sequence_no: int (req, ≥1), condition_type: 1|2|3 (req),
        assignee_user_ids: number[] (req for type 1,2 — ≥1; ต้องว่าง/ละเว้นสำหรับ type 3) }
    ] }
  ```
- Validate (ก่อนแตะ DB ทั้งหมด → 4xx ไม่ใช่ 500):
  - template exists + `status='draft'` (FOR UPDATE) ไม่งั้น 404 / `409 not_draft`.
  - steps ไม่ว่าง (≥1).
  - sequence_no: ทุกตัว ≥1, **ไม่ซ้ำกัน** (unique เพื่อ ordering ที่ชัด). แนะนำบังคับให้
    เรียง 1..N ต่อเนื่อง หรืออย่างน้อย unique+sorted — เลือก **unique** เป็นขั้นต่ำ.
  - position_code: ต้อง **unique ภายใน template** (มี UNIQUE constraint อยู่แล้ว — แต่เช็ค
    ใน Go ก่อนเพื่อ error ที่อ่านง่าย ไม่ใช่ 23505 ดิบ).
  - condition_type ∈ {1,2,3}.
  - type 1/2 → assignee_user_ids ≥1 และทุก id **มีอยู่จริงใน users + status='active'**
    (query ตรวจ; id ที่ไม่ valid → `400 invalid_assignee`).
  - type 3 → assignee_user_ids ต้องว่าง (ถ้าส่งมา → `400 external_step_has_assignees`).
- Transaction: `DELETE FROM workflow_steps WHERE workflow_template_id=$1` (cascade ลบ
  assignees), แล้ว loop insert steps + assignees (display_order = ลำดับใน array).
- audit `template_steps_updated`. Response OK 200 — return template detail แบบเดียวกับ
  GET /:id (reuse logic ถ้าทำได้ หรือ minimal {id, step_count}).

### 5. (ปรับ Publish ที่มีอยู่) — กัน publish template ที่ steps ว่าง
- ใน `Publish` (lifecycle.go:217) **เพิ่ม guard**: ก่อน set active, เช็ค
  `SELECT count(*) FROM workflow_steps WHERE workflow_template_id=$1` > 0,
  ไม่งั้น `422 no_steps` ("ไม่สามารถเผยแพร่ workflow ที่ไม่มีขั้นตอน").
- **อย่าแก้ส่วนอื่นของ Publish** — แค่เพิ่ม guard ต้น ๆ.

### 6. (ออปชัน, ทำถ้าเหลือเวลา) `DELETE /workflow-templates/:id` — ลบ draft
- เฉพาะ `status='draft'` (active/inactive ห้ามลบ — เป็นประวัติ). CASCADE ลบ steps/assignees.
- ถ้าไม่ใช่ draft → `409 not_draft`. audit `template_deleted`. ถ้าไม่ทำในรอบนี้ ข้ามได้.

### Register routes (main.go ใน block wfTmplG):
```
wfTmplG.POST("",            wfTmplH.Create)
wfTmplG.PUT("/:id",         wfTmplH.Update)
wfTmplG.PUT("/:id/steps",   wfTmplH.UpdateSteps)
// DELETE optional
```
+ users group (ไฟล์ใหม่ users.go, handler+constructor) ใต้ guard เดียวกัน:
```
v1.GET("/users", requireAuth, middleware.RequireRole("workflow_admin","system_admin"), userH.List)
```

### Backend tests (real DB — มี harness อยู่แล้ว ดู *_lifecycle_test.go):
- Create: happy path (201, version=1), version bump (สร้างซ้ำ doc_format → version=2),
  validation (ชื่อว่าง → 400).
- UpdateSteps: happy (replace ได้, assignees ถูก), **reject เมื่อ status≠draft (409)**,
  type3 + assignees → 400, type1 ไม่มี assignee → 400, invalid user id → 400,
  duplicate sequence_no → 400.
- Publish: reject steps ว่าง → 422.
- Users list: คืน active users, ไม่คืน inactive.
- **gate: `go build ./... && go vet ./...` ผ่าน + tests ที่เขียนใหม่ผ่านบน real DB**
  (รันบน server — local ไม่มี Docker, ดู [[local-no-docker-deploy-to-server]] /
  [[ssh-deploy-server-runbook]]).

---

## FRONTEND (Phase B) — template editor

> Admin shell + landing อยู่ใน **Phase A** แล้ว — Phase B ใช้ shell นั้นได้เลย.

### B. Template editor (หัวใจของงาน)
ขยายหน้า `admin/workflows/page.tsx` (หรือแยกหน้า `admin/workflows/[id]/page.tsx` สำหรับ
edit — แนะนำแยกหน้า edit เพื่อไม่ให้ไฟล์เดียวใหญ่เกิน):

1. **ปุ่ม "+ สร้าง Workflow ใหม่"** ในหน้า list → dialog: doc_format_code + name →
   `api.createTemplate()` → ได้ draft → ไปหน้า edit ของ id นั้น.
2. **หน้า/โหมด edit (draft เท่านั้น):**
   - แก้ name.
   - **จัดการ steps**: เพิ่ม/ลบ step, ตั้ง sequence (เรียงลำดับ — ปุ่มขึ้น/ลง หรือ number
     input), position_code, position_name, condition_type (dropdown: คนใดคนหนึ่ง/ทุกคน/
     ภายนอก), และ **เลือกผู้เซ็น** (multi-select จาก `api.listUsers()`; ซ่อน assignee
     selector เมื่อ condition_type=3).
   - ปุ่ม **"บันทึกขั้นตอน"** → `api.updateSteps(id, {steps})` (ส่งทั้ง array — replace-all).
   - ปุ่ม **"เผยแพร่"** → `api.publishTemplate(id)` (มีอยู่แล้ว) — disable ถ้า steps ว่าง.
3. **template active/inactive = read-only** ในหน้า edit. แสดงปุ่ม **"โคลนเพื่อแก้ไข"**
   (`api.cloneTemplate` — มีอยู่แล้ว) → ได้ draft ใหม่ → เด้งไป edit. (อธิบายให้ผู้ใช้เข้าใจ
   ว่าทำไมแก้ active ตรง ๆ ไม่ได้ — ข้อความเช่น "เวอร์ชันนี้ใช้งานอยู่ แก้ไม่ได้ ให้โคลน
   เป็นเวอร์ชันใหม่").

### C. api.ts — เพิ่ม client methods (JSON, ใช้ `request()` ได้ตรง ๆ)
```
createTemplate(token, {doc_format_code, name})           → POST /workflow-templates
updateTemplate(token, id, {name})                        → PUT  /workflow-templates/:id
updateSteps(token, id, {steps:[...]})                    → PUT  /workflow-templates/:id/steps
listUsers(token)                                         → GET  /users
// + types: UserOption, StepInput (position_code, position_name, sequence_no,
//   condition_type, assignee_user_ids:number[])
```
(clone/publish/deactivate มีอยู่แล้ว — reuse.)

### FE gate: `cd apps/web && npm run build` ผ่าน (type-check). ไม่มี FE test runner.

---

# PHASE C — User management (ทำหลัง A,B)

ตอนนี้ user มาจาก seed (0002) เท่านั้น — เพิ่ม/แก้ user หรือ role ผ่านระบบไม่ได้เลย.
Phase C เปิดให้ system_admin จัดการผู้ใช้.

> **Guard: `RequireRole("system_admin")` เท่านั้น** (จัดการ user = สิทธิ์สูงสุด —
> ไม่ให้ document_admin/workflow_admin แตะ).

### Backend (handlers/users.go — ต่อยอดจาก `GET /users` ที่สร้างใน Phase B)
- `GET /users` (มีแล้วจาก Phase B แต่ list แบบ active-only) → **เพิ่ม** return roles ของ
  แต่ละ user (JOIN user_roles+roles) และ option คืน inactive ด้วย (`?include_inactive=1`)
  สำหรับหน้าจัดการ. **อย่าทำให้ Phase B (step editor) พัง** — step editor ต้องการแค่
  active users; ถ้าเปลี่ยน shape ให้ทำ backward-compatible (เพิ่ม field roles ได้,
  อย่าลบ field เดิม).
- `POST /users` — `{username (req, unique, ≤?), display_name (req), email?, phone?, roles: string[] (role codes)}`.
  - validate: username ไม่ซ้ำ (เช็คก่อน → `409 username_taken` ไม่ใช่ 23505 ดิบ);
    roles ทุกตัวต้องมีใน roles.code (ไม่งั้น `400 invalid_role`).
  - tx: INSERT users (status='active') → INSERT user_roles. audit `user_created`.
  - **password:** ดู note ด้านล่าง — รอบนี้ user สร้างมาแบบ **ยังไม่มีรหัสผ่าน/ตั้งทีหลัง**
    (auth fields อยู่ migration 0003). ให้ทำตาม pattern เดียวกับ
    `docs/pilot-prep-plan.md` (set password ผ่าน script/endpoint แยก) — **อย่าฝัง
    password ใน create body แบบ plain.** ถ้าต้องตั้งรหัสตอนสร้าง ให้ bcrypt ฝั่ง server
    เท่านั้น และ **ห้าม log**.
- `PUT /users/:id` — แก้ display_name/email/phone/status(active|inactive)/roles (replace-all
  roles). validate role codes. audit `user_updated`. **ห้าม** ให้ปิด (inactive) ตัวเอง
  หรือถอด system_admin ออกจากตัวเอง (กัน lockout) → `409 cannot_demote_self`.
- `GET /roles` — list role codes+names (จากตาราง roles) ให้ FE ทำ checkbox.
- routes (main.go):
  ```
  usersG := v1.Group("/users", requireAuth, middleware.RequireRole("system_admin"))
  usersG.GET("", userH.List); usersG.POST("", userH.Create); usersG.PUT("/:id", userH.Update)
  v1.GET("/roles", requireAuth, middleware.RequireRole("system_admin"), userH.ListRoles)
  ```
  > **หมายเหตุ guard ขัดกัน:** Phase B ลง `GET /users` ใต้ guard
  > `workflow_admin|system_admin` (step editor ต้องใช้). Phase C อยากได้ system_admin-only.
  > **ทางออก:** แยก path — step editor ใช้ `GET /users` (workflow_admin|system_admin,
  > active-only, minimal fields); หน้าจัดการใช้ `GET /admin/users` (system_admin-only,
  > full fields+roles). อย่าให้ guard ชนกันบน path เดียว.

### Backend tests: create (happy/duplicate username 409/invalid role 400), update
(roles replace, self-demote guard 409), list roles. gate: build+vet+tests บน real DB.

### Frontend
- หน้าใหม่ `apps/web/src/app/(app)/admin/users/page.tsx` — list users (display_name,
  username, roles badges, status) + ปุ่ม "เพิ่มผู้ใช้" + แก้ราย user (dialog).
- ลิงก์ "ผู้ใช้" ใน admin nav (Phase A เตรียม slot ไว้แล้ว) — **แสดงเฉพาะ system_admin**.
- api.ts: `listAdminUsers`, `createUser`, `updateUser`, `listRoles` + types.
- gate: `npm run build` ผ่าน.

> **Phase C อาจรวมกับ `docs/pilot-prep-plan.md`** (password setup) — ดูก่อนว่าซ้อนกันไหม,
> ถ้าซ้อนให้ทำครั้งเดียว ไม่ทำซ้ำ.

---

## Decisions ตัดสินแล้ว (Sonnet ห้ามเปลี่ยน)
- **Edit เฉพาะ draft** — active/inactive immutable. แก้ = clone→edit→publish. (data-integrity
  invariant — ห้ามทำ shortcut ให้แก้ active ได้).
- **steps = replace-all (PUT ทั้ง array)** — ไม่ทำ PATCH ราย step (ลด edge case).
- **users list endpoint ใหม่** — จำเป็นเพราะ FE ต้องมีรายชื่อให้เลือก; ไม่มี endpoint เดิม.
- **id เป็น string ใน JSON** (FormatInt) — ตรง pattern เดิม, เลี่ยง JS number precision.
- **ไม่แตะ engine / signing / import** — งานนี้คือ config layer + nav เท่านั้น.
- **ไม่แก้ migration 0001–0006** — ถ้าจำเป็นต้องมี index/constraint เพิ่ม ให้สร้างไฟล์ใหม่
  (แต่ schema ปัจจุบันพอแล้ว — ไม่ต้องมี migration ใหม่สำหรับงานนี้).
- **nav เป็น top-bar mobile-friendly** ไม่ใช่ desktop sidebar (โปรเจกต์เป็น PWA).

## Done when (ต่อ phase — build/commit/deploy ทีละ phase)
**Phase A:** `npm run build` ผ่าน; admin login→/admin/documents เห็น nav; signer→/inbox.
**Phase B:** `go build ./... && go vet ./...` + tests ใหม่ผ่านบน real DB; `npm run build`
ผ่าน. Manual:
  - สร้าง workflow ใหม่ (doc_format ใหม่ เช่น DEMO) → เพิ่ม 2–3 steps + เลือกผู้เซ็น →
    บันทึก → เผยแพร่ → status active.
  - แก้ active template ตรง ๆ ไม่ได้ (ปุ่ม disabled/clone-prompt).
  - publish template ที่ไม่มี step → error "ไม่มีขั้นตอน".
**Phase C:** build+vet+tests; `npm run build`. Manual: system_admin สร้าง user ใหม่ +
ให้ role → user login ได้ (หลังตั้งรหัส); self-demote ถูกบล็อก.

> Opus จะ audit + deploy ไป server (192.168.2.109) + ทดสอบจริง **ท้ายแต่ละ phase**
> (ดู [[ssh-deploy-server-runbook]]). local ไม่มี Docker → tests/stack รันบน server.

## ห้าม / ระวัง (invariants)
- **ห้ามให้แก้ steps ของ template ที่ status≠'draft'** — จะพัง document ที่กำลังเซ็นอยู่
  (engine ผูกกับ template_id). ทุก write endpoint เช็ค draft ก่อน.
- ห้าม log token/PII. token อยู่ใน Authorization header เท่านั้น.
- ห้ามแตะ engine.go, import, signing, finalize.
- validate ทุก input ต้น handler → 4xx ที่อ่านได้ ไม่ใช่ปล่อยให้เป็น 500/23505 ดิบ.
- assignee user_id ต้อง exist + active — กัน FK error เป็น 500.
- รักษา error-envelope + className + auth pattern เดิม — อย่า refactor เกินจำเป็น.
- ใช้ `strconv.FormatInt` แปลง id→text (ไม่ใช่ `$N::text` ใน SQL) — ตาม convention เดิม.
