# PaperLess — Google Stitch Prompts (ทุกหน้า, copy-paste ได้เลย)

วิธีใช้: วาง **บล็อก 0 (Master + Style)** ใน Stitch ก่อนหนึ่งครั้ง แล้วค่อยสั่งทีละหน้า
(แต่ละบล็อกมี style anchor ในตัว — จะ copy ไปใช้เดี่ยวๆ ก็ได้). คำสั่งดีไซน์เป็นอังกฤษ
(Stitch อ่านแม่นกว่า) แต่ **ข้อความ UI ทั้งหมดเป็นภาษาไทย** ตามที่ระบุ.

ทิศทาง: **"Trust & Calm"** — มืออาชีพ น่าเชื่อถือระดับเอกสารกฎหมาย, สงบ ไม่รก, mobile-first,
ปุ่มแตะใหญ่, มี empty/loading/error ชัดเจน.

---

## 0) MASTER + STYLE (วางก่อนหนึ่งครั้ง)

```
Create a design system for "PaperLess" — a Thai-language e-signature document-workflow
web app (PWA) used inside a company between staff and an ERP. It receives business
documents (purchase orders, invoices) as PDFs, routes them through a configurable
multi-step signing workflow, lets people sign ON PHONES/TABLETS WITH A FINGER, then
produces a legally-stamped final PDF. Users: internal signers (mostly mobile, finger
signing), workflow/document admins (desktop), auditors, external signers (customers via
secure link).

Design direction "Trust & Calm": professional, trustworthy, official enough for legal/
audit use; calm and uncluttered so the document is the hero; mobile-first and responsive;
large finger-friendly touch targets; clear empty/loading/error states everywhere.

STYLE TOKENS:
- Primary navy #2E498A (darker #26396B). Page background off-white #F6F8FB. Surfaces white.
- Text: ink #0F172A, muted #475569, subtle #94A3B8. Borders #E2E8F0.
- Semantic: success #15803D, warning #B45309, danger #B91C1C, info = navy.
  Soft badge tints: success #DCFCE7, warning #FEF3C7, danger #FEE2E2, info #EEF3FB.
- Typeface: Sarabun (Thai). Headings bold; body 15-16px; line-height ~1.6.
- Shape: rounded corners (cards 16px, controls 10-12px), soft low shadows, thin borders.
  No heavy gradients, no visual noise.
- Components: pill status badges WITH a leading status dot; cards and tappable card-rows;
  primary navy buttons (min 44px tall) + outline/ghost secondary; bottom-sheet modals on
  mobile; sticky top header per page.
- ALL UI COPY IN THAI.
Feel: like a trustworthy government/banking e-document tool, not a flashy consumer app.
```

---

## 1) Login

```
Design a Login screen for PaperLess. Style: Thai e-signature app, Trust & Calm — navy
#2E498A on off-white #F6F8FB, white card rounded-16 soft-shadow, Sarabun font, ink
#0F172A / muted #475569, min 44px buttons, mobile-first, all copy in Thai.

Centered card on an off-white page. Brand wordmark "PaperLess" (navy, bold) with subtitle
"ระบบเซ็นเอกสารอิเล็กทรอนิกส์". Inside the card: label "ชื่อผู้ใช้" + text field; label
"รหัสผ่าน" + password field; a full-width navy primary button "เข้าสู่ระบบ". Show an inline
error variant: a soft red box "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง".
```

## 2) Signer Inbox (กล่องเอกสารรอเซ็น)

```
Design a Signer Inbox for PaperLess. Style: Thai e-signature app, Trust & Calm — navy
#2E498A, off-white #F6F8FB bg, white cards rounded-16, Sarabun, pill badges with dot,
mobile-first, copy in Thai.

Sticky top header: title "กล่องเอกสารรอเซ็น" + small subtitle "3 รายการ". Below: a vertical
list of tappable document cards. Each card: a small grey doc-format chip ("PO"), the doc
number "PO26060001" (bold), a muted line "ขั้นที่ 2 · คนใดคนหนึ่งเซ็น", the amount
"฿125,000.00" in navy bold, a small date "21 มิ.ย. 2569", and on the right a warning pill
"รอเซ็น" with an amber dot. Include an empty state: a soft circular icon + "ไม่มีเอกสารที่
รอการเซ็น". Pagination "ก่อนหน้า / 1/2 / ถัดไป" at the bottom.
```

