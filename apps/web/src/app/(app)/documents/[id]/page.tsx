"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ExternalSigner, type InviteResponse, type StepProgress } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import SignaturePad from "@/components/SignaturePad";
import Attachments from "@/components/Attachments";
import WorkflowProgress from "@/components/WorkflowProgress";
import { Button, Card, Input, Spinner, StatusBadge } from "@/components/ui";

interface PageProps {
  params: { id: string };
}

// ── Signer page state ─────────────────────────────────────────────────────────

type PageState =
  | { stage: "loading" }
  | { stage: "error"; code: string }
  | { stage: "viewing"; taskId: number; docId: number; docNo: string; docFormatCode: string; seqNo: number; steps: StepProgress[] }
  | { stage: "signing"; taskId: number; docId: number; docNo: string; docFormatCode: string; seqNo: number; steps: StepProgress[] }
  | { stage: "submitting" }
  | { stage: "rejecting"; taskId: number }
  | { stage: "done"; message: string }
  | { stage: "admin"; docId: number; docNo: string; docFormatCode: string; docStatus: string; steps: StepProgress[] };

// ── Admin invite modal state ──────────────────────────────────────────────────

type ModalState =
  | { open: false }
  | { open: true; phase: "form" }
  | { open: true; phase: "submitting" }
  | { open: true; phase: "success"; result: InviteResponse };

function isAdmin(roles: string[]): boolean {
  return roles.includes("document_admin") || roles.includes("system_admin");
}

// Shared sticky page header with a back action.
function PageHeader({ title, subtitle, onBack }: { title: string; subtitle?: string; onBack: () => void }) {
  return (
    <header className="bg-surface border-b border-line px-4 py-3 sticky top-0 z-10">
      <div className="max-w-lg mx-auto w-full flex items-center gap-3">
        <button
          onClick={onBack}
          className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 flex-shrink-0 rounded-md"
        >
          ← กลับ
        </button>
        <div className="flex-1 min-w-0">
          <h1 className="text-base font-bold text-ink truncate">{title}</h1>
          {subtitle && <p className="text-xs text-muted">{subtitle}</p>}
        </div>
      </div>
    </header>
  );
}

// ── Admin view component ──────────────────────────────────────────────────────

