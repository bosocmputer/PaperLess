# คำถามสำหรับทีม SML — PaperLess Integration

> เอกสารนี้ใช้ส่งให้ทีม SML กรอกคำตอบกลับ แล้ว commit เก็บเป็นหลักฐานใน repo
> PaperLess เชื่อม SML **ผ่าน `sml-api-bybos` เท่านั้น** (ไม่ต่อ DB SML ตรง) ดู `docs/architecture.md`
>
> วิธีใช้: ทีม SML กรอกในช่อง **ตอบ:** ของแต่ละข้อ ข้อไหนยังไม่ชัดให้เขียน "ยังไม่ทราบ / ขอเวลาเช็ค"
>
> Legend ความสำคัญ: 🔴 Blocker (Phase 3 ทำไม่ได้ถ้าไม่มี) · 🟡 สำคัญ (มี fallback) · 🟢 ดีถ้ารู้

---

## 🔴 ส่วนที่ 1 — Blocker: sync สถานะกลับ SML

### Q1. การ "Confirm" เอกสารใน SML
เมื่อ PaperLess เซ็นครบแล้ว ต้อง update สถานะ "ยืนยันแล้ว" กลับ SML

- เก็บใน **table ไหน / column ไหน** สำหรับเอกสารแต่ละชนิด (POP, INV, PUP, PBV, PVV)?
- ค่าที่ต้องเขียนคืออะไร (เช่น flag = 1, หรือ status code เฉพาะ)?
- ต้องเขียน field ประกอบไหม เช่น `confirm_date`, `confirm_by`, user code?

**ทำไมต้องรู้:** นี่คือ output หลักของระบบ
**ถ้าไม่มี:** PaperLess เซ็นได้ แต่ SML ไม่รู้ว่าผ่านอนุมัติแล้ว → สองระบบสถานะไม่ตรง

**ตอบ:**
user จะเป็นคนละบุ รหัสเอกสาร ให้เพื่อจับคู่กับ table sml เช่น PO26060001

TABLE ic_trans.is_lock_record
0=ไม่ได้ lock
1=lock

ตัวอย่าง table
select * from ic_trans where doc_no = 'PO26060001' นี้คือเอกสาร ยังไม่ได้ lock
select * from ic_trans where doc_no = 'PO26060002' นี้คือเอกสาร lock

ลองดูใน ic_trans_detail ด้วยเพื่อให้ครอบคลุม

---

### Q2. การ "Lock" เอกสาร (กันแก้หลังเซ็น)
- เก็บใน **table ไหน / column ไหน**? เป็นคนละ field กับ Confirm หรือ field เดียวกัน?
- **เขียนซ้ำได้ไหม (idempotent)?** ถ้าส่ง lock ซ้ำหลัง timeout จะเกิดอะไร?

**ทำไมต้องรู้:** retry logic ต้องรู้ว่าเขียนซ้ำปลอดภัยไหม
**ถ้าไม่มี:** retry แล้วอาจทำข้อมูล SML เสีย

**ตอบ:**
TABLE ic_trans.is_lock_record
0=ไม่ได้ lock
1=lock

---

### Q3. PaperLess รับเอกสาร + PDF จาก SML ทางไหน
เลือกข้อที่ทำได้ (ตอบได้มากกว่า 1):

- [ ] (a) PaperLess **ดึงเอง** ผ่าน `sml-api-bybos` (อ่าน table SML) — *แนวที่ PaperLess ชอบสุด*
- [ ] (b) SML render PDF เก็บไว้ที่ใดที่หนึ่ง ให้ PaperLess ไปดึง
- [ ] (c) watched folder / scheduled push จาก SML
- [ ] (d) **manual upload เท่านั้น** (Phase 1 ใช้ชั่วคราว)

**ทำไมต้องรู้:** กำหนดวิธีออกแบบ import service
**ถ้าไม่มี:** Phase 1 ใช้ manual upload ได้ แต่ทำอัตโนมัติ (Phase 3) ไม่ได้

**ตอบ:**
(d) **manual upload เท่านั้น**

---