## 3) Document + Signature — SIGNER (หน้าเซ็น, hero, มือถือ)

```
Design the mobile Document Signing screen for PaperLess. Style: Trust & Calm — navy
#2E498A, off-white bg, white cards rounded-16, Sarabun, mobile-first, big thumb-reachable
buttons (min 44px), copy in Thai.

Sticky header: back "← กลับ" + title "PO — PO26060001" + subtitle "ขั้นที่ 2 จาก 2".
Then a WORKFLOW PROGRESS list: step rows — step 1 done (green circle check, "ขั้นที่ 1
(คนใดคนหนึ่ง)"), step 2 current (navy, "ขั้นที่ 2 (ทุกคน)", "1/2 เซ็นแล้ว", right tag
"กำลังดำเนินการ"). Then a card "เอกสาร" containing a PDF preview area. Then an "เอกสารแนบ"
list card. Then a SIGNATURE card: label "ลายเซ็น", a dashed-border box placeholder
"วาดลายเซ็นที่นี่" (signature canvas), two buttons "ล้างลายเซ็น" (outline) and "ยืนยันลายเซ็น"
(navy), and a red text link "ส่งคืนเอกสาร".
Also show the CONFIRM state: after drawing, a card "ตรวจสอบและยืนยัน" with text "ลายเซ็นของคุณ
ถูกบันทึกแล้ว กดยืนยันเพื่อส่งข้อมูล" and buttons "วาดใหม่" (outline) + "ยืนยันเซ็นเอกสาร" (navy).
```

## 4) Document — Reject flow (ส่งคืนเอกสาร)

```
Design a "Return/Reject document" screen for PaperLess. Style: Trust & Calm, navy #2E498A,
white cards, Sarabun, mobile-first, copy in Thai.

Sticky header back "← กลับ" + title "ส่งคืนเอกสาร". Body: helper text "กรุณาระบุเหตุผลในการ
ส่งคืนเอกสาร", a multiline textarea (placeholder "เหตุผล..."), a small required hint
"กรุณาระบุเหตุผล", and a full-width DANGER (red) button "ยืนยันการส่งคืน" (disabled until a
reason is typed).
```

## 5) Document — Submitting & Success states

```
Design two simple full-screen states for PaperLess (Trust & Calm, navy, off-white,
Sarabun, copy in Thai):
(a) SUBMITTING: centered spinner + text "กำลังส่งลายเซ็น..." (and a network-retry variant
    "กำลังตรวจสอบสถานะ... กรุณารอสักครู่").
(b) SUCCESS: centered green circular check, big text "เซ็นเอกสารสำเร็จ", and a navy button
    "กลับไปกล่องเอกสาร".
```

## 6) External Signer page (ลูกค้าภายนอก, public link)

```
Design the External Signer flow for PaperLess — a public secure-link page for an outside
customer. Style: Trust & Calm, navy #2E498A, off-white bg, white cards, Sarabun, mobile-
first, reassuring + official, copy in Thai.

Header: small brand "PaperLess" (navy, uppercase tracking) + doc title "PO PO26060001" +
greeting "สวัสดี คุณ[ชื่อลูกค้า]". Show these states as separate frames:
(1) VIEW: card "เอกสารที่ต้องเซ็น" with PDF preview + full-width navy button "ดำเนินการเซ็น".
(2) SIGN: signature canvas card "วาดลายเซ็น" + helper "ลายเซ็นของคุณจะถูกบันทึกอย่างปลอดภัย"
    + outline button "ย้อนกลับ".
(3) CONSENT/PREVIEW: a card showing the signature image, then an amber consent box with
    legal text "เอกสารนี้จะได้รับการลงลายมือชื่ออิเล็กทรอนิกส์ตามพระราชบัญญัติว่าด้วยธุรกรรมทาง
    อิเล็กทรอนิกส์ พ.ศ. 2544..." + a checkbox "ฉันยอมรับเงื่อนไขและให้ความยินยอมในการลงลายมือ
    ชื่ออิเล็กทรอนิกส์"; buttons "แก้ไขลายเซ็น" (outline) + "ยืนยันการเซ็น" (navy, disabled
    until checked).
(4) SUCCESS: green check + "เซ็นเอกสารสำเร็จ" + "ลายเซ็นของคุณได้รับการบันทึกแล้ว ขอบคุณที่
    ดำเนินการ".
(5) ERROR states: "ลิงก์เซ็นเอกสารนี้หมดอายุแล้ว..." and "เอกสารนี้ได้รับการเซ็นแล้ว...".
```

