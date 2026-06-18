"use client";

interface Props {
  code: string;
  message?: string;
  onRetry?: () => void;
}

const MESSAGES: Record<string, string> = {
  no_pending_documents:        "ไม่มีเอกสารที่รอการเซ็น",
  document_already_completed:  "เอกสารนี้เซ็นครบแล้ว",
  not_allowed_to_sign:         "คุณไม่มีสิทธิ์เซ็นเอกสารนี้",
  waiting_for_previous:        "กำลังรอผู้อนุมัติขั้นก่อนหน้า",
  signature_required:          "กรุณาวาดลายเซ็นก่อนส่ง",
  attachment_upload_failed:    "อัปโหลดไฟล์แนบไม่สำเร็จ กรุณาลองใหม่",
  sml_sync_failed:             "การส่งข้อมูลไปยังระบบ SML ล้มเหลว เอกสารยังใช้งานได้ตามปกติ",
  pdf_preview_failed:          "ไม่สามารถแสดงเอกสารในหน้าเว็บ",
  pdf_generation_pending:      "เอกสารเสร็จแล้ว กำลังสร้างไฟล์ PDF — ลองใหม่อีกครู่",
  workflow_config_missing:     "ไม่พบการตั้งค่า Workflow สำหรับเอกสารประเภทนี้",
  duplicate_document:          "เอกสารนี้ถูกนำเข้าระบบแล้ว",
  external_signer_info_missing:"ข้อมูลผู้เซ็นภายนอกไม่ครบถ้วน",
  unauthorized:                "กรุณาเข้าสู่ระบบใหม่",
  network_error:               "ไม่มีการเชื่อมต่ออินเทอร์เน็ต",
  // External signer states
  external_link_expired:       "ลิงก์เซ็นเอกสารนี้หมดอายุแล้ว กรุณาติดต่อผู้ส่งเพื่อขอลิงก์ใหม่",
  external_link_used:          "เอกสารนี้ได้รับการเซ็นแล้ว ขอบคุณที่ดำเนินการ",
  external_link_invalid:       "ลิงก์ไม่ถูกต้องหรือไม่มีอยู่ในระบบ",
  rate_limited:                "มีการร้องขอมากเกินไป กรุณาลองใหม่ในอีกสักครู่",
};

export default function ErrorState({ code, message, onRetry }: Props) {
  const displayMsg = message ?? MESSAGES[code] ?? `เกิดข้อผิดพลาด (${code})`;

  return (
    <div className="flex flex-col items-center justify-center gap-4 py-12 px-4 text-center">
      <div className="text-4xl">⚠️</div>
      <p className="text-lg font-medium text-gray-800">{displayMsg}</p>
      {code === "pdf_preview_failed" && (
        <p className="text-sm text-gray-500">คุณสามารถดาวน์โหลดเอกสารได้โดยตรง</p>
      )}
      {onRetry && (
        <button
          onClick={onRetry}
          className="mt-2 px-4 py-2 bg-blue-600 text-white rounded-lg text-sm active:scale-95"
        >
          ลองใหม่
        </button>
      )}
    </div>
  );
}