### Q4. ตัว PDF เอกสารมาจากไหน
- SML **สร้าง PDF เองได้ไหม** หรือ PaperLess มีแค่ข้อมูลในตาราง (`ic_trans` ฯลฯ) แล้วต้อง render PDF เอง?
- ถ้า SML สร้าง: ไฟล์อยู่ที่ไหน, format/template ตายตัวไหม?

**ทำไมต้องรู้:** ถ้า PaperLess ต้อง render PDF เองจากข้อมูล = งานใหญ่เพิ่มที่ยังไม่ได้นับ
**ถ้าไม่มี:** ไม่รู้ว่ามี PDF ตั้งต้นให้เซ็นยังไง

**ตอบ:**
ีSML สร้าง PDF เองได้ user จะ save จาก sml เป็น pdf และนำมา UPLOAD มายัง PaperLess

---

## 🟡 ส่วนที่ 2 — สำคัญ: กระทบ schema/feature (มี fallback)

### Q5. Document chain (POP → PUP → PBV → PVV)
จาก Excel เห็นว่าเอกสารโยงกันเป็นสาย เอกสารลูกอ้างถึงเอกสารแม่ด้วย **column ไหน** ใน SML
(เช่น `ref_doc_no`, `source_doc_no`, หรืออื่น ๆ)?

**ถ้าไม่มี:** feature "คลิกดูเอกสารที่เกี่ยวข้อง" ทำไม่ได้ (PaperLess เผื่อ field `source_doc_no` ไว้แล้ว แต่ไม่รู้ว่า map กับอะไร)

**ตอบ:**


ใน sml จะแยก ด้วย trans_flag  และ doc_format_code
จะใช้อยู่ 2 table ลองดูตัวอย่าง เอกสารที่ครบ flow ก่อนนะ
เริ่มจาก PO26060001 และเอกสาร ต่อไปจะ doc_ref PO26060001 

select *  from ic_trans  where doc_no ='PO26060001'
trans_flag : 6 = ใบสั่งซื้อ
select *  from ic_trans  where doc_no ='PA26060001'
trans_flag : 12 = ซื้อสินค้า
select *  from ap_ar_trans  where doc_no ='PB26060001'
trans_flag : 213 = ใบรับวางบิล
select *  from ap_ar_trans  where doc_no ='PV26060001'
trans_flag : 19 = จ่ายชำระหนี้
---

### Q6. เอกสารมี revision/version ฝั่ง SML ไหม
ถ้า SML แก้เอกสารเดิมแล้วส่งซ้ำ มี **เลข revision** บอกไหม?

**ทำไมต้องรู้:** ใช้แยก "ฉบับแก้จริง" vs "retry ซ้ำ"
**Fallback:** PaperLess ทำ `source_hash` ไว้แล้ว แต่ revision ฝั่ง SML จะแม่นกว่า

**ตอบ:**
sml ไม่มี version sml จะปริ้น เป็น pdf ข้อมูลใหม่ และ user ต้องนำมา upload ข้อ paperless ใหม่ ใน paperless ต้องมี version บอกด้วย

---

### Q7. มาตรฐานลายเซ็นที่ลูกค้าต้องการระดับไหน
- [ ] แค่ภาพลายเซ็น + ข้อความ พ.ร.บ. ธุรกรรมอิเล็กทรอนิกส์
- [ ] ภาพลายเซ็น + OTP ยืนยันตัวตน
- [ ] Digital certificate ระดับ CA

**ทำไมต้องรู้:** กระทบ external signer flow และความซับซ้อนของ evidence
**Fallback:** ทำระดับภาพลายเซ็น + evidence (IP/device/time/hash) ไปก่อน

**ตอบ:**
แค่ภาพลายเซ็น + ข้อความ พ.ร.บ. ธุรกรรมอิเล็กทรอนิกส์

---

### Q8. ตำแหน่งลายเซ็นบน PDF — ใครกำหนด
- [ ] แต่ละ doc_format_code มีพิกัดลายเซ็นตายตัว (SML กำหนด)
- [ ] ให้ admin วางพิกัดเองใน PaperLess
- [ ] ไม่จำเป็นต้องวางบนเอกสาร — ใช้หน้าสรุปลายเซ็นต่อท้ายได้

