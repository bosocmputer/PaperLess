"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";
import { api, type DocumentRow } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Card, CardButton, Input, Spinner, StatusBadge } from "@/components/ui";

interface ImportForm {
  file: File | null;
  doc_no: string;
  doc_format_code: string;
  doc_date: string;
  amount: string;
}

const IMPORT_FORM_INIT: ImportForm = {
  file: null,
  doc_no: "",
  doc_format_code: "POP",
  doc_date: "",
  amount: "",
};

// Document status labels for the filter dropdown — pinned to documents.status
// CHECK (0001_init.up.sql): imported,pending,rejected,completed,cancelled.
// Row badges use the shared <StatusBadge kind="document"> (same wording).
const DOC_STATUS_LABELS: Record<string, string> = {
  imported:  "นำเข้าแล้ว",
  pending:   "รอเซ็น",
  rejected:  "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
};

const DOC_STATUS_OPTIONS = ["", "imported", "pending", "rejected", "completed", "cancelled"];

function isAdminRole(roles: string[]): boolean {
  return roles.some((r) => ["document_admin", "system_admin", "auditor"].includes(r));
}

const selectClass =
  "border border-line-strong rounded-md px-3 h-11 text-sm bg-surface text-ink " +
  "focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400";

export default function AdminDocumentsPage() {
  const router = useRouter();
  const [docs, setDocs] = useState<DocumentRow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 20;

  const [status, setStatus] = useState("");
  const [docFormatCode, setDocFormatCode] = useState("");
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");

  // Import dialog state
  const [showImport, setShowImport] = useState(false);
  const [importForm, setImportForm] = useState<ImportForm>(IMPORT_FORM_INIT);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [importSuccess, setImportSuccess] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);

  const handleImport = async () => {
    if (!importForm.file || !importForm.doc_no.trim() || !importForm.doc_format_code.trim()) return;
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    setImporting(true);
    setImportError(null);
    setImportSuccess(null);
    try {
      const result = await api.importDocument(token, {
        file: importForm.file,
        doc_no: importForm.doc_no.trim(),
        doc_format_code: importForm.doc_format_code.trim().toUpperCase(),
        doc_date: importForm.doc_date || undefined,
        amount: importForm.amount || undefined,
      });
      if (!result.success) {
        if (result.error.code === "revision_conflict") {
          setImportError("เลขเอกสารนี้มีอยู่แล้วด้วยไฟล์อื่น (409)");
        } else if (result.error.code === "invalid_request") {
          setImportError("ข้อมูลไม่ถูกต้อง: " + result.error.message);
        } else {
          setImportError(result.error.message || result.error.code);
        }
        return;
      }
      const msg = result.data.duplicate ? "เอกสารนี้มีอยู่แล้ว (นำเข้าซ้ำ)" : "นำเข้าสำเร็จ";
      setImportSuccess(msg);
      setShowImport(false);
      setImportForm(IMPORT_FORM_INIT);
      load(1, status, docFormatCode, q);
      setPage(1);
      // brief toast — auto-clear after 4s
      setTimeout(() => setImportSuccess(null), 4000);
    } catch {
      setImportError("เกิดข้อผิดพลาดในการเชื่อมต่อ");
    } finally {
      setImporting(false);
    }
  };

  const closeImport = () => {
    setShowImport(false);
    setImportForm(IMPORT_FORM_INIT);
    setImportError(null);
  };

  // Debounce search input
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async (p: number, st: string, fmt: string, search: string) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    const user = getUser<{ roles: string[] }>();
    if (!isAdminRole(user?.roles ?? [])) {
      router.replace("/inbox");
      return;
    }

    setLoading(true);
    setError(null);
    try {
      const result = await api.listDocuments(token, {
        page: p,
        size: pageSize,
        status: st || undefined,
        doc_format_code: fmt || undefined,
        q: search || undefined,
      });
      if (!result.success) {
        if (result.error.code === "unauthorized") { router.replace("/login"); return; }
        setError(result.error.code);
        return;
      }
      setDocs(result.data ?? []);
      setTotal(result.meta?.total ?? 0);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => {
    load(page, status, docFormatCode, q);
  }, [load, page, status, docFormatCode, q]);

  const handleSearch = (val: string) => {
    setQInput(val);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      setPage(1);
      setQ(val);
    }, 400);
  };

  const handleStatusChange = (val: string) => {
    setStatus(val);
    setPage(1);
  };

  const totalPages = Math.ceil(total / pageSize);

  return (
    <main className="min-h-screen">
      {/* Success toast */}
      {importSuccess && (
        <div className="fixed top-4 left-1/2 -translate-x-1/2 z-50 bg-success text-white text-sm px-4 py-2 rounded-md shadow-pop">
          {importSuccess}
        </div>
      )}

      <header className="bg-surface border-b border-line px-4 py-4 sticky top-12 z-10">
        <div className="max-w-3xl mx-auto flex items-center justify-between gap-2">
          <div>
            <h1 className="text-lg font-bold text-ink">เอกสารทั้งหมด</h1>
            {!loading && <p className="text-sm text-muted">{total} รายการ</p>}
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => { setImportError(null); setImportForm(IMPORT_FORM_INIT); setShowImport(true); }}
          >
            นำเข้าเอกสาร
          </Button>
        </div>
      </header>

      {/* Import dialog */}
      {showImport && (
        <div className="fixed inset-0 z-40 flex items-center justify-center bg-black/40 px-4">
          <div className="bg-surface rounded-2xl shadow-pop w-full max-w-md p-6 flex flex-col gap-4 max-h-[90vh] overflow-y-auto">
            <h2 className="text-base font-bold text-ink">นำเข้าเอกสาร</h2>

            {/* File */}
            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">ไฟล์ PDF *</label>
              <input
                ref={fileInputRef}
                type="file"
                accept="application/pdf"
                title="เลือกไฟล์ PDF"
                className="text-sm text-ink border border-line-strong rounded-md px-3 py-2 focus:outline-none focus:ring-2 focus:ring-offset-1 file:mr-3 file:rounded file:border-0 file:bg-surface-muted file:px-3 file:py-1 file:text-ink"
                onChange={(e) => setImportForm((f) => ({ ...f, file: e.target.files?.[0] ?? null }))}
              />
            </div>

            <Input
              label="เลขเอกสาร *"
              type="text"
              value={importForm.doc_no}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_no: e.target.value }))}
              placeholder="เช่น PO26060001"
            />

            <Input
              label="รูปแบบเอกสาร *"
              type="text"
              value={importForm.doc_format_code}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_format_code: e.target.value.trim().toUpperCase() }))}
              placeholder="POP"
            />

            <Input
              label="วันที่เอกสาร (ไม่บังคับ)"
              type="date"
              title="วันที่เอกสาร"
              value={importForm.doc_date}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_date: e.target.value }))}
            />

            <Input
              label="จำนวนเงิน (ไม่บังคับ)"
              type="text"
              value={importForm.amount}
              onChange={(e) => setImportForm((f) => ({ ...f, amount: e.target.value }))}
              placeholder="เช่น 15000.00"
            />

            {/* Error */}
            {importError && (
              <p className="text-sm text-danger-fg bg-danger-bg border border-danger/30 rounded-md px-3 py-2">{importError}</p>
            )}

            {/* Actions */}
            <div className="flex justify-end gap-2 mt-1">
              <Button variant="outline" onClick={closeImport} disabled={importing}>ยกเลิก</Button>
              <Button
                onClick={handleImport}
                loading={importing}
                disabled={!importForm.file || !importForm.doc_no.trim() || !importForm.doc_format_code.trim()}
              >
                {importing ? "กำลังอัปโหลด..." : "อัปโหลด"}
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-3">
        {/* Filters */}
        <Card padding="sm" className="flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
          <input
            value={qInput}
            onChange={(e) => handleSearch(e.target.value)}
            placeholder="ค้นหาเลขเอกสาร..."
            className="flex-1 border border-line-strong rounded-md px-3 h-11 text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
          />
          <input
            value={docFormatCode}
            onChange={(e) => { setDocFormatCode(e.target.value.trim().toUpperCase()); setPage(1); }}
            placeholder="รูปแบบ (POP...)"
            className="sm:w-32 border border-line-strong rounded-md px-3 h-11 text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
          />
          <select
            value={status}
            onChange={(e) => handleStatusChange(e.target.value)}
            className={selectClass}
          >
            {DOC_STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>{s ? DOC_STATUS_LABELS[s] : "— สถานะทั้งหมด —"}</option>
            ))}
          </select>
        </Card>

        {/* Loading */}
        {loading && (
          <div className="flex justify-center py-16 text-brand">
            <Spinner size="md" />
          </div>
        )}

        {/* Error */}
        {!loading && error && (
          <ErrorState code={error} onRetry={() => load(page, status, docFormatCode, q)} />
        )}

        {/* Empty */}
        {!loading && !error && docs.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-subtle">
            <p className="text-sm">ไม่พบเอกสาร</p>
          </div>
        )}

        {/* List */}
        {!loading && !error && docs.length > 0 && (
          <div className="flex flex-col gap-2">
            {docs.map((doc) => (
              <CardButton key={doc.id} onClick={() => router.push(`/admin/documents/${doc.id}`)}>
                <div className="flex items-start justify-between gap-3">
                  <div className="flex-1 min-w-0">
                    <p className="font-semibold text-ink truncate">
                      {doc.doc_format_code} — {doc.doc_no}
                    </p>
                    <p className="text-xs text-muted mt-0.5">
                      เวอร์ชัน {doc.workflow_version}
                      {doc.revision > 0 && ` · ครั้งที่ ${doc.revision + 1}`}
                    </p>
                    {doc.amount && (
                      <p className="text-sm font-semibold text-brand-700 mt-1">
                        ฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}
                      </p>
                    )}
                  </div>
                  <StatusBadge kind="document" status={doc.status} />
                </div>
                {doc.doc_date && (
                  <p className="text-xs text-subtle mt-2">{doc.doc_date}</p>
                )}
              </CardButton>
            ))}
          </div>
        )}

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex items-center justify-center gap-3 mt-2">
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>
              ← ก่อนหน้า
            </Button>
            <span className="text-sm text-muted tabular-nums">{page}/{totalPages}</span>
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={page === totalPages}>
              ถัดไป →
            </Button>
          </div>
        )}
      </div>
    </main>
  );
}