## 7) Admin top-nav shell (โครง admin)

```
Design a responsive top navigation bar for the PaperLess admin area. Style: Trust & Calm,
white bar with thin bottom border, navy #2E498A accents, Sarabun, copy in Thai.

Left: brand "PaperLess" (navy bold). Nav tabs: "แดชบอร์ด", "เอกสาร", "ตั้งค่า Workflow",
"ผู้ใช้" (the "ผู้ใช้" tab shown only for system admin). Active tab has a soft navy-tint
pill. Right: user display name (muted) + a small outline "ออกจากระบบ" button. On mobile the
tabs scroll horizontally. Keep it compact (about 48px tall).
```

## 8) Admin Dashboard (แดชบอร์ด)

```
Design an Admin Dashboard for PaperLess. Style: Trust & Calm, navy #2E498A, off-white bg,
white cards rounded-16, soft semantic tints, Sarabun, copy in Thai.

Sticky header "แดชบอร์ด" + subtitle "เอกสารทั้งหมด N ฉบับ". Then a row of 4 stat tiles
(2x2 on mobile, 4-across on desktop), each a soft-tinted rounded card with a big number and
a label: "รอเซ็น" (amber tint), "เสร็จสิ้น" (green tint), "ส่งคืน" (red tint), "ซิงก์ล้มเหลว"
(red tint). Below: a card "แยกตามสถานะเอกสาร" (list of status → count rows) and a card
"แยกตามรูปแบบเอกสาร" (doc-format → count rows).
```

## 9) Admin — Documents list + Import dialog (เอกสารทั้งหมด)

```
Design an Admin Documents list for PaperLess. Style: Trust & Calm, navy #2E498A, off-white
bg, white cards, pill status badges with dot, Sarabun, copy in Thai.

Sticky header: title "เอกสารทั้งหมด" + count, and an outline button "นำเข้าเอกสาร". A filter
card: search field "ค้นหาเลขเอกสาร...", a small field "รูปแบบ (POP...)", and a status dropdown
"— สถานะทั้งหมด —". Then a list of tappable document cards: "POP — PO26060001", muted
"เวอร์ชัน 1", amount in navy, date, and a right-side status pill (e.g. "รอเซ็น" amber,
"เสร็จสิ้น" green, "ส่งคืน" red, "นำเข้าแล้ว" navy-tint). 
Also design the IMPORT DIALOG as a centered modal / mobile bottom-sheet titled "นำเข้าเอกสาร":
a PDF file picker "ไฟล์ PDF *", fields "เลขเอกสาร *", "รูปแบบเอกสาร *", "วันที่เอกสาร (ไม่บังคับ)",
"จำนวนเงิน (ไม่บังคับ)", and buttons "ยกเลิก" (outline) + "อัปโหลด" (navy).
```

## 10) Admin — Document detail (รายละเอียดเอกสาร + audit)

```
Design an Admin Document Detail screen for PaperLess. Style: Trust & Calm, navy #2E498A,
white cards rounded-16, pill badges, Sarabun, copy in Thai.

Sticky header: back "← กลับ" + "POP — PO26060001" + subtitle showing status + amount.
Cards stacked:
1) "รายละเอียดเอกสาร": a 2-column key/value grid (รูปแบบ, เลขเอกสาร, วันที่, จำนวนเงิน,
   เวอร์ชัน Workflow, สถานะเอกสาร = pill, สถานะ SML = pill) + two download buttons
   "ดาวน์โหลด PDF ต้นฉบับ" (outline) and "ดาวน์โหลด PDF ฉบับจริง" (navy).
2) "ขั้นตอน Workflow": the workflow progress list.
3) "เอกสารแนบ": list of attachment rows (file icon, name, size, date) + a "+ แนบไฟล์" control;
   empty state "ยังไม่มีเอกสารแนบ".
4) "ผู้เซ็นภายนอก": list of external signers with status pills, or "ไม่มีผู้เซ็นภายนอก".
5) "ประวัติเอกสาร": an AUDIT TIMELINE — rows with a round icon, actor + action (e.g.
   "ผู้จัดทำ — sign", "document_imported", "step_complete"), timestamp and IP. Evidence-focused,
   append-only feel.
```

