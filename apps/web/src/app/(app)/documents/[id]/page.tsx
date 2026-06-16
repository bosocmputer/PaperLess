"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { api } from "@/lib/api";
import { getAccessToken } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import SignaturePad from "@/components/SignaturePad";
import WorkflowProgress from "@/components/WorkflowProgress";
import type { StepProgress } from "@/lib/api";

interface PageProps {
  params: { id: string };
}

type PageState =
  | { stage: "loading" }
  | { stage: "error"; code: string }
  | { stage: "viewing"; taskId: number; docId: number; docNo: string; docFormatCode: string; seqNo: number; steps: StepProgress[] }
  | { stage: "signing"; taskId: number; docId: number; docNo: string; docFormatCode: string; seqNo: number; steps: StepProgress[] }
  | { stage: "submitting" }
  | { stage: "rejecting"; taskId: number }
  | { stage: "done"; message: string };

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
      // Re-load to get authoritative state from DB.
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
