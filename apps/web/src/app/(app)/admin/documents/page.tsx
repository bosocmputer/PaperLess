"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";
import { api, type DocumentRow } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Icon, Input, Spinner, StatusBadge } from "@/components/ui";

interface ImportForm {
  file: File | null;
  doc_no: string;
  doc_format_code: string;
  doc_date: string;
  amount: string;
}

const IMPORT_INIT: ImportForm = { file: null, doc_no: "", doc_format_code: "POP", doc_date: "", amount: "" };

const DOC_STATUS_LABELS: Record<string, string> = {
  imported: "นำเข้าแล้ว",
  pending: "รอเซ็น",
  rejected: "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
};

const STATUS_OPTIONS = ["", "imported", "pending", "rejected", "completed", "cancelled"];

function isAdminRole(roles: string[]): boolean {
  return roles.some((r) => ["document_admin", "system_admin", "auditor"].includes(r));
}

const selectCls =
  "h-10 px-3 rounded-lg border border-line-strong bg-surface text-sm text-ink " +
  "focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400";

export default function AdminDocumentsPage() {
  const router = useRouter();
  const [docs, setDocs] = useState<DocumentRow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 20;
  const totalPages = Math.ceil(total / pageSize);

  const [status, setStatus] = useState("");
  const [docFmt, setDocFmt] = useState("");
  const [q, setQ] = useState("");
  const [qInput, setQInput] = useState("");

  const [showImport, setShowImport] = useState(false);
  const [importForm, setImportForm] = useState<ImportForm>(IMPORT_INIT);
  const [importing, setImporting] = useState(false);
  const [importError, setImportError] = useState<string | null>(null);
  const [importSuccess, setImportSuccess] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement | null>(null);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const load = useCallback(async (p: number, st: string, fmt: string, search: string) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    if (!isAdminRole(getUser<{ roles: string[] }>()?.roles ?? [])) { router.replace("/inbox"); return; }
    setLoading(true);
    setError(null);
    try {
      const result = await api.listDocuments(token, { page: p, size: pageSize, status: st || undefined, doc_format_code: fmt || undefined, q: search || undefined });
      if (!result.success) {
        if (result.error.code === "unauthorized") { router.replace("/login"); return; }
        setError(result.error.code); return;
      }
      setDocs(result.data ?? []);
      setTotal(result.meta?.total ?? 0);
    } catch { setError("network_error"); } finally { setLoading(false); }
  }, [router]);

  useEffect(() => { load(page, status, docFmt, q); }, [load, page, status, docFmt, q]);

  const handleSearch = (val: string) => {
    setQInput(val);
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => { setPage(1); setQ(val); }, 400);
  };

  const handleImport = async () => {
    if (!importForm.file || !importForm.doc_no.trim() || !importForm.doc_format_code.trim()) return;
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    setImporting(true); setImportError(null); setImportSuccess(null);
    try {
      const result = await api.importDocument(token, {
        file: importForm.file,
        doc_no: importForm.doc_no.trim(),
        doc_format_code: importForm.doc_format_code.trim().toUpperCase(),
        doc_date: importForm.doc_date || undefined,
        amount: importForm.amount || undefined,
      });
      if (!result.success) {
        setImportError(
          result.error.code === "revision_conflict" ? "เลขเอกสารนี้มีอยู่แล้วด้วยไฟล์อื่น" :
          result.error.message || result.error.code
        ); return;
      }
      setImportSuccess(result.data.duplicate ? "เอกสารนี้มีอยู่แล้ว (นำเข้าซ้ำ)" : "นำเข้าสำเร็จ");
      setShowImport(false);
      setImportForm(IMPORT_INIT);
      load(1, status, docFmt, q);
      setPage(1);
      setTimeout(() => setImportSuccess(null), 4000);
    } catch { setImportError("เกิดข้อผิดพลาดในการเชื่อมต่อ"); } finally { setImporting(false); }
  };

  return (
    <div className="min-h-screen">
      {/* Toast */}
      {importSuccess && (
        <div className="fixed top-4 left-1/2 -translate-x-1/2 z-50 flex items-center gap-2 bg-success text-white text-sm px-4 py-2.5 rounded-xl shadow-pop">
          <Icon name="check" size={16} />
          {importSuccess}
        </div>
      )}

      {/* Page header */}
      <div className="bg-surface border-b border-line px-6 py-4 sticky top-14 lg:top-0 z-10">
        <div className="max-w-6xl mx-auto flex items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-bold text-ink">เอกสารทั้งหมด</h1>
            {!loading && <p className="text-sm text-muted mt-0.5">{total.toLocaleString()} รายการ</p>}
          </div>
          <Button
            size="sm"
            onClick={() => { setImportError(null); setImportForm(IMPORT_INIT); setShowImport(true); }}
          >
            <Icon name="upload" size={15} />
            นำเข้าเอกสาร
          </Button>
        </div>
      </div>

      {/* Import modal */}
      {showImport && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
          <div className="bg-surface rounded-2xl shadow-pop w-full max-w-md p-6 flex flex-col gap-4 max-h-[90vh] overflow-y-auto">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-bold text-ink">นำเข้าเอกสาร</h2>
              <button onClick={() => setShowImport(false)} className="text-subtle hover:text-ink touch-target -mr-2 px-2 flex items-center">
                <Icon name="x" size={20} />
              </button>
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">ไฟล์ PDF *</label>
              <input
                ref={fileRef}
                type="file"
                accept="application/pdf"
                title="เลือกไฟล์ PDF"
                className="text-sm text-ink border border-line-strong rounded-lg px-3 py-2 focus:outline-none focus:ring-2 focus:ring-offset-1 file:mr-3 file:rounded-md file:border-0 file:bg-surface-muted file:px-3 file:py-1 file:text-sm file:text-ink"
                onChange={(e) => setImportForm((f) => ({ ...f, file: e.target.files?.[0] ?? null }))}
              />
            </div>

            <Input label="เลขเอกสาร *" value={importForm.doc_no}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_no: e.target.value }))}
              placeholder="เช่น PO26060001" />
            <Input label="รูปแบบเอกสาร *" value={importForm.doc_format_code}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_format_code: e.target.value.trim().toUpperCase() }))}
              placeholder="POP" />
            <Input label="วันที่เอกสาร (ไม่บังคับ)" type="date" title="วันที่เอกสาร"
              value={importForm.doc_date}
              onChange={(e) => setImportForm((f) => ({ ...f, doc_date: e.target.value }))} />
            <Input label="จำนวนเงิน (ไม่บังคับ)" value={importForm.amount}
              onChange={(e) => setImportForm((f) => ({ ...f, amount: e.target.value }))}
              placeholder="เช่น 15000.00" />

            {importError && (
              <div className="flex items-start gap-2 bg-danger-bg border border-danger/30 rounded-lg px-3 py-2.5">
                <Icon name="exclamation-triangle" size={16} className="text-danger-fg flex-shrink-0 mt-0.5" />
                <p className="text-sm text-danger-fg">{importError}</p>
              </div>
            )}

            <div className="flex gap-2 pt-1">
              <Button variant="outline" onClick={() => setShowImport(false)} disabled={importing} block>ยกเลิก</Button>
              <Button onClick={handleImport} loading={importing}
                disabled={!importForm.file || !importForm.doc_no.trim() || !importForm.doc_format_code.trim()} block>
                {importing ? "กำลังอัปโหลด..." : "อัปโหลด"}
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="max-w-6xl mx-auto px-6 py-5 flex flex-col gap-4">
        {/* Filter bar */}
        <div className="flex flex-col sm:flex-row gap-3">
          <div className="relative flex-1">
            <Icon name="search" size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-subtle pointer-events-none" />
            <input
              value={qInput}
              onChange={(e) => handleSearch(e.target.value)}
              placeholder="ค้นหาเลขเอกสาร..."
              className="w-full h-10 pl-9 pr-3 rounded-lg border border-line-strong text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
            />
          </div>
          <input
            value={docFmt}
            onChange={(e) => { setDocFmt(e.target.value.trim().toUpperCase()); setPage(1); }}
            placeholder="รูปแบบ (POP...)"
            className="sm:w-36 h-10 px-3 rounded-lg border border-line-strong text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
          />
          <select value={status} onChange={(e) => { setStatus(e.target.value); setPage(1); }} className={selectCls}>
            {STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>{s ? DOC_STATUS_LABELS[s] : "— สถานะทั้งหมด —"}</option>
            ))}
          </select>
        </div>

        {/* Loading */}
        {loading && (
          <div className="flex justify-center py-20 text-brand">
            <Spinner size="md" />
          </div>
        )}

        {/* Error */}
        {!loading && error && <ErrorState code={error} onRetry={() => load(page, status, docFmt, q)} />}

        {/* Empty */}
        {!loading && !error && docs.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-subtle gap-3">
            <Icon name="file" size={40} className="opacity-30" />
            <p className="text-sm">ไม่พบเอกสาร</p>
          </div>
        )}

        {/* Table */}
        {!loading && !error && docs.length > 0 && (
          <div className="bg-surface border border-line rounded-xl shadow-card overflow-hidden">
            {/* Desktop table */}
            <div className="hidden md:block overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-line bg-surface-muted">
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">เลขเอกสาร</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">รูปแบบ</th>
                    <th className="text-right px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">จำนวนเงิน</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">วันที่</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">สถานะ</th>
                    <th className="px-4 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {docs.map((doc) => (
                    <tr
                      key={doc.id}
                      onClick={() => router.push(`/admin/documents/${doc.id}`)}
                      className="hover:bg-surface-muted cursor-pointer transition-colors"
                    >
                      <td className="px-4 py-3.5">
                        <p className="font-semibold text-ink">{doc.doc_no}</p>
                        {doc.revision > 0 && (
                          <p className="text-xs text-subtle mt-0.5">ครั้งที่ {doc.revision + 1}</p>
                        )}
                      </td>
                      <td className="px-4 py-3.5">
                        <span className="inline-block text-xs font-bold text-brand-700 bg-brand-50 rounded-md px-2 py-0.5">
                          {doc.doc_format_code}
                        </span>
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        {doc.amount
                          ? <span className="font-semibold text-ink">฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}</span>
                          : <span className="text-subtle">—</span>
                        }
                      </td>
                      <td className="px-4 py-3.5 text-muted">{doc.doc_date || "—"}</td>
                      <td className="px-4 py-3.5">
                        <StatusBadge kind="document" status={doc.status} />
                      </td>
                      <td className="px-4 py-3.5 text-subtle">
                        <Icon name="chevron-right" size={16} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Mobile cards */}
            <div className="md:hidden divide-y divide-line">
              {docs.map((doc) => (
                <button
                  key={doc.id}
                  type="button"
                  onClick={() => router.push(`/admin/documents/${doc.id}`)}
                  className="w-full text-left px-4 py-4 hover:bg-surface-muted transition-colors"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 mb-1">
                        <span className="text-[11px] font-bold text-brand-700 bg-brand-50 rounded px-1.5 py-0.5">{doc.doc_format_code}</span>
                      </div>
                      <p className="font-semibold text-ink truncate">{doc.doc_no}</p>
                      {doc.amount && (
                        <p className="text-sm font-semibold text-brand-700 mt-1">
                          ฿{parseFloat(doc.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}
                        </p>
                      )}
                      {doc.doc_date && <p className="text-xs text-subtle mt-0.5">{doc.doc_date}</p>}
                    </div>
                    <div className="flex flex-col items-end gap-2 flex-shrink-0">
                      <StatusBadge kind="document" status={doc.status} />
                      <Icon name="chevron-right" size={16} className="text-subtle" />
                    </div>
                  </div>
                </button>
              ))}
            </div>
          </div>
        )}

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex items-center justify-center gap-2 mt-2">
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>
              <Icon name="chevron-left" size={15} /> ก่อนหน้า
            </Button>
            <div className="flex items-center gap-1">
              {Array.from({ length: Math.min(totalPages, 7) }, (_, i) => {
                let pg: number;
                if (totalPages <= 7) pg = i + 1;
                else if (page <= 4) pg = i + 1;
                else if (page >= totalPages - 3) pg = totalPages - 6 + i;
                else pg = page - 3 + i;
                return (
                  <button
                    key={pg}
                    onClick={() => setPage(pg)}
                    className={`w-8 h-8 rounded-lg text-sm font-medium transition-colors ${
                      pg === page ? "bg-brand text-white" : "text-muted hover:bg-surface-muted"
                    }`}
                  >
                    {pg}
                  </button>
                );
              })}
            </div>
            <Button variant="outline" size="sm" onClick={() => setPage((p) => Math.min(totalPages, p + 1))} disabled={page === totalPages}>
              ถัดไป <Icon name="chevron-right" size={15} />
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
