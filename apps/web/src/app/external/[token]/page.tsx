"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import { api, ExternalDocView } from "@/lib/api";
import SignaturePad from "@/components/SignaturePad";
import ErrorState from "@/components/ErrorState";
import { Button, Icon, Spinner } from "@/components/ui";

type Stage = "loading" | "view" | "signing" | "preview" | "submitting" | "done" | "error";

interface SigningState {
  signatureHash: string;
  consentAgreed: boolean;
  requestId: string;
}

const STEPS = ["อ่านเอกสาร", "วาดลายเซ็น", "ยืนยัน"] as const;

function StepBar({ current }: { current: 0 | 1 | 2 }) {
  return (
    <div className="flex items-center justify-center gap-0">
      {STEPS.map((label, i) => {
        const done = i < current;
        const active = i === current;
        return (
          <div key={i} className="flex items-center">
            <div className="flex flex-col items-center">
              <div className={`w-7 h-7 rounded-full flex items-center justify-center text-xs font-bold transition-colors ${
                done ? "bg-brand text-white" : active ? "bg-brand text-white ring-2 ring-brand ring-offset-2" : "bg-surface-muted border border-line text-muted"
              }`}>
                {done ? <Icon name="check" size={14} /> : i + 1}
              </div>
              <span className={`text-xs mt-1 whitespace-nowrap ${active ? "text-brand-700 font-semibold" : done ? "text-muted" : "text-subtle"}`}>
                {label}
              </span>
            </div>
            {i < STEPS.length - 1 && (
              <div className={`w-14 h-px mx-1 mb-4 transition-colors ${done ? "bg-brand" : "bg-line"}`} />
            )}
          </div>
        );
      })}
    </div>
  );
}

