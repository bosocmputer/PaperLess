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
_(กรอกที่นี่)_

---

### Q2. การ "Lock" เอกสาร (กันแก้หลังเซ็น)
- เก็บใน **table ไหน / column ไหน**? เป็นคนละ field กับ Confirm หรือ field เดียวกัน?
- **เขียนซ้ำได้ไหม (idempotent)?** ถ้าส่ง lock ซ้ำหลัง timeout จะเกิดอะไร?

**ทำไมต้องรู้:** retry logic ต้องรู้ว่าเขียนซ้ำปลอดภัยไหม
**ถ้าไม่มี:** retry แล้วอาจทำข้อมูล SML เสีย

**ตอบ:**
_(กรอกที่นี่)_

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
_(กรอกที่นี่)_

---

### Q4. ตัว PDF เอกสารมาจากไหน
- SML **สร้าง PDF เองได้ไหม** หรือ PaperLess มีแค่ข้อมูลในตาราง (`ic_trans` ฯลฯ) แล้วต้อง render PDF เอง?
- ถ้า SML สร้าง: ไฟล์อยู่ที่ไหน, format/template ตายตัวไหม?

**ทำไมต้องรู้:** ถ้า PaperLess ต้อง render PDF เองจากข้อมูล = งานใหญ่เพิ่มที่ยังไม่ได้นับ
**ถ้าไม่มี:** ไม่รู้ว่ามี PDF ตั้งต้นให้เซ็นยังไง

**ตอบ:**
_(กรอกที่นี่)_

---

## 🟡 ส่วนที่ 2 — สำคัญ: กระทบ schema/feature (มี fallback)

### Q5. Document chain (POP → PUP → PBV → PVV)
จาก Excel เห็นว่าเอกสารโยงกันเป็นสาย เอกสารลูกอ้างถึงเอกสารแม่ด้วย **column ไหน** ใน SML
(เช่น `ref_doc_no`, `source_doc_no`, หรืออื่น ๆ)?

**ถ้าไม่มี:** feature "คลิกดูเอกสารที่เกี่ยวข้อง" ทำไม่ได้ (PaperLess เผื่อ field `source_doc_no` ไว้แล้ว แต่ไม่รู้ว่า map กับอะไร)

**ตอบ:**
_(กรอกที่นี่)_

---

### Q6. เอกสารมี revision/version ฝั่ง SML ไหม
ถ้า SML แก้เอกสารเดิมแล้วส่งซ้ำ มี **เลข revision** บอกไหม?

**ทำไมต้องรู้:** ใช้แยก "ฉบับแก้จริง" vs "retry ซ้ำ"
**Fallback:** PaperLess ทำ `source_hash` ไว้แล้ว แต่ revision ฝั่ง SML จะแม่นกว่า

**ตอบ:**
_(กรอกที่นี่)_

---

### Q7. มาตรฐานลายเซ็นที่ลูกค้าต้องการระดับไหน
- [ ] แค่ภาพลายเซ็น + ข้อความ พ.ร.บ. ธุรกรรมอิเล็กทรอนิกส์
- [ ] ภาพลายเซ็น + OTP ยืนยันตัวตน
- [ ] Digital certificate ระดับ CA

**ทำไมต้องรู้:** กระทบ external signer flow และความซับซ้อนของ evidence
**Fallback:** ทำระดับภาพลายเซ็น + evidence (IP/device/time/hash) ไปก่อน

**ตอบ:**
_(กรอกที่นี่)_

---

### Q8. ตำแหน่งลายเซ็นบน PDF — ใครกำหนด
- [ ] แต่ละ doc_format_code มีพิกัดลายเซ็นตายตัว (SML กำหนด)
- [ ] ให้ admin วางพิกัดเองใน PaperLess
- [ ] ไม่จำเป็นต้องวางบนเอกสาร — ใช้หน้าสรุปลายเซ็นต่อท้ายได้

**Fallback:** PaperLess ใช้ default = แนบ "หน้าสรุปลายเซ็น" ต่อท้าย (ทำงานได้ทุก format); stamp ลงพิกัดเป๊ะเป็น enhancement ทีหลัง

**ตอบ:**
_(กรอกที่นี่)_

---

## 🟢 ส่วนที่ 3 — ดีถ้ารู้: วางแผน ops / scale / config

### Q9. การเก็บเอกสาร + สิทธิ์เปิดย้อนหลัง
- เอกสาร final ต้องเก็บกี่ปี?
- ใครมีสิทธิ์เปิดดูย้อนหลัง?

**ตอบ:**
_(กรอกที่นี่)_

---

### Q10. ช่องทางแจ้งเตือนผู้ที่ต้องเซ็น
- [ ] Email
- [ ] LINE
- [ ] SMS
- [ ] Mobile push
- [ ] แค่ dashboard ในระบบ

**ทำไมต้องรู้:** LINE/SMS ต้องเตรียม integration เพิ่ม

**ตอบ:**
_(กรอกที่นี่)_

---

### Q11. ปริมาณการใช้งาน
- ผู้ใช้เซ็นพร้อมกันสูงสุดกี่คน?
- เอกสารกี่ใบต่อเดือน / ต่อปี?

**ทำไมต้องรู้:** ยืนยัน capacity plan (ตั้งไว้ 10,000–50,000 docs, 20–100 concurrent)

**ตอบ:**
_(กรอกที่นี่)_

---

### Q12. Connection ของ sml-api-bybos สำหรับ PaperLess
- PaperLess ใช้ **API key (X-Api-Key)** ตัวไหนเรียก `sml-api-bybos`?
- ลูกค้านี้ **tenant (X-Tenant)** ชื่ออะไร (เช่น `sml1_2026`)?
- `sml-api-bybos` instance ที่ใช้อยู่ host/port ไหน (default `192.168.2.109:8200`)?

> หมายเหตุ: ค่าเหล่านี้เป็น secret — ส่งผ่านช่องทางปลอดภัย ไม่กรอกลงไฟล์นี้

**ตอบ (ยกเว้น key/secret):**
_(กรอกที่นี่)_

---

## สำหรับ PaperLess team — เก็บคำตอบแล้วทำอะไรต่อ

- Q1, Q2 → เปิดงานเพิ่ม endpoint `confirm` / `lock` ใน `sml-api-bybos` (ดู `docs/api-contract.md`)
- Q3, Q4 → กำหนด import path จริง (Phase 3); ระหว่างนี้ใช้ mock `SmlDocumentGateway`
- Q5 → ยืนยัน mapping `documents.source_doc_no`
- Q6 → ปรับ logic idempotency ถ้ามี revision จริง
- Q7, Q8 → ปรับ signature evidence / final PDF
- Q10 → เลือก notification adapter
- Q12 → ใส่ลง `.env` (secret, ไม่ commit)