**Fallback:** PaperLess ใช้ default = แนบ "หน้าสรุปลายเซ็น" ต่อท้าย (ทำงานได้ทุก format); stamp ลงพิกัดเป๊ะเป็น enhancement ทีหลัง

**ตอบ:**
 แต่ละ doc_format_code มีพิกัดลายเซ็นตายตัว (SML กำหนด)

 ฉันมีไฟล์ ตัวอย่างจาก sml แล้ว เดียวลองดูก่อน

---

## 🟢 ส่วนที่ 3 — ดีถ้ารู้: วางแผน ops / scale / config

### Q9. การเก็บเอกสาร + สิทธิ์เปิดย้อนหลัง
- เอกสาร final ต้องเก็บกี่ปี?
- ใครมีสิทธิ์เปิดดูย้อนหลัง?

**ตอบ:**
ย้อหลัง 1 ปี และสามารถ config ใน ui ได้ ทุกคนดูย้อนหลังได้

---

### Q10. ช่องทางแจ้งเตือนผู้ที่ต้องเซ็น
- [ ] Email
- [ ] LINE
- [ ] SMS
- [ ] Mobile push
- [ ] แค่ dashboard ในระบบ

**ทำไมต้องรู้:** LINE/SMS ต้องเตรียม integration เพิ่ม

**ตอบ:**
ผ่าน Telegram ก่อน — bot: `t.me/paperless_notification_bot`

> ⚠️ Bot token เป็น secret — **ไม่เก็บในไฟล์นี้** (เคยมีอันเก่าหลุดในไฟล์นี้ จึงถูก revoke แล้ว)
> เก็บ token จริงใน `deploy/.env` (`TELEGRAM_BOT_TOKEN=...`, gitignored) และจดใน `deploy/CREDENTIALS.md`

---

### Q11. ปริมาณการใช้งาน
- ผู้ใช้เซ็นพร้อมกันสูงสุดกี่คน?
- เอกสารกี่ใบต่อเดือน / ต่อปี?

**ทำไมต้องรู้:** ยืนยัน capacity plan (ตั้งไว้ 10,000–50,000 docs, 20–100 concurrent)

**ตอบ:**
ตั้งไว้ 10,000–50,000 docs, 20–100 concurrent

---

### Q12. Connection ของ sml-api-bybos สำหรับ PaperLess
- PaperLess ใช้ **API key (X-Api-Key)** ตัวไหนเรียก `sml-api-bybos`?
- ลูกค้านี้ **tenant (X-Tenant)** ชื่ออะไร (เช่น `sml1_2026`)?
- `sml-api-bybos` instance ที่ใช้อยู่ host/port ไหน (default `192.168.2.109:8200`)?

> หมายเหตุ: ค่าเหล่านี้เป็น secret — ส่งผ่านช่องทางปลอดภัย ไม่กรอกลงไฟล์นี้

**ตอบ (ยกเว้น key/secret):**
ทดสอบ ใช้กับ ฐาน sml1_2026 ก่อน 

ที่ 
้host: 192.168.2.248:5432
user:postgres
pass:sml
database: sml1_2026

---

## สำหรับ PaperLess team — เก็บคำตอบแล้วทำอะไรต่อ

- Q1, Q2 → เปิดงานเพิ่ม endpoint `confirm` / `lock` ใน `sml-api-bybos` (ดู `docs/api-contract.md`)
- Q3, Q4 → กำหนด import path จริง (Phase 3); ระหว่างนี้ใช้ mock `SmlDocumentGateway`
- Q5 → ยืนยัน mapping `documents.source_doc_no`
- Q6 → ปรับ logic idempotency ถ้ามี revision จริง
- Q7, Q8 → ปรับ signature evidence / final PDF
- Q10 → เลือก notification adapter
- Q12 → ใส่ลง `.env` (secret, ไม่ commit)

สุดท้าย ทุกข้อให้ดูข้อมูลจริงก่อนนะ sml สร้างมาทดสอบแล้วที่

้host: 192.168.2.248:5432
user:postgres
pass:sml
database: sml1_2026

ตัวอย่าง pdf จาก SML ใบสั่งซื้อ
/Users/nontawatwongnuk/dev_bos/paperless/docs/ใบสั่งซื้อ PO26060001.pdf