export default function ExternalSignPage() {
  const params = useParams();
  const signerToken = useRef<string>(typeof params.token === "string" ? params.token : "");

  const [stage, setStage] = useState<Stage>("loading");
  const [docView, setDocView] = useState<ExternalDocView | null>(null);
  const [errorCode, setErrorCode] = useState("");
  const [pdfObjectUrl, setPdfObjectUrl] = useState<string | null>(null);
  const [pdfError, setPdfError] = useState(false);
  const [signing, setSigning] = useState<SigningState>({
    signatureHash: "",
    consentAgreed: false,
    requestId: crypto.randomUUID(),
  });

  const token = signerToken.current;

  const loadPdf = useCallback((tok: string) => {
    const url = api.externalOriginalPdfUrl();
    const headers = api.externalOriginalPdfHeaders(tok);
    fetch(url, { headers })
      .then((res) => { if (!res.ok) { setPdfError(true); return; } return res.blob(); })
      .then((blob) => { if (blob) setPdfObjectUrl(URL.createObjectURL(blob)); })
      .catch(() => setPdfError(true));
  }, []);

  useEffect(() => {
    if (!token) { setErrorCode("external_link_invalid"); setStage("error"); return; }
    api.externalView(token).then((res) => {
      if (res.success) { setDocView(res.data); setStage("view"); loadPdf(token); }
      else { setErrorCode(res.error.code); setStage("error"); }
    }).catch(() => { setErrorCode("network_error"); setStage("error"); });
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => { return () => { if (pdfObjectUrl) URL.revokeObjectURL(pdfObjectUrl); }; }, [pdfObjectUrl]);

  const handleSign = useCallback((hash: string) => {
    setSigning((s) => ({ ...s, signatureHash: hash }));
    setStage("preview");
  }, []);

  const handleSubmit = async () => {
    if (!signing.consentAgreed) return;
    setStage("submitting");
    try {
      const res = await api.externalSign(token, signing.signatureHash, CONSENT_TEXT, signing.requestId);
      if (res.success) { setStage("done"); }
      else { setErrorCode(res.error.code); setStage("error"); }
    } catch { setErrorCode("network_error"); setStage("error"); }
  };

  // Loading
  if (stage === "loading") {
    return (
      <div className="min-h-screen flex flex-col items-center justify-center gap-3 text-brand">
        <Spinner size="lg" />
        <p className="text-sm text-muted">กำลังโหลด...</p>
      </div>
    );
  }

  // Error — network = special check-status screen
  if (stage === "error") {
    if (errorCode === "network_error") {
      return (
        <div className="min-h-screen flex flex-col items-center justify-center px-4 gap-4 text-center max-w-sm mx-auto">
          <div className="w-14 h-14 rounded-full bg-info-bg text-info-fg flex items-center justify-center">
            <Icon name="information-circle" size={28} />
          </div>
          <p className="text-lg font-bold text-ink">กำลังตรวจสอบสถานะ</p>
          <p className="text-sm text-muted">การเชื่อมต่อขัดข้องระหว่างการส่งข้อมูล เอกสารอาจได้รับการเซ็นแล้ว</p>
          <Button onClick={() => window.location.reload()}>ตรวจสอบสถานะ</Button>
        </div>
      );
    }
    return (
      <div className="min-h-screen flex items-center justify-center px-4">
        <div className="w-full max-w-sm"><ErrorState code={errorCode} /></div>
      </div>
    );
  }

  // Done
  if (stage === "done") {
    return (
      <div className="min-h-screen flex flex-col">
        {/* Security header */}
        <div className="bg-brand px-4 py-3">
          <div className="max-w-lg mx-auto flex items-center gap-2">
            <Icon name="lock-closed" size={14} className="text-brand-100" />
            <p className="text-xs text-brand-100 font-medium">Secure Transaction · PaperLess</p>
          </div>
        </div>
        <div className="flex-1 flex flex-col items-center justify-center gap-5 px-4 text-center">
          <div className="w-20 h-20 rounded-full bg-success-bg text-success-fg flex items-center justify-center">
            <Icon name="check-circle" size={40} />
          </div>
          <div>
            <h1 className="text-2xl font-bold text-ink">เซ็นเอกสารสำเร็จ</h1>
            <p className="text-sm text-muted mt-2">ลายเซ็นของคุณได้รับการบันทึกแล้ว</p>
            <p className="text-sm text-muted">ขอบคุณที่ดำเนินการ</p>
          </div>
          <div className="flex items-center gap-1.5 text-xs text-subtle">
            <Icon name="shield" size={14} />
            <span>ลงนามอิเล็กทรอนิกส์ตาม พ.ร.บ. 2544</span>
          </div>
        </div>
      </div>
    );
  }

  // Active flow: view | signing | preview | submitting
  const stepIndex: 0 | 1 | 2 = stage === "view" ? 0 : stage === "signing" ? 1 : 2;

  return (
    <div className="min-h-screen flex flex-col bg-bg">
      {/* Security header */}
      <div className="bg-brand sticky top-0 z-20">
        <div className="max-w-lg mx-auto px-4 py-2.5 flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Icon name="lock-closed" size={14} className="text-brand-100" />
            <p className="text-xs font-semibold text-brand-100">Secure Transaction</p>
          </div>
          <p className="text-xs text-brand-200">PaperLess</p>
        </div>
      </div>

      {/* Doc info bar */}
      {docView && (
        <div className="bg-surface border-b border-line">
          <div className="max-w-lg mx-auto px-4 py-3">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="text-xs font-bold text-brand-700 bg-brand-50 rounded px-1.5 py-0.5">{docView.doc_format_code}</span>
              <span className="text-sm font-semibold text-ink">{docView.doc_no}</span>
            </div>
            <p className="text-xs text-muted mt-0.5">สวัสดี คุณ{docView.signer_name}</p>
          </div>
        </div>
      )}

      <div className="max-w-lg mx-auto w-full px-4 py-5 flex flex-col gap-5">
        {/* Step indicator */}
        <StepBar current={stepIndex} />

        {/* Stage: view */}
        {stage === "view" && (
          <>
            <div className="bg-surface border border-line rounded-xl shadow-card overflow-hidden">
              <div className="flex items-center gap-2 px-4 py-3 border-b border-line">
                <Icon name="file" size={15} className="text-subtle" />
                <p className="text-xs font-semibold text-muted">เอกสารที่ต้องเซ็น</p>
              </div>
              {pdfObjectUrl ? (
                <iframe src={pdfObjectUrl} className="w-full border-0" style={{ height: "60vh" }} title="เอกสาร" />
              ) : pdfError ? (
                <div className="p-4"><ErrorState code="pdf_preview_failed" /></div>
              ) : (
                <div className="flex items-center justify-center text-brand" style={{ height: "60vh" }}><Spinner size="md" /></div>
              )}
            </div>
            <Button onClick={() => setStage("signing")} size="lg" block>
              <Icon name="pencil-square" size={18} />
              ดำเนินการเซ็น
            </Button>
          </>
        )}

        {/* Stage: signing */}
        {stage === "signing" && (
          <div className="bg-surface border border-line rounded-xl shadow-card p-5 flex flex-col gap-4">
            <div>
              <p className="text-sm font-semibold text-ink mb-0.5">วาดลายเซ็น</p>
              <p className="text-xs text-subtle">ลายเซ็นของคุณจะถูกบันทึกอย่างปลอดภัย</p>
            </div>
            <SignaturePad onSign={handleSign} />
            <Button onClick={() => setStage("view")} variant="outline" block>
              <Icon name="arrow-left" size={15} />
              ย้อนกลับ
            </Button>
          </div>
        )}

        {/* Stage: preview */}
        {stage === "preview" && (
          <div className="flex flex-col gap-4">
            <div className="bg-surface border border-line rounded-xl shadow-card p-4 flex flex-col gap-3">
              <div className="flex items-center gap-2">
                <Icon name="check-circle" size={18} className="text-success-fg" />
                <p className="text-sm font-semibold text-ink">ลายเซ็นพร้อมส่ง</p>
              </div>
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={signing.signatureHash} alt="ตัวอย่างลายเซ็น"
                className="w-full max-h-28 object-contain bg-surface-muted rounded-lg border border-line" />
            </div>

            <div className="bg-surface border border-warning/40 rounded-xl p-4">
              <div className="flex items-start gap-2 mb-3">
                <Icon name="shield" size={16} className="text-brand-700 flex-shrink-0 mt-0.5" />
                <p className="text-xs font-semibold text-ink">ข้อตกลงลายมือชื่ออิเล็กทรอนิกส์</p>
              </div>
              <p className="text-xs text-muted leading-relaxed">{CONSENT_TEXT}</p>
              <label className="flex items-start gap-2 mt-3 cursor-pointer">
                <input type="checkbox" checked={signing.consentAgreed}
                  onChange={(e) => setSigning((s) => ({ ...s, consentAgreed: e.target.checked }))}
                  className="mt-0.5 w-4 h-4 rounded border-line-strong accent-brand-600" />
                <span className="text-sm text-ink font-medium">ฉันยอมรับเงื่อนไขและให้ความยินยอม</span>
              </label>
            </div>

            <div className="flex gap-3">
              <Button onClick={() => setStage("signing")} variant="outline" block>
                <Icon name="pencil-square" size={15} />
                แก้ไขลายเซ็น
              </Button>
              <Button onClick={handleSubmit} disabled={!signing.consentAgreed} block>
                ยืนยันการเซ็น
              </Button>
            </div>
          </div>
        )}

        {/* Stage: submitting */}
        {stage === "submitting" && (
          <div className="flex flex-col items-center justify-center py-16 gap-4 text-brand">
            <Spinner size="lg" />
            <p className="text-sm text-muted">กำลังบันทึกลายเซ็น...</p>
          </div>
        )}
      </div>

      {/* Legal footer */}
      {stage !== "submitting" && (
        <div className="max-w-lg mx-auto w-full px-4 pb-6">
          <div className="flex items-center justify-center gap-1.5 text-xs text-subtle">
            <Icon name="lock-closed" size={12} />
            <span>เชื่อมต่อปลอดภัย · ลายเซ็นอิเล็กทรอนิกส์ตาม พ.ร.บ. 2544</span>
          </div>
        </div>
      )}
    </div>
  );
}

const CONSENT_TEXT =
  "เอกสารนี้จะได้รับการลงลายมือชื่ออิเล็กทรอนิกส์ตามพระราชบัญญัติว่าด้วยธุรกรรมทางอิเล็กทรอนิกส์ พ.ศ. 2544 และที่แก้ไขเพิ่มเติม ลายมือชื่ออิเล็กทรอนิกส์มีผลทางกฎหมายเทียบเท่าลายมือชื่อลายลักษณ์อักษร " +
  "This document will be electronically signed in accordance with the Electronic Transactions Act B.E. 2544 (2001) and its amendments. The electronic signature is legally binding.";
