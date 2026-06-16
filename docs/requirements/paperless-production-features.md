# PaperLess / E-Signature for SML - Production Feature Backlog

## สรุประบบ

ระบบนี้คือ PaperLess Document Workflow ที่รับเอกสารจาก SML เช่น ใบสั่งซื้อ ใบซื้อ ใบรับวางบิล และจ่ายชำระหนี้ เข้าสู่ระบบลงลายมือชื่ออิเล็กทรอนิกส์ตาม workflow ที่ config ไว้ แล้วบันทึกหลักฐาน ตรวจสอบย้อนหลัง พิมพ์เอกสารฉบับสมบูรณ์ และอัปเดตสถานะกลับ SML

เป้าหมายระดับ production คือไม่ใช่แค่ให้เซ็น PDF ได้ แต่ต้องกันการเซ็นผิดคน ผิดลำดับ เอกสารซ้ำ เอกสารหาย sync กลับ SML ผิด และต้อง audit ได้ว่าใครทำอะไร เมื่อไร จากอุปกรณ์ใด

## ข้อมูลที่ยืนยันแล้ว

- `เงื่อนไข = 1` หมายถึง คนใดคนหนึ่งในรายชื่อเซ็นแล้วขั้นนั้นสมบูรณ์
- `เงื่อนไข = 2` หมายถึง ทุกคนในรายชื่อต้องเซ็นครบ ขั้นนั้นจึงสมบูรณ์
- `เงื่อนไข = 3` หมายถึง บุคคลภายนอก เช่น ลูกค้า เป็นผู้ทำให้ขั้นนั้นสมบูรณ์
- `ลำดับ` ใช้ควบคุมการส่งงานต่อ ถ้าลำดับ 1 ยังไม่สมบูรณ์ จะยังไม่ส่งไปลำดับ 2
- `User01`, `User02`, `User03` คือผู้รับงาน/ผู้มีสิทธิ์ลงนามใน position นั้น และใช้ประกอบข้อความแจ้งเตือนกับรายงานสรุป
- ระบบต้องรองรับ mobile/tablet 100% เพราะผู้ใช้ส่วนใหญ่จะเซ็นด้วยนิ้วบนอุปกรณ์พกพา
- วิธีส่ง PDF จาก SML และ field/table สำหรับ Confirm/Lock ยังรอ confirm กับทีม SML

## โมดูลหลัก