---

## รอบที่ 2 — คำถาม follow-up (PaperLess ดู DB จริง `sml1_2026` แล้ว)

> ขอบคุณคำตอบรอบแรกครับ ทีม PaperLess เข้าไปดูข้อมูลจริงใน `sml1_2026` ตามที่แนะนำแล้ว
> เจอบางจุดที่ข้อมูลจริง **ไม่ตรง** กับคำตอบ หรือยังกำกวม จึงขอยืนยันก่อนเริ่มเขียน code
> (เรายึดหลัก: เขียน/อ่าน SML ผ่าน `sml-api-bybos` เท่านั้น ไม่ต่อ DB ตรง — DB ที่ให้มาใช้สำรวจ schema)

### F1. (จาก Q1/Q2) Confirm/Lock — ต้องเขียน field อะไรบ้าง นอกจาก `is_lock_record`?
ดูจริงแล้ว: `PO26060001` (is_lock_record=0) กับ `PO26060002` (is_lock_record=1) **ต่างกันแค่ `is_lock_record`**
ส่วน `approve_status`, `approve_code`, `approve_date`, `user_approve` ของ **ทั้งคู่ว่าง/0** เหมือนกัน

- ตอน "เซ็นครบใน PaperLess แล้ว confirm กลับ SML" — เขียนแค่ `is_lock_record = 1` พอ ใช่ไหม?
- หรือต้องเขียน `approve_status` / `approve_code` / `approve_date` / `user_approve` ด้วย?
  (รหัส user ที่เซ็นใน PaperLess ควรลงที่ `user_approve` ไหม?)
- ยังไม่เห็นตัวอย่างเอกสารที่ "approve จริง" ในฐานทดสอบ — ขอ 1 ใบที่ผ่าน approve เต็มขั้น เพื่อดูว่า field ไหนเปลี่ยนบ้าง

**ตอบ:**
is_lock_record = 1 ก็พอ filed เดียว user ก็ไม่ต้อง

### F2. (จาก Q2) Lock idempotent ไหม — เขียนซ้ำปลอดภัยไหม?
- ถ้า PaperLess ส่ง lock (`is_lock_record=1`) ซ้ำกับเอกสารที่ lock อยู่แล้ว (เช่น retry หลัง timeout) — เกิดอะไรขึ้น?
  (error / เขียนทับเฉย ๆ / มี trigger อะไรทำงานซ้ำไหม)
- มี field timestamp ที่ระบบ set อัตโนมัติตอน lock ไหม (เผื่อ audit)

**ทำไมต้องรู้:** retry logic ของ PaperLess ต้องรู้ว่าเขียนซ้ำปลอดภัย — **timeout เราไม่นับว่าสำเร็จ** จึงจะ retry

**ตอบ:**
แจ้ง เตือน ใน PaperLess ประมาณว่า เอกสารเดิมโดย lock อยู่แล้ว กดยืนยัน เพื่อ บันทึกซ้ำ เขียนทับเฉย ไปเลย เพียงแต่ user ต้องรู้ แจ้งเตือน 


### F3. (จาก Q5) Chain อยู่ที่ `ic_trans_detail.ref_doc_no` ไม่ใช่ `ic_trans.doc_ref` — ยืนยันหน่อย
คำตอบ Q5 บอกใช้ `doc_ref` แต่ดูจริง:
- `ic_trans.doc_ref` ของ `PA26060001` (และ PO/PB/PV ชุดตัวอย่าง) **ว่างเปล่า**
- เจอ linkage จริงที่ **`ic_trans_detail.ref_doc_no`** → `PA26060001` มี `ref_doc_no = PO26060001` (ตรง chain พอดี)
- `ic_trans.doc_ref` ที่มีค่า (เอกสารอื่น) เป็น token แปลก ๆ เช่น `260516JPUV8AKW` ไม่ใช่ doc_no

ขอยืนยัน: PaperLess ควร map "เอกสารที่เกี่ยวข้อง" ด้วย **`ic_trans_detail.ref_doc_no`** ใช่ไหม?
แล้วฝั่ง `ap_ar_trans` (PB/PV) chain ผูกที่ table/column ไหน? (มี detail table แยกไหม)