function AdminDocView({
  docId,
  docNo,
  docFormatCode,
  docStatus,
  steps,
  onBack,
}: {
  docId: number;
  docNo: string;
  docFormatCode: string;
  docStatus: string;
  steps: StepProgress[];
  onBack: () => void;
}) {
  const [modal, setModal] = useState<ModalState>({ open: false });
  const [signers, setSigners] = useState<ExternalSigner[]>([]);
  const [signersLoading, setSignersLoading] = useState(true);

  // Invite form fields
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [phone, setPhone] = useState("");
  const [expiresHours, setExpiresHours] = useState("72");
  const [inviteError, setInviteError] = useState<string | null>(null);

  // One-time token display — stored in ref so we never re-render it from state after dismissal
  const tokenRef = useRef<string | null>(null);
  const [tokenCopied, setTokenCopied] = useState(false);
  const [linkCopied, setLinkCopied] = useState(false);

  const hasWaitingExternal = steps.some((s) => !s.complete);
  const canInvite = docStatus !== "completed" && docStatus !== "cancelled" && docStatus !== "rejected";

  const loadSigners = useCallback(async () => {
    const token = getAccessToken();
    if (!token) return;
    setSignersLoading(true);
    try {
      const res = await api.listExternalSigners(token, docId);
      if (res.success) setSigners(res.data ?? []);
    } finally {
      setSignersLoading(false);
    }
  }, [docId]);

  useEffect(() => { loadSigners(); }, [loadSigners]);

  const openModal = () => {
    setName("");
    setEmail("");
    setPhone("");
    setExpiresHours("72");
    setInviteError(null);
    tokenRef.current = null;
    setTokenCopied(false);
    setLinkCopied(false);
    setModal({ open: true, phase: "form" });
  };

  const closeModal = () => {
    setModal({ open: false });
    // Refresh signer list after invite (or after dismissing success)
    loadSigners();
  };

  const handleInvite = async () => {
    if (!name.trim()) { setInviteError("กรุณาระบุชื่อ"); return; }
    const hours = parseInt(expiresHours, 10);
    // Cap must match the backend (maxExpiryHours = 168 in external_signers.go).
    // The API silently clamps above this; reject in the UI so the admin can't
    // enter a value that would be quietly overridden.
    if (isNaN(hours) || hours < 1 || hours > 168) {
      setInviteError("กรอกจำนวนชั่วโมง 1–168 (สูงสุด 7 วัน)");
      return;
    }
    setInviteError(null);
    setModal({ open: true, phase: "submitting" });

    const token = getAccessToken();
    if (!token) return;

    const res = await api.invite(token, docId, {
      name: name.trim(),
      email: email.trim() || undefined,
      phone: phone.trim() || undefined,
      expires_in_hours: hours,
    });

    if (!res.success) {
      setInviteError(res.error.message || res.error.code);
      setModal({ open: true, phase: "form" });
      return;
    }

    tokenRef.current = res.data.token;
    setModal({ open: true, phase: "success", result: res.data });
  };

  const copyToken = async () => {
    if (!tokenRef.current) return;
    await navigator.clipboard.writeText(tokenRef.current);
    setTokenCopied(true);
  };

  const copyLink = async (result: InviteResponse) => {
    const link = `${window.location.origin}/external/${tokenRef.current}`;
    await navigator.clipboard.writeText(link);
    setLinkCopied(true);
    void result; // suppress unused warning
  };

  const submitting = modal.open && modal.phase === "submitting";

  return (
    <>
      <main className="min-h-screen flex flex-col">
        <PageHeader title={`${docFormatCode} — ${docNo}`} subtitle="จัดการเอกสาร" onBack={onBack} />

        <div className="max-w-lg mx-auto w-full px-4 py-4 flex flex-col gap-4">
          {/* Workflow progress */}
          <WorkflowProgress steps={steps} currentSeq={steps.find((s) => !s.complete)?.sequence_no ?? 1} />

          {/* Invite button — shown only when there's a waiting external task */}
          {canInvite && hasWaitingExternal && (
            <Card className="flex flex-col gap-3">
              <div>
                <p className="text-sm font-semibold text-ink">ผู้เซ็นภายนอก</p>
                <p className="text-xs text-muted mt-0.5">เชิญผู้เซ็นที่ไม่ได้อยู่ในระบบมาเซ็นเอกสาร</p>
              </div>
              <Button onClick={openModal} block>เชิญผู้เซ็นภายนอก</Button>
            </Card>
          )}

          {/* Existing signers list */}
          <Card>
            <p className="text-sm font-semibold text-ink mb-3">รายชื่อผู้เซ็นภายนอก</p>
            {signersLoading ? (
              <div className="flex justify-center py-4 text-brand">
                <Spinner size="sm" />
              </div>
            ) : signers.length === 0 ? (
              <p className="text-sm text-subtle text-center py-4">ยังไม่มีผู้เซ็นภายนอก</p>
            ) : (
              <div className="flex flex-col">
                {signers.map((s) => (
                  <div key={s.id} className="flex items-center justify-between gap-2 py-2.5 border-b border-line last:border-0">
                    <div className="min-w-0">
                      <p className="text-sm font-medium text-ink truncate">{s.name}</p>
                      {s.email && <p className="text-xs text-muted truncate">{s.email}</p>}
                      {s.phone && <p className="text-xs text-muted">{s.phone}</p>}
                    </div>
                    <StatusBadge kind="signer" status={s.status} />
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
      </main>

      {/* Invite modal */}
      {modal.open && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={modal.phase === "success" ? closeModal : undefined} />
          <div className="relative bg-surface rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-pop">

            {modal.phase === "form" || modal.phase === "submitting" ? (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-base font-bold text-ink">เชิญผู้เซ็นภายนอก</h2>
                  <button onClick={closeModal} className="text-subtle text-2xl leading-none touch-target -mr-2 px-2">×</button>
                </div>

                <div className="flex flex-col gap-3">
                  <Input
                    label="ชื่อ *"
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="ชื่อ-นามสกุล"
                    maxLength={200}
                    disabled={submitting}
                  />
                  <Input
                    label="อีเมล (ไม่บังคับ)"
                    value={email}
                    onChange={(e) => setEmail(e.target.value)}
                    placeholder="example@email.com"
                    type="email"
                    maxLength={254}
                    disabled={submitting}
                  />
                  <Input
                    label="เบอร์โทร (ไม่บังคับ)"
                    value={phone}
                    onChange={(e) => setPhone(e.target.value)}
                    placeholder="0812345678"
                    type="tel"
                    maxLength={20}
                    disabled={submitting}
                  />
                  <Input
                    label="หมดอายุใน (ชั่วโมง)"
                    value={expiresHours}
                    onChange={(e) => setExpiresHours(e.target.value)}
                    type="number"
                    min={1}
                    max={168}
                    placeholder="72"
                    hint="ค่าเริ่มต้น 72 ชั่วโมง (3 วัน) — สูงสุด 168 ชั่วโมง (7 วัน)"
                    disabled={submitting}
                  />
                </div>

                {inviteError && (
                  <p className="text-xs text-danger-fg bg-danger-bg rounded-md px-3 py-2">{inviteError}</p>
                )}

                <Button onClick={handleInvite} disabled={!name.trim()} loading={submitting} block>
                  {submitting ? "กำลังสร้างลิงก์..." : "สร้างลิงก์เชิญ"}
                </Button>
              </div>
            ) : (
              // Success — show token once
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center gap-3">
                  <span className="flex items-center justify-center w-10 h-10 rounded-full bg-success-bg text-success-fg text-xl">✓</span>
                  <div>
                    <h2 className="text-base font-bold text-ink">สร้างลิงก์สำเร็จ</h2>
                    <p className="text-xs text-muted">สำหรับ {modal.result.name}</p>
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
                    onClick={() => copyLink(modal.result)}
                    className={linkCopied ? "bg-success hover:bg-success" : undefined}
                    block
                  >
                    {linkCopied ? "คัดลอกลิงก์แล้ว ✓" : "คัดลอกลิงก์"}
                  </Button>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-muted">โทเคน (สำหรับ API โดยตรง)</p>
                  <div className="bg-surface-muted rounded-md px-3 py-2 text-xs font-mono text-ink break-all">
                    {tokenRef.current}
                  </div>
                  <Button
                    onClick={copyToken}
                    variant={tokenCopied ? "secondary" : "outline"}
                    size="sm"
                    block
                  >
                    {tokenCopied ? "คัดลอกโทเคนแล้ว ✓" : "คัดลอกโทเคน"}
                  </Button>
                </div>

                <p className="text-xs text-muted text-center">
                  หมดอายุ: {new Date(modal.result.expires_at).toLocaleString("th-TH")}
                </p>

                <Button onClick={closeModal} variant="outline" block>ปิด</Button>
              </div>
            )}
          </div>
        </div>
      )}
    </>
  );
}

// ── Main page ─────────────────────────────────────────────────────────────────

export default function DocumentPage({ params }: PageProps) {
  const router = useRouter();
  const searchParams = useSearchParams();
  const taskIdParam = searchParams.get("taskId");
  const docId = parseInt(params.id, 10);
  const [state, setState] = useState<PageState>({ stage: "loading" });
  const [pdfError, setPdfError] = useState(false);
  const [rejectReason, setRejectReason] = useState("");
  const [submittingStatus, setSubmittingStatus] = useState<string | null>(null);
  const signatureRef = useRef<string | null>(null);
  const taskId = taskIdParam ? parseInt(taskIdParam, 10) : null;

  const load = useCallback(async () => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    setState({ stage: "loading" });

    const currentUser = getUser<{ roles: string[] }>();
    const userIsAdmin = isAdmin(currentUser?.roles ?? []);

    try {
      const [docRes, wfRes] = await Promise.all([
        api.getDocument(token, docId),
        api.workflowStatus(token, docId),
      ]);

      if (!docRes.success) {
        if (docRes.error.code === "unauthorized") { router.replace("/login"); return; }
        setState({ stage: "error", code: docRes.error.code });
        return;
      }
      if (!wfRes.success) {
        setState({ stage: "error", code: wfRes.error.code });
        return;
      }

      const doc = docRes.data as { id: number; doc_no: string; doc_format_code: string; status: string };
      const steps = wfRes.data.steps;

      // Admin with no taskId → show admin view
      if (!taskId && userIsAdmin) {
        setState({
          stage: "admin",
          docId,
          docNo: doc.doc_no,
          docFormatCode: doc.doc_format_code,
          docStatus: doc.status,
          steps,
        });
        return;
      }

      if (doc.status === "completed") {
        setState({ stage: "done", message: "เอกสารนี้เซ็นครบแล้ว" });
        return;
      }
      if (doc.status === "cancelled") {
        setState({ stage: "error", code: "document_already_completed" });
        return;
      }
      if (!taskId) {
        setState({ stage: "error", code: "not_allowed_to_sign" });
        return;
      }

      const taskRes = await api.getTask(token, taskId);
      if (!taskRes.success) {
        setState({ stage: "error", code: taskRes.error.code });
        return;
      }

      const task = taskRes.data as { id: number; status: string; sequence_no: number; condition_type: number };
      if (task.status === "waiting") {
        setState({ stage: "error", code: "waiting_for_previous" });
        return;
      }
      if (task.status !== "open") {
        setState({ stage: "error", code: "document_already_completed" });
        return;
      }

      setState({
        stage: "viewing",
        taskId: task.id,
        docId,
        docNo: doc.doc_no,
        docFormatCode: doc.doc_format_code,
        seqNo: task.sequence_no,
        steps,
      });
    } catch {
      setState({ stage: "error", code: "network_error" });
    }
  }, [docId, taskId, router]);

  useEffect(() => { load(); }, [load]);

  const handleSign = useCallback(async (hash: string) => {
    if (state.stage !== "viewing") return;
    signatureRef.current = hash;
    setState({ stage: "signing", taskId: state.taskId, docId: state.docId, docNo: state.docNo, docFormatCode: state.docFormatCode, seqNo: state.seqNo, steps: state.steps });
  }, [state]);

  const handleSubmit = async () => {
    if (state.stage !== "signing" || !signatureRef.current) return;
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    setState({ stage: "submitting" });
    setSubmittingStatus("กำลังส่งลายเซ็น...");

    try {
      const result = await api.sign(token, state.taskId, signatureRef.current, "");
      if (!result.success) {
        setState({ stage: "error", code: (result.error as { code: string }).code });
        return;
      }
      setState({ stage: "done", message: "เซ็นเอกสารสำเร็จ" });
    } catch {
      // Network dropped during submit — show status check, prevent double-submit.
      setSubmittingStatus("กำลังตรวจสอบสถานะ... กรุณารอสักครู่");
      await new Promise((r) => setTimeout(r, 3000));
      load();
    }
  };

  const handleReject = async () => {
    if (state.stage !== "rejecting" || !rejectReason.trim()) return;
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    const result = await api.reject(token, state.taskId, rejectReason.trim());
    if (!result.success) {
      setState({ stage: "error", code: (result.error as { code: string }).code });
      return;
    }
    setState({ stage: "done", message: "ส่งคืนเอกสารสำเร็จ" });
  };

  const token = getAccessToken() ?? "";

  // ── Render ────────────────────────────────────────────────────────────────

  if (state.stage === "loading") {
    return (
      <div className="min-h-screen flex items-center justify-center text-brand">
        <Spinner size="md" />
      </div>
    );
  }

  if (state.stage === "error") {
    return (
      <main className="min-h-screen flex flex-col">
        <header className="bg-surface border-b border-line px-4 py-3">
          <button onClick={() => router.back()} className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 rounded-md">← กลับ</button>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <ErrorState code={state.code} onRetry={state.code === "network_error" ? load : undefined} />
        </div>
      </main>
    );
  }

  if (state.stage === "admin") {
    return (
      <AdminDocView
        docId={state.docId}
        docNo={state.docNo}
        docFormatCode={state.docFormatCode}
        docStatus={state.docStatus}
        steps={state.steps}
        onBack={() => router.back()}
      />
    );
  }

  if (state.stage === "done") {
    return (
      <main className="min-h-screen flex flex-col items-center justify-center gap-4 px-4 text-center">
        <div className="flex items-center justify-center w-16 h-16 rounded-full bg-success-bg text-success-fg text-3xl">✓</div>
        <p className="text-lg font-semibold text-ink">{state.message}</p>
        <Button onClick={() => router.replace("/inbox")} size="lg">กลับไปกล่องเอกสาร</Button>
      </main>
    );
  }

  if (state.stage === "submitting") {
    return (
      <main className="min-h-screen flex flex-col items-center justify-center gap-4 px-4 text-brand">
        <Spinner size="lg" />
        <p className="text-sm text-muted">{submittingStatus}</p>
      </main>
    );
  }

  if (state.stage === "rejecting") {
    return (
      <main className="min-h-screen flex flex-col">
        <PageHeader title="ส่งคืนเอกสาร" onBack={() => setState({ ...(state as typeof state), stage: "viewing" } as PageState)} />
        <div className="max-w-lg mx-auto w-full px-4 py-6 flex flex-col gap-4">
          <p className="text-sm text-muted">กรุณาระบุเหตุผลในการส่งคืนเอกสาร</p>
          <textarea
            value={rejectReason}
            onChange={(e) => setRejectReason(e.target.value)}
            placeholder="เหตุผล..."
            rows={4}
            className="w-full border border-line-strong rounded-md px-3 py-2 text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-danger"
          />
          {!rejectReason.trim() && (
            <p className="text-xs text-danger-fg">กรุณาระบุเหตุผล</p>
          )}
          <Button onClick={handleReject} disabled={!rejectReason.trim()} variant="danger" block>
            ยืนยันการส่งคืน
          </Button>
        </div>
      </main>
    );
  }

  // stages: viewing | signing
  const isSigning = state.stage === "signing";
  const pdfSrc = `${api.originalPdfUrl(state.docId)}?token=${encodeURIComponent(token)}`;

  return (
    <main className="min-h-screen flex flex-col">
      <PageHeader
        title={`${state.docFormatCode} — ${state.docNo}`}
        subtitle={`ขั้นที่ ${state.seqNo} จาก ${state.steps.length}`}
        onBack={() => router.back()}
      />

      <div className="max-w-lg mx-auto w-full px-4 py-4 flex flex-col gap-4">
        {/* Workflow progress */}
        <WorkflowProgress steps={state.steps} currentSeq={state.seqNo} />

        {/* PDF viewer */}
        <Card padding="none" className="overflow-hidden">
          <p className="text-xs font-semibold text-muted px-3 pt-3 pb-1">เอกสาร</p>
          {pdfError ? (
            <div className="px-3 pb-3">
              <ErrorState code="pdf_preview_failed" />
              <a
                href={pdfSrc}
                target="_blank"
                rel="noopener noreferrer"
                className="block text-center text-sm text-brand-700 underline mt-2"
              >
                ดาวน์โหลด PDF
              </a>
            </div>
          ) : (
            <iframe
              src={pdfSrc}
              className="w-full border-0"
              style={{ height: 360 }}
              title="เอกสาร PDF"
              onError={() => setPdfError(true)}
            />
          )}
        </Card>

        {/* Attachments (read-only for signers) */}
        <Card>
          <Attachments docId={state.docId} token={token} canEdit={false} />
        </Card>

        {/* Signature section */}
        {!isSigning ? (
          <Card className="flex flex-col gap-3">
            <p className="text-sm font-semibold text-ink">ลายเซ็น</p>
            <SignaturePad
              onSign={handleSign}
              disabled={false}
            />
            <button
              onClick={() => setState({ stage: "rejecting", taskId: state.taskId })}
              className="text-sm text-danger-fg underline text-center mt-1 touch-target"
            >
              ส่งคืนเอกสาร
            </button>
          </Card>
        ) : (
          <Card className="flex flex-col gap-3 border-brand-200">
            <p className="text-sm font-semibold text-ink">ตรวจสอบและยืนยัน</p>
            <p className="text-xs text-muted">ลายเซ็นของคุณถูกบันทึกแล้ว กดยืนยันเพื่อส่งข้อมูล</p>
            <div className="flex gap-2">
              <Button
                variant="outline"
                block
                onClick={() => {
                  signatureRef.current = null;
                  setState({ stage: "viewing", taskId: state.taskId, docId: state.docId, docNo: state.docNo, docFormatCode: state.docFormatCode, seqNo: state.seqNo, steps: state.steps });
                }}
              >
                วาดใหม่
              </Button>
              <Button block onClick={handleSubmit}>
                ยืนยันเซ็นเอกสาร
              </Button>
            </div>
          </Card>
        )}
      </div>
    </main>
  );
}
