# Import UI Plan — หน้าจอ "นำเข้าเอกสาร" (Opus plan, Sonnet implements)

**Goal:** เพิ่ม UI ให้ admin อัปโหลด PDF + กรอก metadata เพื่อ import เอกสารเข้า
PaperLess. Backend endpoint `POST /api/v1/documents/import` **มีอยู่แล้วและทำงาน
จริง** (verified ผ่าน curl บน prod) — งานนี้คือ **frontend ล้วน**: เพิ่ม
`api.importDocument()` + ปุ่ม/dialog ในหน้า `admin/documents`. ไม่แตะ backend.

**Read first:** `apps/web/src/app/(app)/admin/documents/page.tsx`,
`apps/web/src/lib/api.ts`, this file.
**Test target:** หลัง deploy คุณ (ผู้ใช้) จะ upload `PO26060001.pdf` ผ่าน UI ได้.

---

## หลักฐาน (verified โดย Opus — Sonnet ไม่ต้องค้นซ้ำ)

**Backend endpoint (มีแล้ว, อย่าแก้)** — `apps/api/internal/handlers/documents.go`:
- `POST /api/v1/documents/import` — **multipart/form-data** (ไม่ใช่ JSON).
- Required fields: `doc_format_code` (≤50 ชาร์), `doc_no` (≤100 ชาร์), `file` (PDF).
- Optional: `revision` (int, default 0), `doc_date` (YYYY-MM-DD), `amount` (decimal),
  `source_doc_no`.
- Validation: bad `doc_date`/`amount` → `400 invalid_request` (ไม่ใช่ 500).
- Success → **HTTP 201** envelope `{success:true, data:{id, doc_format_code, doc_no,
  revision, status:"pending", duplicate:false}}`.
- Dedup: ส่งไฟล์เดิม+key เดิม → `duplicate:true` + doc เดิม; key เดิมแต่ไฟล์ต่าง →
  `409` revision-conflict.
- Import จะเปิด first-sequence tasks ให้อัตโนมัติ (`OpenFirstSequence`) — หลัง import
  สำเร็จ เอกสารพร้อมให้ maker เซ็นทันที (ไม่ต้องทำอะไรเพิ่ม).
- Route ต้อง auth (อยู่ใน `docsG` group, `requireAuth`); admin มี role พอแล้ว.

**api.ts client pattern** — `apps/web/src/lib/api.ts`:
- `BASE = "/api/v1"` (browser ใช้ Next proxy, ไม่มี CORS).
- `request<T>()` (บรรทัด 11) **hardcode `Content-Type: application/json`** ทุก call.
  → **PITFALL:** import เป็น multipart — **ห้ามใช้ `request()` ตรง ๆ** และ **ห้าม set
  Content-Type เอง** สำหรับ multipart (ต้องให้ browser ใส่ `boundary` ให้). ต้องเขียน
  helper แยก (เหมือน `externalRequest` ที่แยกออกมา บรรทัด 29) ที่ส่ง `FormData`
  เป็น body โดย **ไม่ตั้ง Content-Type**.
- Auth: ใส่ `headers["Authorization"] = "Bearer " + token`. token มาจาก
  `getAccessToken()` (`@/lib/auth`).
- ทุก call ใส่ `X-Request-ID: crypto.randomUUID()`.
- `api` object (บรรทัด 208) รวม method ทั้งหมด — เพิ่ม `importDocument` ที่นี่.

**หน้า admin/documents** — `apps/web/src/app/(app)/admin/documents/page.tsx`:
- `"use client"`, ใช้ `useState/useEffect/useCallback/useRef`, `useRouter`.
- `getAccessToken()` + `getUser()` จาก `@/lib/auth`; `ErrorState` component มีอยู่.
- Header (บรรทัด 109–120): `<div className="...flex items-center justify-between
  gap-2">` มี `<h1>เอกสารทั้งหมด</h1>` ซ้าย + ปุ่ม **"Workflow Templates"** ขวา
  (`router.push("/admin/workflows")`). **วางปุ่ม "นำเข้าเอกสาร" ข้างปุ่มนี้** — mirror
  className เดิมของปุ่ม Workflow Templates เป๊ะ.
- หน้ามี `fetchDocs`/reload pattern อยู่แล้ว — หลัง import สำเร็จให้ reload list.
- ErrorState รองรับ code → ข้อความไทย (เช่น `attachment_upload_failed`).

**doc_format_code** — pilot ใช้ `POP` (template active เดียวในระบบ — verified DB).
ไม่ต้องดึง dropdown จาก SML ในเฟสนี้: ใช้ text input (placeholder "POP") หรือ
default "POP" ก็พอ. (มี endpoint `GET /ic/doc-formats` แต่ over-engineering สำหรับ
pilot — อย่าเพิ่งทำ.)

---

## ไฟล์ที่ต้องแก้/สร้าง

1. **`apps/web/src/lib/api.ts`** — เพิ่ม:
   - helper `importRequest()` (หรือ inline ใน method) ที่:
     - body = `FormData`; **ไม่ตั้ง `Content-Type`** (ให้ browser ใส่ boundary).
     - ตั้ง `Authorization: Bearer <token>` + `X-Request-ID`.
     - `fetch(`${BASE}/documents/import`, {method:"POST", body: formData, headers})`.
     - parse envelope แบบเดียวกับ `request()` (return `ApiResult<T>`).
   - `api.importDocument(token, fields)` — `fields: { file: File; doc_format_code:
     string; doc_no: string; revision?: number; doc_date?: string; amount?: string }`.
     สร้าง `FormData`, append เฉพาะ field ที่มีค่า (อย่า append empty optional).
   - type `ImportResult = { id:number; doc_format_code:string; doc_no:string;
     revision:number; status:string; duplicate:boolean }`.