**ตอบ:**

ic_trans_detail.ref_doc_no ถูกต้องตาม ข้อมูลที่คุณดูเลย บางทีทีม sml อาจจะจำผิดซ้ำสน ให้ยึดจากข้อมูลใน database เป็นหลัก


### F4. (จาก Q5) `doc_format_code` ↔ `trans_flag` ไม่ใช่ 1:1 — จะ map ชนิดเอกสารยังไง?
ดูจริงในฐาน เจอว่าหนึ่ง `trans_flag` มีได้หลาย format และกลับกัน:
- `INV` และ `SI` → `trans_flag = 44` ทั้งคู่
- `SR` → มีทั้ง `trans_flag = 34` และ `36`
- มี row ที่ `doc_format_code` ว่าง แต่มี `trans_flag = 6 / 36`

PaperLess จับคู่ workflow ด้วย `doc_format_code` (POP/INV/PUP/PBV/PVV) — แต่ค่าจริงในฐานเป็น `PO/PA/PB/PV/INV/SO/...`
- ขอ **ตาราง mapping ชัด ๆ**: PaperLess doc type (POP/INV/PUP/PBV/PVV) → SML `doc_format_code` + `trans_flag` + table (`ic_trans` หรือ `ap_ar_trans`)
- กรณี `doc_format_code` ว่างแต่มี flag — เกิดจากอะไร ต้องสนใจไหม?

**ตอบ:**
ดูใน table select *  from erp_doc_format อาจช่วยคุณได้ใน sml-api-byboss รู้สึกจะมีแล้วนะ part นี้ 

> **[PaperLess ตรวจแล้ว — สรุป]**
> - `erp_doc_format` = catalog ชนิดเอกสารทั้งหมด (`code` = PO/PA/PB/PV/INV/SO… + ชื่อไทย) ใช้เป็น master ของ doc type ได้ **แต่ไม่มี `trans_flag` ในตารางนี้**
> - bridge `code → trans_flag` มีอยู่แล้วใน **sml-api-bybos**: `internal/handlers/doc_no.go` (map `so/si/po…` → transFlag + table) และ `internal/models/transaction.go` (constants: PurchaseOrder=6, PurchaseInvoice=12, SaleInvoice=44, SaleOrder=36 …)
> - **สรุป map:** PaperLess `doc_format_code` ใช้ `erp_doc_format.code` ตรง ๆ; เวลาต้องรู้ table (`ic_trans` vs `ap_ar_trans`) + trans_flag ให้ใช้ตาราง mapping ใน `doc_no.go` เป็นแหล่งจริง
> - กรณี `doc_format_code` ว่างแต่มี flag = legacy/draft rows — ข้ามได้ (PaperLess match ด้วย doc_no ที่ upload อยู่แล้ว)

### F5. (จาก Q12) sml-api-bybos endpoint สำหรับ confirm/lock มีหรือยัง?
ฝั่ง PaperLess จะเรียก `sml-api-bybos` (ไม่ต่อ DB ตรง) เพื่อสั่ง lock/confirm
- ตอนนี้ `sml-api-bybos` มี endpoint สำหรับ **set `is_lock_record`** อยู่แล้วไหม? ถ้ามี ขอ path + request/response shape
- ถ้ายังไม่มี — ใครเป็นคนเพิ่ม (ทีม sml-api-bybos) และรับ tenant `sml1_2026` ยังไง (`X-Tenant` / `X-Api-Key`)
- endpoint สำหรับ **อ่านเอกสาร + chain** (ตอน Phase 3 import อัตโนมัติ) มีหรือยัง? (Phase 1 ใช้ manual upload ตาม Q3 ไปก่อน)

**ตอบ:**
น่าจะยังไม่มี ไม่แน่ใจ คุณลองดูใน sml-api-bybos เองได้เลย ที่ 
/Users/nontawatwongnuk/dev_bos/sml-api-byboss

