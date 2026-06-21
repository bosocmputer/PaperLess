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
import { Button, Card, Input, Spinner, StatusBadge } from "@/components/ui";

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

// documents.status CHECK (0001_init.up.sql): imported,pending,rejected,completed,cancelled
const DOC_STATUS_LABELS: Record<string, string> = {
  imported:  "นำเข้าแล้ว",
  pending:   "รอเซ็น",
  rejected:  "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
};

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
      <div className="min-h-screen flex items-center justify-center text-brand">
        <Spinner size="md" />
      </div>
    );
  }

  if (error || !doc) {
    return (
      <main className="min-h-screen flex flex-col">
        <header className="bg-surface border-b border-line px-4 py-3">
          <button onClick={() => router.back()} className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 rounded-md">← กลับ</button>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <ErrorState code={error ?? "not_found"} onRetry={error === "network_error" ? load : undefined} />
        </div>
      </main>
    );
  }

  return (
    <>
      <main className="min-h-screen flex flex-col">
        <header className="bg-surface border-b border-line px-4 py-3 sticky top-12 z-10">
          <div className="max-w-3xl mx-auto flex items-center gap-3">
            <button onClick={() => router.back()} className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 flex-shrink-0 rounded-md">← กลับ</button>
            <div className="flex-1 min-w-0">
              <h1 className="text-base font-bold text-ink truncate">
                {doc.doc_format_code} — {doc.doc_no}
              </h1>
              <p className="text-xs text-muted">
                {DOC_STATUS_LABELS[doc.status] ?? doc.status}
                {doc.amount && ` · ฿${parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}`}
              </p>
            </div>
          </div>
        </header>

        <div className="max-w-3xl mx-auto w-full px-4 py-4 flex flex-col gap-4">

          {/* Document metadata */}
          <Card>
            <p className="text-sm font-semibold text-ink mb-3">รายละเอียดเอกสาร</p>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm items-center">
              <dt className="text-muted">รูปแบบ</dt>
              <dd className="text-ink font-medium">{doc.doc_format_code}</dd>
              <dt className="text-muted">เลขเอกสาร</dt>
              <dd className="text-ink break-all">{doc.doc_no}</dd>
              {doc.doc_date && (
                <>
                  <dt className="text-muted">วันที่</dt>
                  <dd className="text-ink">{doc.doc_date}</dd>
                </>
              )}
              {doc.amount && (
                <>
                  <dt className="text-muted">จำนวนเงิน</dt>
                  <dd className="text-ink">฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}</dd>
                </>
              )}
              <dt className="text-muted">เวอร์ชัน Workflow</dt>
              <dd className="text-ink">{doc.workflow_version}</dd>
              <dt className="text-muted">สถานะเอกสาร</dt>
              <dd><StatusBadge kind="document" status={doc.status} /></dd>
              {doc.sync_status && (
                <>
                  <dt className="text-muted">สถานะ SML</dt>
                  <dd><StatusBadge kind="sync" status={doc.sync_status} /></dd>
                </>
              )}
            </dl>

            {/* Download links */}
            <div className="mt-4 flex gap-2">
              <a
                href={`${api.originalPdfUrl(docId)}?token=${encodeURIComponent(token)}`}
                target="_blank"
                rel="noopener noreferrer"
                className="flex-1 text-center h-11 inline-flex items-center justify-center border border-line-strong rounded-md text-sm text-ink hover:bg-surface-muted"
              >
                ดาวน์โหลด PDF ต้นฉบับ
              </a>
              {doc.status === "completed" && (
                <a
                  href={`${api.finalPdfUrl(docId)}?token=${encodeURIComponent(token)}`}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="flex-1 text-center h-11 inline-flex items-center justify-center bg-brand text-white rounded-md text-sm font-medium hover:bg-brand-700"
                >
                  ดาวน์โหลด PDF ฉบับจริง
                </a>
              )}
            </div>
          </Card>

          {/* Workflow progress */}
          {steps.length > 0 && (
            <Card>
              <p className="text-sm font-semibold text-ink mb-3">ขั้นตอน Workflow</p>
              <WorkflowProgress steps={steps} currentSeq={steps.find((s) => !s.complete)?.sequence_no ?? 1} />
            </Card>
          )}

          {/* External signers */}
          <Card>
            <p className="text-sm font-semibold text-ink mb-3">ผู้เซ็นภายนอก</p>
            {actionMsg && (
              <div className="mb-3 text-xs bg-info-bg text-info-fg rounded-md px-3 py-2">{actionMsg}</div>
            )}
            {signers.length === 0 ? (
              <p className="text-sm text-subtle text-center py-4">ไม่มีผู้เซ็นภายนอก</p>
            ) : (
              <div className="flex flex-col gap-3">
                {signers.map((s) => (
                  <div key={s.id} className="border border-line rounded-md p-3">
                    <div className="flex items-start justify-between gap-2">
                      <div className="flex-1 min-w-0">
                        <p className="text-sm font-medium text-ink">{s.name}</p>
                        {s.email && <p className="text-xs text-muted truncate">{s.email}</p>}
                        {s.phone && <p className="text-xs text-muted">{s.phone}</p>}
                        <p className="text-xs text-subtle mt-1">
                          หมดอายุ {formatDateTime(s.expires_at)}
                        </p>
                      </div>
                      <StatusBadge kind="signer" status={s.status} />
                    </div>

                    {/* Actions — only for document_admin/system_admin and non-terminal docs */}
                    {canMutateSigner(userRoles) && !isTerminal && (
                      <div className="mt-2 flex gap-2">
                        {s.status === "pending" && (
                          <Button variant="outline" size="sm" block onClick={() => openResend(s)}>
                            ส่งลิงก์ใหม่
                          </Button>
                        )}
                        {(s.status === "pending" || s.status === "expired") && (
                          <Button
                            variant="outline"
                            size="sm"
                            block
                            onClick={() => handleCancel(s.id)}
                            disabled={cancellingId === s.id}
                            loading={cancellingId === s.id}
                            className="border-danger/40 text-danger-fg"
                          >
                            {cancellingId === s.id ? "กำลังยกเลิก..." : "ยกเลิก"}
                          </Button>
                        )}
                      </div>
                    )}
                  </div>
                ))}
              </div>
            )}
          </Card>

          {/* Audit timeline */}
          <Card>
            <p className="text-sm font-semibold text-ink mb-3">ประวัติเอกสาร</p>
            {auditLogs.length === 0 && sigEvents.length === 0 ? (
              <p className="text-sm text-subtle text-center py-4">ยังไม่มีประวัติ</p>
            ) : (
              <div className="flex flex-col gap-2">
                {/* Signature events */}
                {sigEvents.map((e) => (
                  <div key={`sig-${e.id}`} className="flex gap-3 py-2 border-b border-line last:border-0">
                    <div className="w-8 h-8 rounded-full bg-success-bg text-success-fg flex items-center justify-center flex-shrink-0 text-xs">
                      ✍
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-ink">
                        <span className="font-medium">{e.signer_name}</span>
                        {" — "}{e.action}
                        {e.signer_type === "external" && (
                          <span className="ml-1 text-xs text-subtle">(ภายนอก)</span>
                        )}
                      </p>
                      {e.comment && <p className="text-xs text-muted mt-0.5 italic">{e.comment}</p>}
                      <p className="text-xs text-subtle mt-0.5">
                        {e.signed_at.replace("T", " ").slice(0, 19)}
                        {e.ip_address && ` · ${e.ip_address}`}
                      </p>
                    </div>
                  </div>
                ))}
                {/* Audit log entries */}
                {auditLogs.map((e) => (
                  <div key={`al-${e.id}`} className="flex gap-3 py-2 border-b border-line last:border-0">
                    <div className="w-8 h-8 rounded-full bg-info-bg text-info-fg flex items-center justify-center flex-shrink-0 text-xs">
                      📋
                    </div>
                    <div className="flex-1 min-w-0">
                      <p className="text-sm text-ink">
                        {e.action}
                        {e.actor_id && <span className="text-xs text-subtle ml-1">(user {e.actor_id})</span>}
                      </p>
                      {e.reason && <p className="text-xs text-muted mt-0.5 italic">{e.reason}</p>}
                      <p className="text-xs text-subtle mt-0.5">{e.created_at.slice(0, 19)}</p>
                    </div>
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
      </main>

      {/* Resend modal */}
      {resendModal.open && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={resendModal.phase === "success" ? closeResend : undefined} />
          <div className="relative bg-surface rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-pop">

            {resendModal.phase === "success" ? (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center gap-3">
                  <span className="flex items-center justify-center w-10 h-10 rounded-full bg-success-bg text-success-fg text-xl">✓</span>
                  <div>
                    <h2 className="text-base font-bold text-ink">ส่งลิงก์ใหม่สำเร็จ</h2>
                    <p className="text-xs text-muted">สำหรับ {resendModal.signerName}</p>
                  </div>
                </div>

                <div className="bg-danger-bg border border-danger/30 rounded-md p-3">
                  <p className="text-xs font-semibold text-danger-fg mb-1">⚠ คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก</p>
                  <p className="text-xs text-danger-fg">ระบบจะไม่สามารถแสดงโทเคนหรือลิงก์นี้ซ้ำได้อีก</p>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-muted">ลิงก์เซ็นเอกสาร</p>
                  <div className="bg-surface-muted rounded-md px-3 py-2 text-xs font-mono text-ink break-all">
                    {window.location.origin}/external/{tokenRef.current}
                  </div>
                  <Button
                    onClick={copyLink}
                    className={linkCopied ? "bg-success hover:bg-success" : undefined}
                    block
                  >
                    {linkCopied ? "คัดลอกลิงก์แล้ว ✓" : "คัดลอกลิงก์"}
                  </Button>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-muted">โทเคน</p>
                  <div className="bg-surface-muted rounded-md px-3 py-2 text-xs font-mono text-ink break-all">
                    {tokenRef.current}
                  </div>
                  <Button onClick={copyToken} variant={tokenCopied ? "secondary" : "outline"} size="sm" block>
                    {tokenCopied ? "คัดลอกโทเคนแล้ว ✓" : "คัดลอกโทเคน"}
                  </Button>
                </div>

                <p className="text-xs text-muted text-center">
                  หมดอายุ: {formatDateTime(resendModal.result.expires_at)}
                </p>

                <Button onClick={closeResend} variant="outline" block>ปิด</Button>
              </div>
            ) : (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-base font-bold text-ink">ส่งลิงก์ใหม่</h2>
                  <button onClick={() => setResendModal({ open: false })} className="text-subtle text-2xl leading-none touch-target -mr-2 px-2">×</button>
                </div>
                <p className="text-sm text-muted">สำหรับ <strong className="text-ink">{resendModal.signerName}</strong></p>
                <Input
                  label="หมดอายุใน (ชั่วโมง)"
                  value={resendHours}
                  onChange={(e) => setResendHours(e.target.value)}
                  type="number"
                  min={1}
                  max={168}
                  hint="สูงสุด 168 ชั่วโมง (7 วัน)"
                  disabled={resendModal.phase === "submitting"}
                />
                {resendError && (
                  <p className="text-xs text-danger-fg bg-danger-bg rounded-md px-3 py-2">{resendError}</p>
                )}
                <Button
                  onClick={handleResend}
                  loading={resendModal.phase === "submitting"}
                  block
                >
                  {resendModal.phase === "submitting" ? "กำลังสร้างลิงก์..." : "สร้างลิงก์ใหม่"}
                </Button>
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}
