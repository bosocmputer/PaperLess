"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api, type ExternalSigner, type InviteResponse, type StepProgress } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import SignaturePad from "@/components/SignaturePad";
import WorkflowProgress from "@/components/WorkflowProgress";

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

  // Must match the external_signers.status CHECK constraint (0001_init.up.sql):
  // ('pending','signed','expired','cancelled'). No 'active' value exists.
  const statusLabel: Record<string, string> = {
    pending: "รอเซ็น",
    signed: "เซ็นแล้ว",
    expired: "หมดอายุ",
    cancelled: "ยกเลิก",
  };

  return (
    <>
      <main className="min-h-screen bg-gray-50 flex flex-col">
        <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-0 z-10">
          <div className="flex items-center gap-3">
            <button onClick={onBack} className="text-blue-600 text-sm flex-shrink-0">← กลับ</button>
            <div className="flex-1 min-w-0">
              <h1 className="text-base font-bold text-gray-900 truncate">{docFormatCode} — {docNo}</h1>
              <p className="text-xs text-gray-500">จัดการเอกสาร</p>
            </div>
          </div>
        </header>

        <div className="max-w-lg mx-auto w-full px-4 py-4 flex flex-col gap-4">
          {/* Workflow progress */}
          <WorkflowProgress steps={steps} currentSeq={steps.find((s) => !s.complete)?.sequence_no ?? 1} />

          {/* Invite button — shown only when there's a waiting external task */}
          {canInvite && hasWaitingExternal && (
            <div className="bg-white rounded-xl border border-gray-200 p-4">
              <p className="text-sm font-medium text-gray-700 mb-1">ผู้เซ็นภายนอก</p>
              <p className="text-xs text-gray-500 mb-3">เชิญผู้เซ็นที่ไม่ได้อยู่ในระบบมาเซ็นเอกสาร</p>
              <button
                onClick={openModal}
                className="w-full py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium active:scale-95"
              >
                เชิญผู้เซ็นภายนอก
              </button>
            </div>
          )}

          {/* Existing signers list */}
          <div className="bg-white rounded-xl border border-gray-200 p-4">
            <p className="text-sm font-medium text-gray-700 mb-3">รายชื่อผู้เซ็นภายนอก</p>
            {signersLoading ? (
              <div className="flex justify-center py-4">
                <div className="w-6 h-6 border-3 border-blue-600 border-t-transparent rounded-full animate-spin" />
              </div>
            ) : signers.length === 0 ? (
              <p className="text-sm text-gray-400 text-center py-4">ยังไม่มีผู้เซ็นภายนอก</p>
            ) : (
              <div className="flex flex-col gap-2">
                {signers.map((s) => (
                  <div key={s.id} className="flex items-center justify-between py-2 border-b border-gray-100 last:border-0">
                    <div>
                      <p className="text-sm font-medium text-gray-800">{s.name}</p>
                      {s.email && <p className="text-xs text-gray-500">{s.email}</p>}
                      {s.phone && <p className="text-xs text-gray-500">{s.phone}</p>}
                    </div>
                    <span className={`text-xs px-2 py-1 rounded-full flex-shrink-0 ${
                      s.status === "signed"
                        ? "bg-green-100 text-green-700"
                        : s.status === "expired" || s.status === "cancelled"
                        ? "bg-gray-100 text-gray-500"
                        : "bg-amber-100 text-amber-700"
                    }`}>
                      {statusLabel[s.status] ?? s.status}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      </main>

      {/* Invite modal */}
      {modal.open && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={modal.phase === "success" ? closeModal : undefined} />
          <div className="relative bg-white rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-xl">

            {modal.phase === "form" || modal.phase === "submitting" ? (
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center justify-between">
                  <h2 className="text-base font-bold text-gray-900">เชิญผู้เซ็นภายนอก</h2>
                  <button onClick={closeModal} className="text-gray-400 text-xl leading-none">×</button>
                </div>

                <div className="flex flex-col gap-3">
                  <div>
                    <label className="text-xs font-medium text-gray-600 block mb-1">ชื่อ <span className="text-red-500">*</span></label>
                    <input
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      placeholder="ชื่อ-นามสกุล"
                      maxLength={200}
                      className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
                      disabled={modal.phase === "submitting"}
                    />
                  </div>
                  <div>
                    <label className="text-xs font-medium text-gray-600 block mb-1">อีเมล (ไม่บังคับ)</label>
                    <input
                      value={email}
                      onChange={(e) => setEmail(e.target.value)}
                      placeholder="example@email.com"
                      type="email"
                      maxLength={254}
                      className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
                      disabled={modal.phase === "submitting"}
                    />
                  </div>
                  <div>
                    <label className="text-xs font-medium text-gray-600 block mb-1">เบอร์โทร (ไม่บังคับ)</label>
                    <input
                      value={phone}
                      onChange={(e) => setPhone(e.target.value)}
                      placeholder="0812345678"
                      type="tel"
                      maxLength={20}
                      className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
                      disabled={modal.phase === "submitting"}
                    />
                  </div>
                  <div>
                    <label className="text-xs font-medium text-gray-600 block mb-1">หมดอายุใน (ชั่วโมง)</label>
                    <input
                      value={expiresHours}
                      onChange={(e) => setExpiresHours(e.target.value)}
                      type="number"
                      min={1}
                      max={168}
                      placeholder="72"
                      title="จำนวนชั่วโมงที่ลิงก์จะหมดอายุ (1–168)"
                      className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
                      disabled={modal.phase === "submitting"}
                    />
                    <p className="text-xs text-gray-400 mt-1">ค่าเริ่มต้น 72 ชั่วโมง (3 วัน) — สูงสุด 168 ชั่วโมง (7 วัน)</p>
                  </div>
                </div>

                {inviteError && (
                  <p className="text-xs text-red-600 bg-red-50 rounded-lg px-3 py-2">{inviteError}</p>
                )}

                <button
                  onClick={handleInvite}
                  disabled={modal.phase === "submitting" || !name.trim()}
                  className="w-full py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium disabled:opacity-40 active:scale-95"
                >
                  {modal.phase === "submitting" ? "กำลังสร้างลิงก์..." : "สร้างลิงก์เชิญ"}
                </button>
              </div>
            ) : (
              // Success — show token once
              <div className="p-5 flex flex-col gap-4">
                <div className="flex items-center gap-2">
                  <span className="text-2xl">✅</span>
                  <div>
                    <h2 className="text-base font-bold text-gray-900">สร้างลิงก์สำเร็จ</h2>
                    <p className="text-xs text-gray-500">สำหรับ {modal.result.name}</p>
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
                    onClick={() => copyLink(modal.result)}
                    className={`w-full py-2.5 rounded-lg text-sm font-medium active:scale-95 ${
                      linkCopied
                        ? "bg-green-600 text-white"
                        : "bg-blue-600 text-white"
                    }`}
                  >
                    {linkCopied ? "คัดลอกลิงก์แล้ว ✓" : "คัดลอกลิงก์"}
                  </button>
                </div>

                <div className="flex flex-col gap-2">
                  <p className="text-xs font-medium text-gray-600">โทเคน (สำหรับ API โดยตรง)</p>
                  <div className="bg-gray-50 rounded-lg px-3 py-2 text-xs font-mono text-gray-700 break-all">
                    {tokenRef.current}
                  </div>
                  <button
                    onClick={copyToken}
                    className={`w-full py-2 rounded-lg text-sm active:scale-95 ${
                      tokenCopied
                        ? "bg-green-100 text-green-700 border border-green-300"
                        : "bg-gray-100 text-gray-700 border border-gray-300"
                    }`}
                  >
                    {tokenCopied ? "คัดลอกโทเคนแล้ว ✓" : "คัดลอกโทเคน"}
                  </button>
                </div>

                <p className="text-xs text-gray-500 text-center">
                  หมดอายุ: {new Date(modal.result.expires_at).toLocaleString("th-TH")}
                </p>

                <button
                  onClick={closeModal}
                  className="w-full py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 active:scale-95"
                >
                  ปิด
                </button>
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
      <div className="min-h-screen flex items-center justify-center">
        <div className="w-8 h-8 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
      </div>
    );
  }

  if (state.stage === "error") {
    return (
      <main className="min-h-screen bg-gray-50 flex flex-col">
        <header className="bg-white border-b border-gray-200 px-4 py-4">
          <button onClick={() => router.back()} className="text-blue-600 text-sm">← กลับ</button>
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
      <main className="min-h-screen bg-gray-50 flex flex-col items-center justify-center gap-4 px-4">
        <div className="text-5xl">✅</div>
        <p className="text-lg font-semibold text-gray-800">{state.message}</p>
        <button onClick={() => router.replace("/inbox")} className="px-6 py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium">
          กลับไปกล่องเอกสาร
        </button>
      </main>
    );
  }

  if (state.stage === "submitting") {
    return (
      <main className="min-h-screen bg-gray-50 flex flex-col items-center justify-center gap-4 px-4">
        <div className="w-10 h-10 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
        <p className="text-sm text-gray-600">{submittingStatus}</p>
      </main>
    );
  }

  if (state.stage === "rejecting") {
    return (
      <main className="min-h-screen bg-gray-50 flex flex-col">
        <header className="bg-white border-b border-gray-200 px-4 py-4 flex items-center gap-3">
          <button onClick={() => setState({ ...(state as typeof state), stage: "viewing" } as PageState)} className="text-blue-600 text-sm">← กลับ</button>
          <h1 className="text-base font-bold text-gray-900">ส่งคืนเอกสาร</h1>
        </header>
        <div className="max-w-lg mx-auto w-full px-4 py-6 flex flex-col gap-4">
          <p className="text-sm text-gray-600">กรุณาระบุเหตุผลในการส่งคืนเอกสาร</p>
          <textarea
            value={rejectReason}
            onChange={(e) => setRejectReason(e.target.value)}
            placeholder="เหตุผล..."
            rows={4}
            className="w-full border border-gray-300 rounded-xl px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-red-400"
          />
          {!rejectReason.trim() && (
            <p className="text-xs text-red-500">กรุณาระบุเหตุผล</p>
          )}
          <button
            onClick={handleReject}
            disabled={!rejectReason.trim()}
            className="py-2.5 bg-red-600 text-white rounded-lg text-sm font-medium disabled:opacity-40 active:scale-95"
          >
            ยืนยันการส่งคืน
          </button>
        </div>
      </main>
    );
  }

  // stages: viewing | signing
  const isSigning = state.stage === "signing";
  const pdfSrc = `${api.originalPdfUrl(state.docId)}?token=${encodeURIComponent(token)}`;

  return (
    <main className="min-h-screen bg-gray-50 flex flex-col">
      <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-0 z-10">
        <div className="flex items-center gap-3">
          <button onClick={() => router.back()} className="text-blue-600 text-sm flex-shrink-0">← กลับ</button>
          <div className="flex-1 min-w-0">
            <h1 className="text-base font-bold text-gray-900 truncate">{state.docFormatCode} — {state.docNo}</h1>
            <p className="text-xs text-gray-500">ขั้นที่ {state.seqNo} จาก {state.steps.length}</p>
          </div>
        </div>
      </header>

      <div className="max-w-lg mx-auto w-full px-4 py-4 flex flex-col gap-4">
        {/* Workflow progress */}
        <WorkflowProgress steps={state.steps} currentSeq={state.seqNo} />

        {/* PDF viewer */}
        <div className="bg-white rounded-xl border border-gray-200 overflow-hidden">
          <p className="text-xs font-medium text-gray-500 px-3 pt-3 pb-1">เอกสาร</p>
          {pdfError ? (
            <div className="px-3 pb-3">
              <ErrorState code="pdf_preview_failed" />
              <a
                href={pdfSrc}
                target="_blank"
                rel="noopener noreferrer"
                className="block text-center text-sm text-blue-600 underline mt-2"
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
        </div>

        {/* Signature section */}
        {!isSigning ? (
          <div className="bg-white rounded-xl border border-gray-200 p-4 flex flex-col gap-3">
            <p className="text-sm font-medium text-gray-700">ลายเซ็น</p>
            <SignaturePad
              onSign={handleSign}
              disabled={false}
            />
            <button
              onClick={() => setState({ stage: "rejecting", taskId: state.taskId })}
              className="text-sm text-red-600 underline text-center mt-1"
            >
              ส่งคืนเอกสาร
            </button>
          </div>
        ) : (
          <div className="bg-white rounded-xl border border-blue-200 p-4 flex flex-col gap-3">
            <p className="text-sm font-medium text-gray-700">ตรวจสอบและยืนยัน</p>
            <p className="text-xs text-gray-500">ลายเซ็นของคุณถูกบันทึกแล้ว กดยืนยันเพื่อส่งข้อมูล</p>
            <div className="flex gap-2">
              <button
                onClick={() => {
                  signatureRef.current = null;
                  setState({ stage: "viewing", taskId: state.taskId, docId: state.docId, docNo: state.docNo, docFormatCode: state.docFormatCode, seqNo: state.seqNo, steps: state.steps });
                }}
                className="flex-1 py-2.5 border border-gray-300 rounded-lg text-sm text-gray-700 active:scale-95"
              >
                วาดใหม่
              </button>
              <button
                onClick={handleSubmit}
                className="flex-1 py-2.5 bg-blue-600 text-white rounded-lg text-sm font-medium active:scale-95"
              >
                ยืนยันเซ็นเอกสาร
              </button>
            </div>
          </div>
        )}
      </div>
    </main>
  );
}