## 11) Invite External Signer modal + one-time token (เชิญผู้เซ็นภายนอก)

```
Design an "Invite external signer" modal for PaperLess (mobile bottom-sheet / desktop
centered). Style: Trust & Calm, navy #2E498A, Sarabun, copy in Thai.

FORM state, title "เชิญผู้เซ็นภายนอก": fields "ชื่อ *", "อีเมล (ไม่บังคับ)", "เบอร์โทร (ไม่บังคับ)",
"หมดอายุใน (ชั่วโมง)" with hint "ค่าเริ่มต้น 72 ชั่วโมง (3 วัน) — สูงสุด 168 ชั่วโมง (7 วัน)";
primary navy button "สร้างลิงก์เชิญ".
SUCCESS state: green check + "สร้างลิงก์สำเร็จ"; a prominent red warning box "⚠ คัดลอกเดี๋ยวนี้ —
จะไม่แสดงอีก"; a read-only link box (monospace) with a "คัดลอกลิงก์" button; a token box with
"คัดลอกโทเคน"; expiry line; a "ปิด" button. Emphasize the one-time nature.
```

## 12) Resend link modal (ส่งลิงก์ใหม่)

```
Design a "Resend signing link" modal for PaperLess (bottom-sheet/centered). Style: Trust &
Calm, navy #2E498A, Sarabun, copy in Thai.

FORM: title "ส่งลิงก์ใหม่", text "สำหรับ [ชื่อผู้เซ็น]", field "หมดอายุใน (ชั่วโมง)" with hint
"สูงสุด 168 ชั่วโมง (7 วัน)", primary button "สร้างลิงก์ใหม่".
SUCCESS: same one-time link/token reveal as the invite modal (red "คัดลอกเดี๋ยวนี้ — จะไม่
แสดงอีก", copy-link/copy-token, expiry, "ปิด").
```

## 13) Workflow Templates list + Create dialog (ตั้งค่า Workflow)

```
Design a Workflow Templates list for PaperLess. Style: Trust & Calm, navy #2E498A, white
cards, pill badges, Sarabun, copy in Thai.

Sticky header "Workflow Templates" + a navy "+ สร้าง Workflow" button (system/workflow admin
only). A filter field "กรองรูปแบบเอกสาร (POP, DEMO3...)". A list of expandable template cards:
each row shows doc-format code, "v2", a status pill ("ใช้งาน" green / "ร่าง" amber / "ปิดใช้"
grey), and the template name. Expanded card shows action buttons "แก้ไขขั้นตอน →" / "โคลน" /
"เผยแพร่" / "ปิดใช้งาน" and a read-only step preview.
Also design the CREATE DIALOG (modal): title "สร้าง Workflow ใหม่", fields "รหัสรูปแบบเอกสาร
(doc format)" + "ชื่อ Workflow", helper "จะสร้างเป็นฉบับร่าง...", buttons "ยกเลิก" / "สร้างฉบับร่าง".
```

## 14) Workflow Config Editor + draw signature boxes (หน้าแก้ไข — สำคัญ/ซับซ้อน)

