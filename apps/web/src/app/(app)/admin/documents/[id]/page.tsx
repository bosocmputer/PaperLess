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
import Attachments from "@/components/Attachments";
import { Button, Icon, Input, Spinner, StatusBadge } from "@/components/ui";

interface PageProps {
  params: { id: string };
}

function formatDateTime(raw: string): string {
  if (!raw) return "";
  let s = raw.trim().replace(" ", "T");
  s = s.replace(/([+-]\d{2})$/, "$1:00");
  const d = new Date(s);
  if (isNaN(d.getTime())) return raw;
  return d.toLocaleString("th-TH");
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

type Tab = "details" | "workflow" | "attachments" | "signers" | "audit";

const TABS: { id: Tab; label: string }[] = [
  { id: "details", label: "รายละเอียด" },
  { id: "workflow", label: "Workflow" },
  { id: "attachments", label: "ไฟล์แนบ" },
  { id: "signers", label: "ผู้เซ็นภายนอก" },
  { id: "audit", label: "ประวัติ" },
];

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
  const [activeTab, setActiveTab] = useState<Tab>("details");

  const [cancellingId, setCancellingId] = useState<number | null>(null);
  const [resendModal, setResendModal] = useState<ResendModalState>({ open: false });
  const [resendHours, setResendHours] = useState("72");
  const [resendError, setResendError] = useState<string | null>(null);
  const [actionMsg, setActionMsg] = useState<string | null>(null);
  const tokenRef = useRef<string | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const [linkCopied, setLinkCopied] = useState(false);
  const [userRoles, setUserRoles] = useState<string[]>([]);

  const load = useCallback(async () => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    const user = getUser<{ roles: string[] }>();
    if (!isAdminRole(user?.roles ?? [])) { router.replace("/inbox"); return; }
    setUserRoles(user?.roles ?? []);
    setLoading(true); setError(null);
    try {
      const [docRes, wfRes, signersRes, auditRes] = await Promise.all([
        api.getDocumentDetail(token, docId),
        api.workflowStatus(token, docId),
        api.listExternalSigners(token, docId),
        api.getAuditLogs(token, docId),
      ]);
      if (!docRes.success) { setError(docRes.error.code); return; }
      setDoc(docRes.data);
      if (wfRes.success) setSteps(wfRes.data.steps ?? []);
      if (signersRes.success) setSigners(signersRes.data ?? []);
      if (auditRes.success) {
        setAuditLogs(auditRes.data.audit_logs ?? []);
        setSigEvents(auditRes.data.signature_events ?? []);
      }
    } catch { setError("network_error"); } finally { setLoading(false); }
  }, [docId, router]);

  useEffect(() => { load(); }, [load]);

  const handleCancel = async (signerId: number) => {
    const token = getAccessToken();
    if (!token) return;
    setCancellingId(signerId); setActionMsg(null);
    try {
      const res = await api.cancelSigner(token, docId, signerId);
      if (!res.success) { setActionMsg(`ยกเลิกไม่สำเร็จ: ${res.error.message || res.error.code}`); }
      else {
        setActionMsg("ยกเลิกผู้เซ็นสำเร็จ");
        const updated = await api.listExternalSigners(token, docId);
        if (updated.success) setSigners(updated.data ?? []);
      }
    } catch { setActionMsg("เกิดข้อผิดพลาดในการเชื่อมต่อ"); } finally { setCancellingId(null); }
  };

  const openResend = (signer: ExternalSigner) => {
    setResendHours("72"); setResendError(null); tokenRef.current = null;
    setTokenCopied(false); setLinkCopied(false);
    setResendModal({ open: true, signerId: signer.id, signerName: signer.name, phase: "form" });
  };

  const handleResend = async () => {
    if (!resendModal.open) return;
    const hours = parseInt(resendHours, 10);
    if (isNaN(hours) || hours < 1 || hours > 168) { setResendError("กรอกจำนวนชั่วโมง 1–168"); return; }
    const token = getAccessToken();
    if (!token) return;
    setResendModal((s) => s.open ? { ...s, phase: "submitting" } : s);
    setResendError(null);
    try {
      const res = await api.resendSigner(token, docId, resendModal.signerId, hours);
      if (!res.success) {
        setResendError(res.error.message || res.error.code);
        setResendModal((s) => s.open ? { ...s, phase: "form" } : s); return;
      }
      tokenRef.current = res.data.token;
      setResendModal((s) => s.open ? { ...s, phase: "success", result: res.data } : s);
    } catch {
      setResendError("เกิดข้อผิดพลาด");
      setResendModal((s) => s.open ? { ...s, phase: "form" } : s);
    }
  };

  const closeResend = async () => {
    setResendModal({ open: false }); tokenRef.current = null; setActionMsg(null);
    const token = getAccessToken();
    if (token) { const updated = await api.listExternalSigners(token, docId); if (updated.success) setSigners(updated.data ?? []); }
  };

  const token = typeof window === "undefined" ? "" : (getAccessToken() ?? "");
  const isTerminal = doc?.status === "completed" || doc?.status === "cancelled" || doc?.status === "rejected";

  if (loading) return <div className="min-h-screen flex items-center justify-center text-brand"><Spinner size="md" /></div>;

  if (error || !doc) {
    return (
      <div className="min-h-screen flex flex-col">
        <div className="bg-surface border-b border-line px-4 py-3 sticky top-14 lg:top-0 z-10">
          <button onClick={() => router.back()} className="flex items-center gap-1.5 text-sm font-medium text-brand-700 touch-target">
            <Icon name="arrow-left" size={16} /> กลับ
          </button>
        </div>
        <div className="flex-1 flex items-center justify-center p-8">
          <ErrorState code={error ?? "not_found"} onRetry={error === "network_error" ? load : undefined} />
        </div>
      </div>
    );
  }

  return (
    <>
      <div className="min-h-screen">
        {/* Page header */}
        <div className="bg-surface border-b border-line sticky top-14 lg:top-0 z-10">
          <div className="max-w-5xl mx-auto px-4 lg:px-6 py-3">
            <div className="flex items-start justify-between gap-3">
              <div className="flex items-start gap-3 min-w-0">
                <button onClick={() => router.back()} className="flex items-center gap-1 text-sm font-medium text-brand-700 mt-0.5 flex-shrink-0 touch-target">
                  <Icon name="arrow-left" size={16} />
                </button>
                <div className="min-w-0">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-xs font-bold text-brand-700 bg-brand-50 rounded px-1.5 py-0.5">{doc.doc_format_code}</span>
                    <h1 className="text-base font-bold text-ink truncate">{doc.doc_no}</h1>
                    <StatusBadge kind="document" status={doc.status} />
                  </div>
                  {doc.amount && (
                    <p className="text-sm font-semibold text-brand-700 mt-0.5">
                      ฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}
                    </p>
                  )}
                </div>
              </div>

              {/* Download buttons */}
              <div className="flex gap-2 flex-shrink-0">
                <a href={`${api.originalPdfUrl(docId)}?token=${encodeURIComponent(token)}`} target="_blank" rel="noopener noreferrer"
                  className="hidden sm:inline-flex items-center gap-1.5 h-9 px-3 rounded-lg border border-line-strong text-xs font-medium text-ink hover:bg-surface-muted transition-colors">
                  <Icon name="download" size={14} /> PDF ต้นฉบับ
                </a>
                {doc.status === "completed" && (
                  <a href={`${api.finalPdfUrl(docId)}?token=${encodeURIComponent(token)}`} target="_blank" rel="noopener noreferrer"
                    className="hidden sm:inline-flex items-center gap-1.5 h-9 px-3 rounded-lg bg-brand text-white text-xs font-medium hover:bg-brand-700 transition-colors">
                    <Icon name="download" size={14} /> PDF ฉบับจริง
                  </a>
                )}
              </div>
            </div>

            {/* Tabs */}
            <div className="flex gap-0 mt-3 -mb-px overflow-x-auto scrollbar-none">
              {TABS.map((tab) => (
                <button
                  key={tab.id}
                  onClick={() => setActiveTab(tab.id)}
                  className={`px-4 py-2 text-sm font-medium whitespace-nowrap border-b-2 transition-colors ${
                    activeTab === tab.id
                      ? "border-brand text-brand-700"
                      : "border-transparent text-muted hover:text-ink hover:border-line"
                  }`}
                >
                  {tab.label}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="max-w-5xl mx-auto px-4 lg:px-6 py-5">
          {/* Details tab */}
          {activeTab === "details" && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">รายละเอียดเอกสาร</p>
              <dl className="grid grid-cols-[140px_1fr] gap-x-4 gap-y-3 text-sm">
                <dt className="text-muted">รูปแบบ</dt>
                <dd className="font-medium text-ink">{doc.doc_format_code}</dd>
                <dt className="text-muted">เลขเอกสาร</dt>
                <dd className="text-ink break-all">{doc.doc_no}</dd>
                {doc.doc_date && <>
                  <dt className="text-muted">วันที่</dt>
                  <dd className="text-ink">{doc.doc_date}</dd>
                </>}
                {doc.amount && <>
                  <dt className="text-muted">จำนวนเงิน</dt>
                  <dd className="font-semibold text-ink">฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}</dd>
                </>}
                <dt className="text-muted">เวอร์ชัน Workflow</dt>
                <dd className="text-ink">{doc.workflow_version}</dd>
                <dt className="text-muted">สถานะเอกสาร</dt>
                <dd><StatusBadge kind="document" status={doc.status} /></dd>
                {doc.sync_status && <>
                  <dt className="text-muted">สถานะ SML</dt>
                  <dd><StatusBadge kind="sync" status={doc.sync_status} /></dd>
                </>}
              </dl>

              {/* Mobile PDF buttons */}
              <div className="sm:hidden mt-5 flex gap-2">
                <a href={`${api.originalPdfUrl(docId)}?token=${encodeURIComponent(token)}`} target="_blank" rel="noopener noreferrer"
                  className="flex-1 flex items-center justify-center gap-1.5 h-10 rounded-lg border border-line-strong text-sm text-ink hover:bg-surface-muted">
                  <Icon name="download" size={15} /> PDF ต้นฉบับ
                </a>
                {doc.status === "completed" && (
                  <a href={`${api.finalPdfUrl(docId)}?token=${encodeURIComponent(token)}`} target="_blank" rel="noopener noreferrer"
                    className="flex-1 flex items-center justify-center gap-1.5 h-10 rounded-lg bg-brand text-white text-sm font-medium hover:bg-brand-700">
                    <Icon name="download" size={15} /> PDF ฉบับจริง
                  </a>
                )}
              </div>
            </div>
          )}

          {/* Workflow tab */}
          {activeTab === "workflow" && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">ขั้นตอน Workflow</p>
              {steps.length === 0
                ? <p className="text-sm text-subtle text-center py-8">ไม่มีข้อมูล Workflow</p>
                : <WorkflowProgress steps={steps} currentSeq={steps.find((s) => !s.complete)?.sequence_no ?? 1} />
              }
            </div>
          )}

          {/* Attachments tab */}
          {activeTab === "attachments" && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <Attachments docId={docId} token={token} canEdit />
            </div>
          )}

          {/* Signers tab */}
          {activeTab === "signers" && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">ผู้เซ็นภายนอก</p>
              {actionMsg && (
                <div className="mb-4 flex items-center gap-2 text-sm bg-info-bg text-info-fg rounded-lg px-3 py-2.5">
                  <Icon name="information-circle" size={16} />
                  {actionMsg}
                </div>
              )}
              {signers.length === 0 ? (
                <p className="text-sm text-subtle text-center py-8">ไม่มีผู้เซ็นภายนอก</p>
              ) : (
                <div className="flex flex-col gap-3">
                  {signers.map((s) => (
                    <div key={s.id} className="border border-line rounded-xl p-4">
                      <div className="flex items-start justify-between gap-3">
                        <div className="flex-1 min-w-0">
                          <p className="font-semibold text-ink">{s.name}</p>
                          {s.email && <p className="text-sm text-muted mt-0.5 truncate">{s.email}</p>}
                          {s.phone && <p className="text-sm text-muted">{s.phone}</p>}
                          <p className="text-xs text-subtle mt-1">หมดอายุ {formatDateTime(s.expires_at)}</p>
                        </div>
                        <StatusBadge kind="signer" status={s.status} />
                      </div>
                      {canMutateSigner(userRoles) && !isTerminal && (
                        <div className="mt-3 flex gap-2">
                          {s.status === "pending" && (
                            <Button variant="outline" size="sm" onClick={() => openResend(s)}>ส่งลิงก์ใหม่</Button>
                          )}
                          {(s.status === "pending" || s.status === "expired") && (
                            <Button variant="outline" size="sm" onClick={() => handleCancel(s.id)}
                              disabled={cancellingId === s.id} loading={cancellingId === s.id}
                              className="border-danger/40 text-danger-fg hover:bg-danger-bg">
                              {cancellingId === s.id ? "กำลังยกเลิก..." : "ยกเลิก"}
                            </Button>
                          )}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}

          {/* Audit tab */}
          {activeTab === "audit" && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">ประวัติเอกสาร</p>
              {auditLogs.length === 0 && sigEvents.length === 0 ? (
                <p className="text-sm text-subtle text-center py-8">ยังไม่มีประวัติ</p>
              ) : (
                <div className="flex flex-col">
                  {sigEvents.map((e) => (
                    <div key={`sig-${e.id}`} className="flex gap-3 py-3 border-b border-line last:border-0">
                      <div className="w-8 h-8 rounded-full bg-success-bg text-success-fg flex items-center justify-center flex-shrink-0">
                        <Icon name="pencil-square" size={14} />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="text-sm text-ink">
                          <span className="font-semibold">{e.signer_name}</span> — {e.action}
                          {e.signer_type === "external" && <span className="ml-1 text-xs text-subtle">(ภายนอก)</span>}
                        </p>
                        {e.comment && <p className="text-xs text-muted mt-0.5 italic">{e.comment}</p>}
                        <p className="text-xs text-subtle mt-0.5">{e.signed_at.replace("T", " ").slice(0, 19)}{e.ip_address && ` · ${e.ip_address}`}</p>
                      </div>
                    </div>
                  ))}
                  {auditLogs.map((e) => (
                    <div key={`al-${e.id}`} className="flex gap-3 py-3 border-b border-line last:border-0">
                      <div className="w-8 h-8 rounded-full bg-info-bg text-info-fg flex items-center justify-center flex-shrink-0">
                        <Icon name="clock" size={14} />
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="text-sm text-ink">{e.action}{e.actor_id && <span className="text-xs text-subtle ml-1">(user {e.actor_id})</span>}</p>
                        {e.reason && <p className="text-xs text-muted mt-0.5 italic">{e.reason}</p>}
                        <p className="text-xs text-subtle mt-0.5">{e.created_at.slice(0, 19)}</p>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* Resend modal */}
      {resendModal.open && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={resendModal.phase === "success" ? closeResend : undefined} />
          <div className="relative bg-surface rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-pop">
            {resendModal.phase === "success" ? (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center gap-3">
                  <div className="w-10 h-10 rounded-full bg-success-bg text-success-fg flex items-center justify-center">
                    <Icon name="check-circle" size={20} />
                  </div>
                  <div>
                    <h2 className="text-base font-bold text-ink">ส่งลิงก์ใหม่สำเร็จ</h2>
                    <p className="text-xs text-muted">สำหรับ {resendModal.signerName}</p>
                  </div>
                </div>
                <div className="bg-danger-bg border border-danger/30 rounded-xl p-3">
                  <p className="text-xs font-semibold text-danger-fg mb-1 flex items-center gap-1.5">
                    <Icon name="exclamation-triangle" size={14} /> คัดลอกเดี๋ยวนี้ — จะไม่แสดงอีก
                  </p>
                  <p className="text-xs text-danger-fg">ระบบจะไม่สามารถแสดงโทเคนหรือลิงก์นี้ซ้ำได้อีก</p>
                </div>
                <div className="flex flex-col gap-2">
                  <p className="text-xs font-semibold text-muted">ลิงก์เซ็นเอกสาร</p>
                  <div className="bg-surface-muted rounded-lg px-3 py-2 text-xs font-mono text-ink break-all border border-line">
                    {window.location.origin}/external/{tokenRef.current}
                  </div>
                  <Button onClick={async () => { await navigator.clipboard.writeText(`${window.location.origin}/external/${tokenRef.current}`); setLinkCopied(true); }}
                    className={linkCopied ? "bg-success hover:bg-success" : undefined} block>
                    <Icon name={linkCopied ? "check" : "copy"} size={15} />
                    {linkCopied ? "คัดลอกลิงก์แล้ว" : "คัดลอกลิงก์"}
                  </Button>
                </div>
                <div className="flex flex-col gap-2">
                  <p className="text-xs font-semibold text-muted">โทเคน</p>
                  <div className="bg-surface-muted rounded-lg px-3 py-2 text-xs font-mono text-ink break-all border border-line">
                    {tokenRef.current}
                  </div>
                  <Button onClick={async () => { await navigator.clipboard.writeText(tokenRef.current!); setTokenCopied(true); }}
                    variant={tokenCopied ? "secondary" : "outline"} size="sm" block>
                    <Icon name={tokenCopied ? "check" : "copy"} size={14} />
                    {tokenCopied ? "คัดลอกโทเคนแล้ว" : "คัดลอกโทเคน"}
                  </Button>
                </div>
                {resendModal.phase === "success" && (
                  <p className="text-xs text-muted text-center">หมดอายุ: {formatDateTime(resendModal.result.expires_at)}</p>
                )}
                <Button onClick={closeResend} variant="outline" block>ปิด</Button>
              </div>
            ) : (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-base font-bold text-ink">ส่งลิงก์ใหม่</h2>
                  <button onClick={() => setResendModal({ open: false })} className="text-subtle hover:text-ink touch-target -mr-2 px-2 flex items-center">
                    <Icon name="x" size={20} />
                  </button>
                </div>
                <p className="text-sm text-muted">สำหรับ <strong className="text-ink">{resendModal.signerName}</strong></p>
                <Input label="หมดอายุใน (ชั่วโมง)" value={resendHours}
                  onChange={(e) => setResendHours(e.target.value)} type="number" min={1} max={168}
                  hint="สูงสุด 168 ชั่วโมง (7 วัน)" disabled={resendModal.phase === "submitting"} />
                {resendError && (
                  <p className="text-xs text-danger-fg bg-danger-bg rounded-lg px-3 py-2.5">{resendError}</p>
                )}
                <Button onClick={handleResend} loading={resendModal.phase === "submitting"} block>
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