> **[PaperLess ตรวจแล้ว — สรุป]** (path จริง: `/Users/nontawatwongnuk/dev_bos/sml-api-bybos`)
> - **confirm/lock endpoint: ยังไม่มี** — grep `is_lock_record` / route `lock|confirm` = 0 ผล ต้องเพิ่มใหม่
> - convention พร้อมให้ต่อยอด: handler ใช้ `h.dbm.Get(ctx, middleware.TenantKey)` (multi-tenant pool), auth = `X-Api-Key` (`middleware.Auth`), tenant = `middleware.Tenant` (รับ `sml1_2026`)
> - `internal/handlers/transaction.go` เขียน `ic_trans`/`ic_trans_detail` (INSERT) ได้แล้ว — endpoint lock จะเป็น UPDATE `is_lock_record=1` WHERE doc_no ตาม pattern เดิม
> - **อ่านเอกสาร + chain มีแล้วบางส่วน:** `transaction.go` query `ic_trans` + `ic_trans_detail` (มี ref_doc_no) → Phase 3 import ต่อยอดจากนี้ได้
>
> **งานฝั่ง sml-api-bybos ที่ต้องเพิ่มสำหรับ Phase 3:**
> 1. `POST .../documents/:docNo/lock` → UPDATE `is_lock_record=1` (idempotent overwrite ตาม F2)
> 2. (มีแล้วบางส่วน) endpoint อ่าน doc + chain ผ่าน `ic_trans_detail.ref_doc_no`

---

## รอบที่ 3 — Gap ที่เหลือก่อนเขียน Phase 3 (PaperLess ตรวจ DB + sml-api-bybos code แล้ว)

> สองจุดนี้ **เป็นงานฝั่งทีม sml-api-bybos** (คนละ repo) PaperLess เดาเองไม่ได้ ขอคำยืนยัน/ข้อมูลก่อน

### G1. lock บน `ap_ar_trans` (PB/PV) — ใช้ `is_lock_record=1` เหมือน `ic_trans` ไหม?
ตรวจ DB จริง:
- `ap_ar_trans` **มี** column `is_lock_record` (integer) — โครงสร้างเหมือน `ic_trans`
- แต่ **ทั้งฐานทดสอบไม่มี ap_ar_trans ที่ lock=1 เลย** (PB26060001, PV26060001 = 0 ทั้งคู่) → ยังพิสูจน์ไม่ได้ว่า lock ตรงนี้ใช้ได้จริง
- `ic_trans.is_lock_record` มี **NULL 129 row** (column nullable) → UPDATE ต้อง handle `NULL→1` ด้วย ไม่ใช่แค่ `0→1`

**ขอ:** ยืนยันว่า lock PB/PV = `UPDATE ap_ar_trans SET is_lock_record=1 WHERE doc_no=...` ถูกต้อง + ขอ 1 ตัวอย่าง ap_ar_trans ที่ lock จริง (เหมือนที่ให้ PO26060002 มา)

**ตอบ:**


### G2. trans_flag ของ PA(12) / PB(213) / PV(19) ยังไม่มีใน sml-api-bybos
ดู `sml-api-bybos` code จริง — mapping table (`doc_no.go`) + constants (`models/transaction.go`) **ขาด 3 ชนิดที่ PaperLess ต้องใช้:**

| PaperLess ต้องใช้ | doc_no ตัวอย่าง | trans_flag | table | มีใน sml-api-bybos? |
|---|---|---|---|---|
| PO ใบสั่งซื้อ | PO26060001 | 6 | ic_trans | ✅ `po` |
| PA ซื้อ | PA26060001 | 12 | ic_trans | ❌ ขาด (มี const `PurchaseInvoice=12` แต่ไม่มีใน doc_no map) |
| PB ใบรับวางบิล | PB26060001 | 213 | ap_ar_trans | ❌ ขาดทั้ง const + map |
| PV จ่ายชำระ | PV26060001 | 19 | ap_ar_trans | ❌ ขาดทั้ง const + map |

**ขอ:** ทีม sml-api-bybos เพิ่ม trans_flag 12/213/19 + map `pa/pb/pv → table` ให้ครบ (และ chain ฝั่ง ap_ar ผูกที่ column ไหน — `ic_trans_detail.ref_doc_no` เป็นของ ic_trans; ap_ar มี detail table แยก `ap_ar_trans_detail`)

**ตอบ:**