| Priority | Module | Feature | Production Requirement | ป้องกันปัญหา |
|---|---|---|---|---|
| MVP | Document Config | ตั้งค่า doc format | Map `doc_format_code` จาก SML เช่น POP, INV เข้ากับ workflow template | เอกสารเข้า workflow ผิดแบบ |
| MVP | Document Config | ตั้งค่า position ผู้ลงนาม | กำหนด position เช่น ผู้จัดทำ ผู้ตรวจสอบ ผู้อนุมัติ ลูกค้า พร้อมลำดับ | เซ็นข้ามขั้นตอน |
| MVP | Document Config | ตั้งค่า approver หลายคน | รองรับ user หลายคนต่อ position และ rule ตาม `เงื่อนไข` 1, 2, 3 | รอคนผิด หรืออนุมัติไม่ครบ |
| MVP | Document Config | Versioned workflow config | config ต้องมี version/effective date เอกสารเก่าต้องใช้ config เดิม | เอกสารค้างหรือเปลี่ยนกติกากลางทาง |
| MVP | SML Integration | รับเอกสารจาก SML | รับ PDF และ metadata เช่น doc no, doc type, date, amount, source doc | ข้อมูลไม่ครบ ตรวจสอบย้อนหลังไม่ได้ |
| MVP | SML Integration | Idempotency | ใช้ key เช่น `doc_format_code + doc_no + revision` กัน import ซ้ำ | เอกสารซ้ำจากการ retry |
| MVP | SML Integration | Sync status กลับ SML | เมื่อ complete แล้ว update confirm/lock กลับ SML แบบมี log result | SML กับ PaperLess สถานะไม่ตรง |
| MVP | Workflow Engine | State machine | สถานะเอกสารชัดเจน: Draft, Imported, Pending, Signed, Rejected, Cancelled, Completed, SyncFailed | logic กระจัดกระจายและ bug ง่าย |
| MVP | Workflow Engine | Sequential/parallel signing | รองรับลำดับขั้นและขั้นที่มีผู้ลงนามหลายคน | รอผิดขั้นหรือเปิด task ผิดเวลา |
| MVP | Workflow Engine | Reject / return | ผู้ตรวจหรือผู้อนุมัติ reject พร้อมเหตุผล และส่งกลับขั้นที่กำหนด | ไม่มีทางแก้เอกสารผิด |
| MVP | Signing App | Inbox เอกสารรอเซ็น | แสดงเฉพาะเอกสารที่ user มีสิทธิ์และถึงคิวแล้ว | user เซ็นเอกสารที่ไม่ใช่ของตัวเอง |
| MVP | Signing App | PDF viewer | เปิดเอกสารพร้อม metadata สำคัญ เช่น เลขที่ วันที่ มูลค่า สถานะ | เซ็นโดยไม่เห็นข้อมูลครบ |
| MVP | Signing App | Signature capture | รองรับลายเซ็นด้วยนิ้วบน mobile/tablet เป็นหลัก และรองรับ mouse/upload เป็นทางเลือก | ลายเซ็นว่างหรือกดพลาด |
| MVP | Signing App | Mobile/tablet UX | หน้าลงนามต้องใช้ได้เต็มบนมือถือและแท็บเล็ต ทั้งแนวตั้ง/แนวนอน ปุ่มต้องกดง่าย และ PDF ต้อง zoom/pan ได้ | ใช้งานจริงหน้างานไม่ได้ |
| MVP | Signing App | Confirmation guard | ปุ่มยืนยัน disabled จนกว่าจะครบเงื่อนไข เช่น ดูเอกสารแล้ว เซ็นแล้ว | user error จากการกดเร็ว |
| MVP | Attachments | เอกสารแนบ | แนบไฟล์/ภาพประกอบ พร้อมชนิดไฟล์ ขนาด ผู้แนบ วันที่ | ไฟล์อ้างอิงหายหรือไม่รู้ที่มา |
| MVP | Audit | Audit trail | เก็บ user, role, action, old/new status, timestamp, IP, device, session id | ตรวจสอบย้อนหลังไม่ได้ |
| MVP | Output | Final PDF | พิมพ์/ดาวน์โหลดเอกสารพร้อมลายเซ็น ข้อความกฎหมาย เวลา complete และ verification code | เอกสารฉบับ final ไม่ชัดเจน |
| MVP | Security | Permission model | แยก admin config, signer, viewer, auditor, integration service | สิทธิ์เกินจำเป็น |
| MVP | Reliability | Retry queue | งาน import/export/sync SML ต้อง retry ได้และดู error ได้ | network fail แล้วเอกสารหาย |
| P1 | Workflow Engine | Delegation/substitute | ผู้ลงนามมอบหมายแทนหรือกำหนดตัวแทนช่วงลาได้ | งานค้างเพราะคนไม่อยู่ |
| P1 | Workflow Engine | Reminder/escalation | แจ้งเตือนและ escalate เมื่อเลย SLA | เอกสารค้างโดยไม่มีคนเห็น |
| P1 | External Signer | ลูกค้า/บุคคลภายนอก | `เงื่อนไข = 3` ต้องสร้าง external signing task แยกจาก user ภายใน แนะนำ secure link/OTP/expiry หรือ temp signer ต่อเอกสาร | ลูกค้าเซ็นยากหรือไม่ปลอดภัย |
| P1 | Document Chain | เอกสารที่เกี่ยวข้อง | แสดง chain จาก SML เช่น POP -> PUP -> PBV -> PVV และคลิกดูได้ | ตรวจเอกสารประกอบลำบาก |
| P1 | Reporting | Dashboard | จำนวนรอเซ็น ค้างเกิน SLA complete/reject/sync failed แยกตาม doc type | ผู้บริหารไม่เห็นคอขวด |
| P1 | Legal Evidence | Verification page | QR/code สำหรับเปิดตรวจสอบ hash, signer, timestamp, final status | พิสูจน์เอกสารยาก |
| P1 | Performance | Large PDF handling | render หน้าแรกเร็ว lazy load หน้าอื่น cache thumbnail | PDF ใหญ่แล้วระบบช้า |
| P1 | Operations | Admin reprocess | admin สั่ง re-sync หรือ re-generate final PDF ได้แบบมี audit | แก้เคส production ต้องแตะ DB |
| P2 | Advanced UX | Search/filter | ค้นหาด้วย doc no, supplier, amount, date, status, signer | หาเอกสารไม่เจอเมื่อข้อมูลเยอะ |
| P2 | API | Open API/internal API | ให้ระบบอื่น query สถานะเอกสารหรือดึง final PDF ได้ | integration ในอนาคตติดล็อก |
| P2 | Archive | Retention policy | กำหนดอายุจัดเก็บ แยก metadata/PDF/signature evidence | storage โตไม่ควบคุม |

