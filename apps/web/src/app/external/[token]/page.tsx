"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { useParams } from "next/navigation";
import { api, ExternalDocView } from "@/lib/api";
import SignaturePad from "@/components/SignaturePad";
import ErrorState from "@/components/ErrorState";
import { Button, Card, Spinner } from "@/components/ui";

type Stage =
  | "loading"
  | "view"      // showing document info + PDF
  | "signing"   // showing signature pad
  | "preview"   // showing signature hash preview before submit
  | "submitting"
  | "done"
  | "error";

interface SigningState {
  signatureHash: string;
  consentAgreed: boolean;
  requestId: string;
}

export default function ExternalSignPage() {
  const params = useParams();

  // Read the raw token from the URL path ONCE. Store it in state/ref — never
  // echo it into other URLs or log it.
  const signerToken = useRef<string>(
    typeof params.token === "string" ? params.token : ""
  );

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

  // Load document view on mount.
  useEffect(() => {
    if (!token) {
      setErrorCode("external_link_invalid");
      setStage("error");
      return;
    }
    api.externalView(token).then((res) => {
      if (res.success) {
        setDocView(res.data);
        setStage("view");
        loadPdf(token);
      } else {
        setErrorCode(res.error.code);
        setStage("error");
      }
    }).catch(() => {
      setErrorCode("network_error");
      setStage("error");
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Fetch the PDF with the token in the header, create a blob URL for the iframe.
  const loadPdf = useCallback((tok: string) => {
    const url = api.externalOriginalPdfUrl();
    const headers = api.externalOriginalPdfHeaders(tok);
    fetch(url, { headers })
      .then((res) => {
        if (!res.ok) {
          setPdfError(true);
          return;
        }
        return res.blob();
      })
      .then((blob) => {
        if (!blob) return;
        setPdfObjectUrl(URL.createObjectURL(blob));
      })
      .catch(() => setPdfError(true));
  }, []);

  // Clean up blob URL on unmount.
  useEffect(() => {
    return () => {
      if (pdfObjectUrl) URL.revokeObjectURL(pdfObjectUrl);
    };
  }, [pdfObjectUrl]);

  const handleSign = useCallback((hash: string) => {
    setSigning((s) => ({ ...s, signatureHash: hash }));
    setStage("preview");
  }, []);

  const handleSubmit = async () => {
    if (!signing.consentAgreed) return;
    setStage("submitting");

    try {
      const res = await api.externalSign(
        token,
        signing.signatureHash,
        CONSENT_TEXT,
        signing.requestId
      );
      if (res.success) {
        setStage("done");
      } else {
        setErrorCode(res.error.code);
        setStage("error");
      }
    } catch {
      // Network drop during submit — show checking status message; rely on request_id.
      setErrorCode("network_error");
      setStage("error");
    }
  };

  if (stage === "loading") {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <div className="text-center text-brand">
          <Spinner size="lg" className="mx-auto mb-3" />
          <p className="text-muted text-sm">กำลังโหลด...</p>
        </div>
      </div>
    );
  }

  if (stage === "error") {
    if (errorCode === "network_error") {
      return (
        <div className="min-h-screen flex items-center justify-center px-4">
          <div className="text-center max-w-sm">
            <div className="flex items-center justify-center w-14 h-14 rounded-full bg-info-bg text-info-fg text-2xl mx-auto mb-3">⟳</div>
            <p className="text-lg font-semibold text-ink mb-2">กำลังตรวจสอบสถานะ</p>
            <p className="text-sm text-muted mb-4">
              การเชื่อมต่อขัดข้องระหว่างการส่งข้อมูล เอกสารอาจได้รับการเซ็นแล้ว
            </p>
            <Button onClick={() => window.location.reload()}>ตรวจสอบสถานะ</Button>
          </div>
        </div>
      );
    }
    return (
      <div className="min-h-screen flex items-center justify-center px-4">
        <div className="w-full max-w-sm">
          <ErrorState code={errorCode} />
        </div>
      </div>
    );
  }

  if (stage === "done") {
    return (
      <div className="min-h-screen flex items-center justify-center px-4">
        <div className="text-center max-w-sm">
          <div className="flex items-center justify-center w-16 h-16 rounded-full bg-success-bg text-success-fg text-3xl mx-auto mb-4">✓</div>
          <h1 className="text-xl font-bold text-ink mb-2">เซ็นเอกสารสำเร็จ</h1>
          <p className="text-sm text-muted">
            ลายเซ็นของคุณได้รับการบันทึกแล้ว ขอบคุณที่ดำเนินการ
          </p>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen">
      {/* Header */}
      <div className="bg-surface border-b border-line px-4 py-3 sticky top-0 z-10">
        <div className="max-w-lg mx-auto">
          <p className="text-xs font-semibold text-brand-600 uppercase tracking-widest">PaperLess</p>
          <h1 className="text-base font-semibold text-ink">
            {docView ? `${docView.doc_format_code} ${docView.doc_no}` : "เซ็นเอกสาร"}
          </h1>
          {docView && (
            <p className="text-xs text-muted">สวัสดี คุณ{docView.signer_name}</p>
          )}
        </div>
      </div>

      <div className="max-w-lg mx-auto px-4 py-4 space-y-4">

        {/* PDF Viewer */}
        {stage === "view" && (
          <>
            <Card padding="none" className="overflow-hidden">
              <div className="px-4 py-3 border-b border-line">
                <p className="text-sm font-semibold text-ink">เอกสารที่ต้องเซ็น</p>
              </div>
              {pdfObjectUrl ? (
                <iframe
                  src={pdfObjectUrl}
                  className="w-full border-0"
                  style={{ height: "60vh" }}
                  title="document preview"
                />
              ) : pdfError ? (
                <div className="p-4">
                  <ErrorState code="pdf_preview_failed" />
                </div>
              ) : (
                <div className="flex items-center justify-center text-brand" style={{ height: "60vh" }}>
                  <Spinner size="md" />
                </div>
              )}
            </Card>

            <Button onClick={() => setStage("signing")} size="lg" block>
              ดำเนินการเซ็น
            </Button>
          </>
        )}

        {/* Signature Pad */}
        {stage === "signing" && (
          <Card className="space-y-3">
            <div>
              <p className="text-sm font-semibold text-ink mb-1">วาดลายเซ็น</p>
              <p className="text-xs text-subtle">ลายเซ็นของคุณจะถูกบันทึกอย่างปลอดภัย</p>
            </div>
            <SignaturePad onSign={handleSign} />
            <Button onClick={() => setStage("view")} variant="outline" block>
              ย้อนกลับ
            </Button>
          </Card>
        )}

        {/* Preview + Consent */}
        {stage === "preview" && (
          <div className="space-y-4">
            <Card>
              <div className="flex items-center gap-2 mb-3">
                <span className="flex items-center justify-center w-7 h-7 rounded-full bg-success-bg text-success-fg text-sm">✓</span>
                <p className="text-sm font-semibold text-ink">ลายเซ็นพร้อมส่ง</p>
              </div>
              <p className="text-xs font-mono text-subtle break-all">
                {signing.signatureHash.slice(0, 32)}...
              </p>
            </Card>

            <div className="bg-warning-bg border border-warning/30 rounded-lg p-4">
              <p className="text-xs text-ink leading-relaxed">{CONSENT_TEXT}</p>
              <label className="flex items-start gap-2 mt-3 cursor-pointer">
                <input
                  type="checkbox"
                  checked={signing.consentAgreed}
                  onChange={(e) => setSigning((s) => ({ ...s, consentAgreed: e.target.checked }))}
                  className="mt-0.5 w-4 h-4 rounded border-line-strong accent-brand-600"
                />
                <span className="text-sm text-ink">
                  ฉันยอมรับเงื่อนไขและให้ความยินยอมในการลงลายมือชื่ออิเล็กทรอนิกส์
                </span>
              </label>
            </div>

            <div className="flex gap-3">
              <Button onClick={() => setStage("signing")} variant="outline" block>
                แก้ไขลายเซ็น
              </Button>
              <Button onClick={handleSubmit} disabled={!signing.consentAgreed} block>
                ยืนยันการเซ็น
              </Button>
            </div>
          </div>
        )}

        {stage === "submitting" && (
          <div className="flex flex-col items-center justify-center py-16 gap-3 text-brand">
            <Spinner size="lg" />
            <p className="text-muted text-sm">กำลังบันทึกลายเซ็น...</p>
          </div>
        )}
      </div>
    </div>
  );
}

// พ.ร.บ. 2544 consent text shown to the external signer before submission.
const CONSENT_TEXT =
  "เอกสารนี้จะได้รับการลงลายมือชื่ออิเล็กทรอนิกส์ตามพระราชบัญญัติว่าด้วยธุรกรรมทางอิเล็กทรอนิกส์ พ.ศ. 2544 และที่แก้ไขเพิ่มเติม ลายมือชื่ออิเล็กทรอนิกส์มีผลทางกฎหมายเทียบเท่าลายมือชื่อลายลักษณ์อักษร " +
  "This document will be electronically signed in accordance with the Electronic Transactions Act B.E. 2544 (2001) and its amendments. The electronic signature is legally binding.";