```
Design the Workflow Config Editor for PaperLess — the key admin screen. Style: Trust &
Calm, navy #2E498A, white cards rounded-16, Sarabun, copy in Thai.

Sticky header: back "← กลับ" + "DEMO3 · v2" + a status pill "ร่าง".
Cards:
1) "ชื่อ Workflow" field + "บันทึกชื่อ" button.
2) "ขั้นตอนการเซ็น (N)" with a "+ เพิ่มขั้นตอน" button. Each STEP card: a navy number badge,
   reorder ↑ ↓ and a red "ลบ"; fields "รหัสตำแหน่ง" + "ชื่อตำแหน่ง"; a dropdown "เงื่อนไข"
   (คนใดคนหนึ่งเซ็น / ทุกคนต้องเซ็น / ผู้เซ็นภายนอก); and a "ผู้เซ็น (N)" checkbox list of users
   (hidden when condition = external, replaced by note "ผู้เซ็นภายนอกถูกเชิญตอนนำเข้าเอกสาร —
   ไม่ต้องกำหนดที่นี่").
3) "ตำแหน่งลายเซ็นบนเอกสาร (ไม่บังคับ)": an "เลือกไฟล์ PDF ตัวอย่าง" upload; once uploaded, the
   PDF page renders on a canvas and the admin DRAWS labeled rectangles to mark where each
   position's signature goes (color-coded chips per position, "วางกรอบแล้ว 2/3 ตำแหน่ง", a
   "ลบกรอบ" per position). Multi-page navigation.
Sticky bottom actions: "บันทึกขั้นตอน" (navy) + "เผยแพร่" (outline). Also show a READ-ONLY
variant for active/inactive versions with a notice "เวอร์ชันนี้ใช้งานอยู่ แก้ไม่ได้..." and a
"โคลนเพื่อแก้ไข" button.
```

## 15) User Management list + Create/Edit dialog (จัดการผู้ใช้, system admin)

```
Design a User Management screen for PaperLess (system admin only). Style: Trust & Calm,
navy #2E498A, white cards, role badges, Sarabun, copy in Thai.

Sticky header "จัดการผู้ใช้" + count + a navy "+ เพิ่มผู้ใช้" button. A toggle "แสดงผู้ใช้ที่
ปิดใช้งานด้วย". A list of user cards: display name (bold), "@username" (muted), email/phone,
role badges (e.g. "System Admin", "Signer"), a grey "ปิดใช้งาน" badge if inactive, and an
outline "แก้ไข" button.
Also design the CREATE/EDIT DIALOG (bottom-sheet/centered): title "เพิ่มผู้ใช้" / "แก้ไขผู้ใช้";
fields "ชื่อผู้ใช้ (username) *" (create only), "ชื่อที่แสดง *", "อีเมล", "เบอร์โทร", a status
dropdown (edit only: ใช้งาน/ปิดใช้งาน), a "บทบาท (roles)" checkbox list, and "รหัสผ่าน" field
with hint "ถ้าไม่ตั้งรหัสผ่าน ผู้ใช้จะยังเข้าสู่ระบบไม่ได้จนกว่าจะตั้ง"; buttons "ยกเลิก" /
"สร้างผู้ใช้" (or "บันทึก").
```

## 16) Shared states (empty / loading / error) — components sheet

```
Design a small components sheet for PaperLess covering shared states. Style: Trust & Calm,
navy #2E498A, off-white bg, Sarabun, copy in Thai.

- LOADING: a centered navy spinner.
- EMPTY (positive): a soft green circular "✓" + message (e.g. "ไม่มีเอกสารที่รอการเซ็น",
  "เอกสารนี้เซ็นครบแล้ว").
- ERROR (warning): a soft amber circular "!" + message + an outline "ลองใหม่" button. Example
  messages: "ไม่มีการเชื่อมต่ออินเทอร์เน็ต", "คุณไม่มีสิทธิ์เซ็นเอกสารนี้", "กำลังรอผู้อนุมัติ
  ขั้นก่อนหน้า", "ไม่พบการตั้งค่า Workflow สำหรับเอกสารประเภทนี้".
- STATUS BADGES set: pill badges with a leading dot — "รอเซ็น" (amber), "เสร็จสิ้น"/"เซ็นแล้ว"
  (green), "ส่งคืน"/"ซิงก์ล้มเหลว" (red), "นำเข้าแล้ว"/"รอซิงก์" (navy-tint), "ยกเลิก"/"ข้าม"
  (grey).
```

---

## ลำดับแนะนำใน Stitch
0 (Master+Style) → 2 (Inbox) → 3 (หน้าเซ็น, hero) → 6 (External) → 8 (Dashboard) →
9–10 (Documents) → 14 (Workflow editor) → 15 (Users) → ที่เหลือเป็น state/modal เสริม.
หน้า hero ที่ควรให้เวลามากสุด: **3 (หน้าเซ็นมือถือ)** และ **14 (Workflow config + ตีกรอบลายเซ็น)**.