## Workflow ที่แนะนำ

1. SML สร้างเอกสารและ export PDF พร้อม metadata
2. PaperLess import เอกสารด้วย idempotency key
3. ระบบเลือก workflow template ตาม `doc_format_code` และ version ที่ active
4. สร้าง signature tasks ตามลำดับและเงื่อนไข
5. user login เห็นเฉพาะ task ที่ต้องทำ
6. user เปิด PDF ตรวจเอกสาร แนบไฟล์ถ้ามี ลงลายมือชื่อ และ confirm
7. ระบบตรวจ rule ว่าขั้นนั้น complete หรือยัง
8. ถ้าครบทุกขั้น ระบบสร้าง final PDF พร้อมลายเซ็น ข้อความกฎหมาย และ verification code
9. ระบบ sync confirm/lock กลับ SML
10. audit/dashboard แสดงสถานะสำเร็จหรือ sync failed

## กติกา workflow

| ค่าใน config | Rule | ความหมาย |
|---|---|
| `เงื่อนไข = 1` | Any one | มีรายชื่อหลายคนใน `User01-User03` แต่คนใดคนหนึ่งเซ็นแล้ว position นั้นสมบูรณ์ |
| `เงื่อนไข = 2` | All users | ทุกคนที่ระบุใน `User01-User03` ต้องเซ็นครบ position นั้นจึงสมบูรณ์ |
| `เงื่อนไข = 3` | External signer | บุคคลภายนอก เช่น ลูกค้า ต้องเป็นผู้ทำให้ position นั้นสมบูรณ์ |
| `ลำดับ` | Sequential gate | ลำดับถัดไปจะยังไม่ถูกส่งงาน ถ้าลำดับก่อนหน้ายังไม่สมบูรณ์ |
| `User01-User03` | Assignees/recipients | ใช้เป็นผู้รับงาน ผู้มีสิทธิ์เซ็น และข้อมูลสำหรับแจ้งเตือน/รายงานสรุป |

หมายเหตุ production: ไม่ควรใช้ temp user กลางร่วมกันแบบถาวรสำหรับลูกค้าทุกคน เพราะ audit จะไม่ชัดว่าใครเป็นผู้เซ็นจริง ถ้าต้องใช้ temp user ควรผูกเป็น external signer record รายเอกสาร พร้อมชื่อผู้เซ็น เบอร์/อีเมล หลักฐาน OTP หรือหลักฐานยืนยันอื่น และวันหมดอายุ

## Validation กัน user error

- ห้ามเซ็นถ้าเอกสารถูก cancel, completed, locked หรือ sync กลับ SML แล้ว
- ห้ามเซ็นถ้า user ไม่อยู่ใน approver rule ของ task นั้น
- ห้ามกด confirm ถ้ายังไม่มีลายเซ็นหรือไม่ได้เปิดดูเอกสาร
- ห้าม import เอกสารซ้ำโดยไม่ระบุ revision
- ห้ามแก้ workflow config ที่มีเอกสารใช้งานอยู่ ให้สร้าง version ใหม่แทน
- ทุก reject ต้องมีเหตุผล
- ทุก reprocess/re-sync ต้องมีเหตุผลและ audit
- ถ้า SML sync fail ต้องแสดงสถานะชัดเจน ไม่ทำเหมือน complete สำเร็จ
- กรณี `เงื่อนไข = 1` เมื่อมีคนหนึ่งเซ็นแล้ว ต้องปิด task ของคนอื่นใน position เดียวกัน หรือแสดงว่าไม่ต้องดำเนินการแล้ว
- กรณี `เงื่อนไข = 2` ต้องแสดงจำนวนที่เซ็นแล้ว/จำนวนทั้งหมด เช่น 1/2, 2/2
- กรณี `เงื่อนไข = 3` ต้องบันทึกข้อมูลบุคคลภายนอกให้ครบก่อนส่งให้เซ็น
- บน mobile/tablet ต้องมี confirm ก่อนล้างลายเซ็นหรือ submit เพื่อกันนิ้วกดพลาด

## Performance Requirements