2. **`apps/web/src/app/(app)/admin/documents/page.tsx`** — เพิ่ม:
   - ปุ่ม **"นำเข้าเอกสาร"** ใน header ข้าง "Workflow Templates" (mirror className).
   - state เปิด/ปิด dialog (`showImport`), form state (file, doc_no, doc_format_code
     default "POP", optional doc_date/amount), submitting, error.
   - Dialog/modal (inline ในไฟล์นี้ก็ได้ — ดู pattern modal ที่อาจมีในหน้าอื่น เช่น
     `admin/documents/[id]/page.tsx` ก่อน; ถ้าไม่มี ทำ overlay div ง่าย ๆ):
     - `<input type="file" accept="application/pdf">` (ใช้ `useRef` หรือ onChange).
     - input `doc_no` (required), `doc_format_code` (default "POP"), optional
       `doc_date` (type=date), `amount`.
     - ปุ่ม "อัปโหลด" → call `api.importDocument()`.
   - On success: ปิด dialog, reload list, แสดง toast/ข้อความ "นำเข้าสำเร็จ"
     (+ ถ้า `duplicate:true` บอก "เอกสารนี้มีอยู่แล้ว").
   - On error: แสดงข้อความจาก `error.code` ผ่าน ErrorState/inline (เช่น 409 →
     "เลขเอกสารนี้มีอยู่แล้วด้วยไฟล์อื่น"; 400 → ข้อความ validation).
   - Disabled state: ปุ่มอัปโหลด disable ระหว่าง submitting + เมื่อยังไม่เลือกไฟล์/
     ไม่กรอก doc_no.

3. (ถ้าจำเป็น) **component modal แยก** — ถ้า inline แล้วไฟล์ใหญ่เกิน ให้แยกเป็น
   `apps/web/src/components/ImportDialog.tsx` แต่ **อย่า over-engineer** — inline ได้
   ก็ inline.

---

## Decisions ตัดสินแล้ว (Sonnet ห้ามเปลี่ยน)

- **multipart, ไม่ใช่ JSON** — ห้ามใช้ `request()` เดิม (มัน hardcode JSON
  Content-Type). เขียน helper ที่ส่ง FormData โดยไม่ตั้ง Content-Type.
- **doc_format_code ใช้ text input default "POP"** — ไม่ดึง dropdown จาก SML ในเฟสนี้.
- **ไม่แตะ backend** — endpoint ทำงานครบแล้ว. งานนี้ frontend ล้วน.
- **append เฉพาะ optional field ที่มีค่า** — อย่าส่ง `doc_date=""`/`amount=""` (จะให้
  backend validate ค่าว่างโดยไม่จำเป็น; backend skip ถ้าว่างอยู่แล้วแต่กันไว้).
- **หลัง import สำเร็จ reload list** — เอกสารใหม่ต้องโผล่ในตารางทันที.
- **วางปุ่มใน header เดิม** ข้าง Workflow Templates — ไม่สร้าง layout ใหม่.

## Done when

- `cd apps/web && npm run build` ผ่าน (ไม่มี TS error) — นี่คือ gate หลักของเฟสนี้
  (ไม่มี FE test runner ในโปรเจกต์; build = type-check).
- `npm run lint` (ถ้ามี script) ผ่าน.
- Manual (หลัง deploy ไป server): กดปุ่ม "นำเข้าเอกสาร" → เลือก PDF → กรอก doc_no
  "PO26060001" + format "POP" → อัปโหลด → เห็น "นำเข้าสำเร็จ" + เอกสารโผล่ในตาราง +
  status "pending". (Opus จะ verify ขั้นนี้ตอน audit บน server — Sonnet แค่ทำให้
  build ผ่าน.)
- อัปโหลดไฟล์เดิมซ้ำ → ข้อความ "มีอยู่แล้ว" (duplicate), ไม่ใช่ error 500.

## ห้าม / ระวัง (invariants)

- **ห้ามตั้ง `Content-Type` สำหรับ multipart** — browser ต้องใส่ `boundary` เอง;
  ถ้าตั้งเอง backend parse multipart ไม่ได้ → 400. (นี่คือบั๊กที่เจอบ่อยสุดของงานนี้.)
- ห้าม log token/password; token อยู่ใน Authorization header เท่านั้น ไม่ใส่ใน URL.
- ห้ามแก้ backend / endpoint / migration.
- รักษา pattern เดิม (className, ErrorState, getAccessToken) — อย่า refactor หน้าเดิม
  เกินที่จำเป็นเพื่อใส่ปุ่ม+dialog.
- ไฟล์ใหญ่ (>50MB) backend จะ reject (maxUploadBytes) — ฝั่ง UI ไม่ต้องเช็ค size เอง
  แต่ถ้า backend คืน error ให้แสดงข้อความสุภาพ.
- ถ้า build เจอ type ของ `api`/`auth` ไม่ตรง — แก้ที่ฝั่งเรียก ไม่ใช่ลด strictness.
