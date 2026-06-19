"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";
import {
  api,
  type DocumentDetail,
  type ExternalSigner,
  type AuditEntry,
  type SigEvent,
  type StepProgress,
  type ResendResponse,
} from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import WorkflowProgress from "@/components/WorkflowProgress";

interface PageProps {
  params: { id: string };
}

// Normalize a timestamp before new Date(). The List endpoints return Postgres
// `::text` (e.g. "2026-06-18 15:53:37.571+00") — space separator + short "+00"
// offset — which iOS Safari's JavaScriptCore rejects as Invalid Date. The
// resend/invite endpoints return RFC3339 ("...T...Z"). Convert the space to "T"
// and pad a short "+HH" / "-HH" offset to "+HH:00" so both parse on every
// browser. Falls back to the raw string if parsing still fails.
function formatDateTime(raw: string): string {
  if (!raw) return "";
  let s = raw.trim().replace(" ", "T");
  // Pad short offsets like "+00" / "-07" → "+00:00" / "-07:00"
  s = s.replace(/([+-]\d{2})$/, "$1:00");
  const d = new Date(s);
  if (isNaN(d.getTime())) return raw;
  return d.toLocaleString("th-TH");
}

// external_signers.status CHECK (0001_init.up.sql): pending,signed,expired,cancelled
const SIGNER_STATUS_LABELS: Record<string, string> = {
  pending:   "รอเซ็น",
  signed:    "เซ็นแล้ว",
  expired:   "หมดอายุ",
  cancelled: "ยกเลิก",
};

// documents.status CHECK (0001_init.up.sql): imported,pending,rejected,completed,cancelled
const DOC_STATUS_LABELS: Record<string, string> = {
  imported:  "นำเข้าแล้ว",
  pending:   "รอเซ็น",
  rejected:  "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
};

function signerStatusBadge(status: string) {
  const colors: Record<string, string> = {
    pending:   "bg-amber-100 text-amber-700",
    signed:    "bg-green-100 text-green-700",
    expired:   "bg-gray-100 text-gray-500",
    cancelled: "bg-gray-100 text-gray-500",
  };
  return colors[status] ?? "bg-gray-100 text-gray-500";
}

function isAdminRole(roles: string[]): boolean {
  return roles.some((r) => ["document_admin", "system_admin", "auditor"].includes(r));
}

function canMutateSigner(roles: string[]): boolean {
  return roles.some((r) => ["document_admin", "system_admin"].includes(r));
}

type ResendModalState =
  | { open: false }
  | { open: true; signerId: number; signerName: string; phase: "form" }
  | { open: true; signerId: number; signerName: string; phase: "submitting" }
  | { open: true; signerId: number; signerName: string; phase: "success"; result: ResendResponse };