- หน้า inbox ต้อง paginate/filter ฝั่ง server ห้ามโหลดเอกสารทั้งหมดทีเดียว
- index DB ที่ `doc_no`, `doc_type`, `status`, `current_step`, `assigned_user_id`, `created_at`
- PDF ควรเก็บใน object storage หรือ file storage ไม่เก็บ binary ใหญ่ใน DB
- สร้าง thumbnail/preview แบบ background job
- งาน import, final PDF generation, notification, SML sync ควรเป็น queue
- API ต้องมี timeout, retry policy, และ idempotency
- Dashboard ใช้ aggregate table หรือ cache ไม่ scan transaction table ใหญ่ทุกครั้ง
- Log/audit table ต้อง partition หรือ archive ได้เมื่อข้อมูลโต
- PDF viewer บน mobile/tablet ต้องโหลดแบบ lazy load, มี thumbnail/page cache และไม่ render ทุกหน้าพร้อมกัน
- Signature canvas ต้องใช้ pointer/touch events ที่เสถียร และจำกัดขนาดภาพลายเซ็นก่อน upload เพื่อกันไฟล์ใหญ่เกินจำเป็น

## Security And Compliance

- Login ต้องผูก identity ชัดเจน ไม่ใช้ shared account
- Admin, signer, viewer, auditor, integration service ต้องแยกสิทธิ์
- เก็บ signature image แยกจาก evidence metadata และห้ามแก้หลัง submit
- Final PDF ควรมี hash หรือ verification code
- Audit log ต้อง append-only ในเชิงระบบ ห้ามแก้ไขย้อนหลังผ่าน UI
- ไม่ log token, password, full signature image หรือข้อมูลลับเกินจำเป็น
- External link ต้องมี expiry, one-time token และ optional OTP
- ลายเซ็นจาก mobile/tablet ต้องเก็บพร้อมหลักฐาน user/session/device/time และไม่อนุญาตให้แก้รูปหลัง submit

## Error States ที่ UI ต้องมี

- No pending documents
- Document already completed
- You are not allowed to sign this document
- Waiting for previous approver
- Signature required
- Attachment upload failed
- SML sync failed
- PDF preview failed but download available
- Workflow config missing for this document type
- Duplicate document detected
- Mobile signature area not supported by this browser
- External signer information missing

## Acceptance Criteria ระดับ MVP

- import POP/INV จาก SML ได้โดยไม่เกิด duplicate เมื่อ retry
- config ผู้ลงนามได้อย่างน้อย 3 ขั้น และรองรับ `เงื่อนไข` 1=คนใดคนหนึ่ง, 2=ทุกคน, 3=บุคคลภายนอก
- user เห็นเฉพาะเอกสารที่ตัวเองต้องเซ็น
- ลำดับ 2 จะไม่ถูกส่งงานจนกว่าลำดับ 1 จะสมบูรณ์
- กรณี `เงื่อนไข = 1` คนใดคนหนึ่งเซ็นแล้วขั้นนั้น complete ทันที
- กรณี `เงื่อนไข = 2` ต้องครบทุกคนก่อนขั้นนั้น complete
- กรณี `เงื่อนไข = 3` ต้องมี external signer evidence ก่อนขั้นนั้น complete
- ลงลายมือชื่อแล้วระบบบันทึก timestamp, user, device/session
- ลงลายมือชื่อด้วยนิ้วบน mobile/tablet ได้สมบูรณ์ ทั้ง iOS/Android อย่างน้อย browser หลัก
- reject พร้อมเหตุผลได้
- complete แล้วสร้าง final PDF พร้อมลายเซ็นและข้อความกฎหมาย
- complete แล้ว sync confirm/lock กลับ SML ได้ หรือขึ้น sync failed ให้แก้ไขได้
- admin ดู audit trail ของเอกสารหนึ่งใบได้ครบ
- inbox 10,000 เอกสารยังเปิดและค้นหาได้เร็วด้วย pagination/filter

## สิ่งที่ต้องถาม/confirm เพิ่ม

- SML จะส่ง PDF/metadata ผ่านช่องทางใด: API, watched folder, scheduled job หรือ manual upload
- Field/table ใน SML สำหรับ update confirm/lock คืออะไร
- ต้องมีมาตรฐานลายเซ็นระดับไหน: แค่ภาพลายเซ็น, OTP evidence, หรือ digital certificate
- SLA/notification ต้องใช้ช่องทางอะไร: email, line, mobile push, dashboard
- เอกสาร final ต้องเก็บกี่ปี และใครมีสิทธิ์เปิดย้อนหลัง