export default function AdminDocDetailPage({ params }: PageProps) {
  const router = useRouter();
  const docId = parseInt(params.id, 10);

  const [doc, setDoc] = useState<DocumentDetail | null>(null);
  const [steps, setSteps] = useState<StepProgress[]>([]);
  const [signers, setSigners] = useState<ExternalSigner[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditEntry[]>([]);
  const [sigEvents, setSigEvents] = useState<SigEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [cancellingId, setCancellingId] = useState<number | null>(null);
  const [resendModal, setResendModal] = useState<ResendModalState>({ open: false });
  const [resendHours, setResendHours] = useState("72");
  const [resendError, setResendError] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);

  // One-time token in ref — never in React state, never re-fetched (Phase 3 Step 4 contract)
  const tokenRef = useRef<string | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const [linkCopied, setLinkCopied] = useState(false);

  // userRoles kept in state and set inside load() — NOT read at the top-level
  // component body, which would call sessionStorage during SSR and crash
  // (ReferenceError: sessionStorage is not defined). Dynamic routes skip
  // static prerender at build time, so this only surfaces at request-time SSR.
  const [userRoles, setUserRoles] = useState<string[]>([]);

  const load = useCallback(async () => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    const user = getUser<{ roles: string[] }>();
    if (!isAdminRole(user?.roles ?? [])) { router.replace("/inbox"); return; }
    setUserRoles(user?.roles ?? []);

    setLoading(true);
    setError(null);
    try {
      const [docRes, wfRes, signersRes, auditRes] = await Promise.all([
        api.getDocumentDetail(token, docId),
        api.workflowStatus(token, docId),
        api.listExternalSigners(token, docId),
        api.getAuditLogs(token, docId),
      ]);

      if (!docRes.success) {
        setError(docRes.error.code);
        return;
      }
      setDoc(docRes.data);

      if (wfRes.success) setSteps(wfRes.data.steps ?? []);
      if (signersRes.success) setSigners(signersRes.data ?? []);
      if (auditRes.success) {
        setAuditLogs(auditRes.data.audit_logs ?? []);
        setSigEvents(auditRes.data.signature_events ?? []);
      }
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [docId, router]);

  useEffect(() => { load(); }, [load]);

  const handleCancel = async (signerId: number) => {
    const token = getAccessToken();
    if (!token) return;
    setCancellingId(signerId);
    setActionMsg(null);
    try {
      const res = await api.cancelSigner(token, docId, signerId);
      if (!res.success) {
        setActionMsg(`ยกเลิกไม่สำเร็จ: ${res.error.message || res.error.code}`);
      } else {
        setActionMsg("ยกเลิกผู้เซ็นสำเร็จ");
        // Re-fetch signers after cancel
        const updated = await api.listExternalSigners(token, docId);
        if (updated.success) setSigners(updated.data ?? []);
      }
    } catch {
      setActionMsg("เกิดข้อผิดพลาดในการเชื่อมต่อ");
    } finally {
      setCancellingId(null);
    }
  };

  const openResend = (signer: ExternalSigner) => {
    setResendHours("72");
    setResendError(null);
    tokenRef.current = null;
    setTokenCopied(false);
    setLinkCopied(false);
    setResendModal({ open: true, signerId: signer.id, signerName: signer.name, phase: "form" });
  };

  const handleResend = async () => {
    if (!resendModal.open) return;
    const hours = parseInt(resendHours, 10);
    // Cap must match the backend maxExpiryHours = 168 (external_signers.go)
    if (isNaN(hours) || hours < 1 || hours > 168) {
      setResendError("กรอกจำนวนชั่วโมง 1–168");
      return;
    }
    const token = getAccessToken();
    if (!token) return;
    setResendModal((s) => s.open ? { ...s, phase: "submitting" } : s);
    setResendError(null);
    try {
      const res = await api.resendSigner(token, docId, resendModal.signerId, hours);
      if (!res.success) {
        setResendError(res.error.message || res.error.code);
        setResendModal((s) => s.open ? { ...s, phase: "form" } : s);
        return;
      }
      tokenRef.current = res.data.token;
      setResendModal((s) =>
        s.open ? { ...s, phase: "success", result: res.data } : s
      );
    } catch {
      setResendError("เกิดข้อผิดพลาดในการเชื่อมต่อ");
      setResendModal((s) => s.open ? { ...s, phase: "form" } : s);
    }
  };

  const closeResend = async () => {
    setResendModal({ open: false });
    tokenRef.current = null;
    setActionMsg(null);
    // Re-fetch status after resend — publish/resend response may lag DB state
    const token = getAccessToken();
    if (token) {
      const updated = await api.listExternalSigners(token, docId);
      if (updated.success) setSigners(updated.data ?? []);
    }
  };

  const copyToken = async () => {
    if (!tokenRef.current) return;
    await navigator.clipboard.writeText(tokenRef.current);
    setTokenCopied(true);
  };

  const copyLink = async () => {
    if (!tokenRef.current) return;
    await navigator.clipboard.writeText(`${window.location.origin}/external/${tokenRef.current}`);
    setLinkCopied(true);
  };

  // getAccessToken() touches sessionStorage — only safe in the browser. During
  // request-time SSR (dynamic route) window is undefined; guard so the route
  // renders the loading shell server-side instead of 500ing before the client
  // hydrates. loading starts true so SSR and client first-render both show the
  // spinner (no hydration mismatch).
  const token = typeof window === "undefined" ? "" : (getAccessToken() ?? "");

  const isTerminal = doc?.status === "completed" || doc?.status === "cancelled" || doc?.status === "rejected";

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  if (error || !doc) {
    return (
      <main className="min-h-screen bg-gray-50 flex flex-col">
        <header className="bg-white border-b border-gray-200 px-4 py-4">
          <button onClick={() => router.back()} className="text-blue-600 text-sm">← กลับ</button>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <ErrorState code={error ?? "not_found"} onRetry={error === "network_error" ? load : undefined} />
        </div>
      </main>
    );
  }

  return (
    <>
      <main className="min-h-screen bg-gray-50 flex flex-col">
        <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-12 z-10">
          <div className="max-w-3xl mx-auto flex items-center gap-3">
            <button onClick={() => router.back()} className="text-blue-600 text-sm flex-shrink-0">← กลับ</button>
            <div className="flex-1 min-w-0">
              <h1 className="text-base font-bold text-gray-900 truncate">
                {doc.doc_format_code} — {doc.doc_no}
              </h1>
              <p className="text-xs text-gray-500">
                {DOC_STATUS_LABELS[doc.status] ?? doc.status}
                {doc.amount && ` · ฿${parseFloat(doc.amount).toLocaleString()}`}
              </p>
            </div>
          </div>
        </header>

        <div className="max-w-3xl mx-auto w-full px-4 py-4 flex flex-col gap-4">

          {/* Document metadata */}
          <div className="bg-white rounded-xl border border-gray-200 p-4">
            <p className="text-sm font-medium text-gray-700 mb-3">รายละเอียดเอกสาร</p>
            <dl className="grid grid-cols-2 gap-x-4 gap-y-2 text-sm">
              <dt className="text-gray-500">รูปแบบ</dt>
              <dd className="text-gray-800 font-medium">{doc.doc_format_code}</dd>
              <dt className="text-gray-500">เลขเอกสาร</dt>
              <dd className="text-gray-800 break-all">{doc.doc_no}</dd>
              {doc.doc_date && (
                <>
                  <dt className="text-gray-500">วันที่</dt>
                  <dd className="text-gray-800">{doc.doc_date}</dd>
                </>
              )}
              {doc.amount && (
                <>
                  <dt className="text-gray-500">จำนวนเงิน</dt>
                  <dd className="text-gray-800">฿{parseFloat(doc.amount).toLocaleString()}</dd>
                </>
              )}
              <dt className="text-gray-500">เวอร์ชัน Workflow</dt>
              <dd className="text-gray-800">{doc.workflow_version}</dd>
              {doc.sync_status && (
                <>
                  <dt className="text-gray-500">สถานะ SML</dt>
                  <dd className="text-gray-800">{doc.sync_status}</dd>
                </>
              )}
            </dl>

            {/* Download links */}
            <div className="mt-4 flex gap-2">
              <a
                href={`${api.originalPdfUrl(docId)}?token=${encodeURIComponent(token)}`}
                target="_blank"
                rel="noopener noreferrer"
                className="flex-1 text-center py-2 border border-gray-300 rounded-lg text-sm text-gray-700"
              >
                ดาวน์โหลด PDF ต้นฉบับ
              </a>
              {doc.status === "completed" && (
                <a
                  href={`${api.finalPdfUrl(docId)}?token=${encodeURIComponent(token)}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex-1 text-center py-2 bg-blue-600 text-white rounded-lg text-sm font-medium"
                >
                  ดาวน์โหลด PDF ฉบับจริง
                </a>
              )}
            </div>
          </div>

          {/* Workflow progress */}
          {steps.length > 0 && (
            <div className="bg-white rounded-xl border border-gray-200 p-4">
              <p className="text-sm font-medium text-gray-700 mb-3">ขั้นตอน Workflow</p>
              <WorkflowProgress steps={steps} currentSeq={steps.find((s) => !s.complete)?.sequence_no ?? 1} />
            </div>
          )}

          {/* External signers */}
          <div className="bg-white rounded-xl border border-gray-200 p-4">
            <p className="text-sm font-medium text-gray-700 mb-3">ผู้เซ็นภายนอก</p>
            {actionMsg && (
              <div className="mb-3 text-xs bg-blue-50 text-blue-700 rounded-lg px-3 py-2">{actionMsg}</div>
            )}
            {signers.length === 0 ? (
              <p className="text-sm text-gray-400 text-center py-4">ไม่มีผู้เซ็นภายนอก</p>
            ) : (
              <div className="flex flex-col gap-3">
                {signers.map((s) => (
                  <div key={s.id} className="border border-gray-100 rounded-lg p-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-gray-800">{s.name}</p>
                        {s.email && <p className="text-xs text-gray-500">{s.email}</p>}
                        {s.phone && <p className="text-xs text-gray-500">{s.phone}</p>}
                        <p className="text-xs text-gray-400 mt-1">
                          หมดอายุ {formatDateTime(s.expires_at)}
                        </p>
                      </div>
                      <span className={`text-xs px-2 py-1 rounded-full flex-shrink-0 ${signerStatusBadge(s.status)}`}>
                        {SIGNER_STATUS_LABELS[s.status] ?? s.status}
                      </span>
                    </div>

                    {/* Actions — only for document_admin/system_admin and non-terminal docs */}
                    {canMutateSigner(userRoles) && !isTerminal && (
                      <div className="mt-2 flex gap-2">
                        {s.status === "pending" && (
                          <button
                            onClick={() => openResend(s)}
                            className="flex-1 py-1.5 border border-blue-300 text-blue-600 rounded-lg text-xs"
                          >
                            ส่งลิงก์ใหม่
                          </button>
                        )}
                        {(s.status === "pending" || s.status === "expired") && (
                          <button
                            onClick={() => handleCancel(s.id)}
                            disabled={cancellingId === s.id}
                            className="flex-1 py-1.5 border border-red-200 text-red-600 rounded-lg text-xs disabled:opacity-40"
                          >
                            {cancellingId === s.id ? "กำลังยกเลิก..." : "ยกเลิก"}
                          </button>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Audit timeline */}
          <div className="bg-white rounded-xl border border-gray-200 p-4">
            <p className="text-sm font-medium text-gray-700 mb-3">ประวัติเอกสาร</p>
            {auditLogs.length === 0 && sigEvents.length === 0 ? (
              <p className="text-sm text-gray-400 text-center py-4">ยังไม่มีประวัติ</p>
            ) : (
              <div className="flex flex-col gap-2">
                {/* Signature events */}
                {sigEvents.map((e) => (
                  <div key={`sig-${e.id}`} className="flex gap-3 py-2 border-b border-gray-50 last:border-0">
                    <div className="w-8 h-8 rounded-full bg-green-100 flex items-center justify-center flex-shrink-0 text-xs">
                      ✍
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-gray-800">
                        <span className="font-medium">{e.signer_name}</span>
                        {" — "}{e.action}
                        {e.signer_type === "external" && (
                          <span className="ml-1 text-xs text-gray-400">(ภายนอก)</span>
                        )}
                      </p>
                      {e.comment && <p className="text-xs text-gray-500 mt-0.5 italic">{e.comment}</p>}
                      <p className="text-xs text-gray-400 mt-0.5">
                        {e.signed_at.replace("T", " ").slice(0, 19)}
                        {e.ip_address && ` · ${e.ip_address}`}
                      </p>
                    </div>
                  </div>
                ))}
                {/* Audit log entries */}
                {auditLogs.map((e) => (
                  <div key={`al-${e.id}`} className="flex gap-3 py-2 border-b border-gray-50 last:border-0">
                    <div className="w-8 h-8 rounded-full bg-blue-50 flex items-center justify-center flex-shrink-0 text-xs">
                      📋
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-gray-800">
                        {e.action}
                        {e.actor_id && <span className="text-xs text-gray-400 ml-1">(user {e.actor_id})</span>}
                      </p>
                      {e.reason && <p className="text-xs text-gray-500 mt-0.5 italic">{e.reason}</p>}
                      <p className="text-xs text-gray-400 mt-0.5">{e.created_at.slice(0, 19)}</p>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </main>

      {/* Resend modal */}
      {resendModal.open && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={resendModal.phase === "success" ? closeResend : undefined} />
          <div className="relative bg-white rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-xl">

            {resendModal.phase === "success" ? (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center gap-2">
                  <span className="text-2xl">✅</span>
                  <div>
                    <h2 className="text-base font-bold text-gray-900">ส่งลิงก์ใหม่สำเร็จ</h2>
                    <p className="text-xs text-gray-500">สำหรับ {resendModal.signerName}</p>
                  </div>
                </div>

                <div className="bg-red-50 border border-red-200 rounded-xl p-3">
                  <p className="text-xs font-semibold text-red-700 mb-1">⚠ คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก</p>
                  <p className="text-xs text-red-600">ระบบจะไม่สามารถแสดงโทเคนหรือลิงก์นี้ซ้ำได้อีก</p>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-gray-600">ลิงก์เซ็นเอกสาร</p>
                  <div className="bg-gray-50 rounded-lg px-3 py-2 text-xs font-mono text-gray-700 break-all">
                    {window.location.origin}/external/{tokenRef.current}
                  </div>
                  <button
                    onClick={copyLink}
                    className={`w-full py-2.5 rounded-lg text-sm font-medium active:scale-95 ${linkCopied ? "bg-green-600 text-white" : "bg-blue-600 text-white"}`}
                  >
                    {linkCopied ? "คัดลอกลิงก์แล้ว ✓" : "คัดลอกลิงก์"}
                  </button>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-gray-600">โทเคน</p>
                  <div className="bg-gray-50 rounded-lg px-3 py-2 text-xs font-mono text-gray-700 break-all">
                    {tokenRef.current}
                  </div>
                  <button
                    onClick={copyToken}
                    className={`w-full py-2 rounded-lg text-sm active:scale-95 ${tokenCopied ? "bg-green-100 text-green-700 border border-green-300" : "bg-gray-100 text-gray-700 border border-gray-300"}`}
                  >
                    {tokenCopied ? "คัดลอกโทเคนแล้ว ✓" : "คัดลอกโทเคน"}
                  </button>
                </div>

                <p className="text-xs text-gray-500 text-center">
                  หมดอายุ: {formatDateTime(resendModal.result.expires_at)}
                </p>

                <button onClick={closeResend} className="w-full py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 active:scale-95">
                  ปิด
                </button>
              </div>
            ) : (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-base font-bold text-gray-900">ส่งลิงก์ใหม่</h2>
                  <button onClick={() => setResendModal({ open: false })} className="text-gray-400 text-xl leading-none">×</button>
                </div>
                <p className="text-sm text-gray-600">สำหรับ <strong>{resendModal.signerName}</strong></p>
                <div>
                  <label className="text-xs font-medium text-gray-600 block mb-1">หมดอายุใน (ชั่วโมง)</label>
                  <input
                    value={resendHours}
                    onChange={(e) => setResendHours(e.target.value)}
                    type="number"
                    min={1}
                    max={168}
                    className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
                    disabled={resendModal.phase === "submitting"}
                  />
                  <p className="text-xs text-gray-400 mt-1">สูงสุด 168 ชั่วโมง (7 วัน)</p>
                </div>
                {resendError && (
                  <p className="text-xs text-red-600 bg-red-50 rounded-lg px-3 py-2">{resendError}</p>
                )}
                <button
                  onClick={handleResend}
                  disabled={resendModal.phase === "submitting"}
                  className="w-full py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium disabled:opacity-40 active:scale-95"
                >
                  {resendModal.phase === "submitting" ? "กำลังสร้างลิงก์..." : "สร้างลิงก์ใหม่"}
                </button>
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
