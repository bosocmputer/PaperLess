"use client";

import { useEffect, useState, useCallback, useRef } from "react";
import { useRouter } from "next/navigation";
import { api, type DocumentRow } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";

// Document status labels — pinned to documents.status CHECK (0001_init.up.sql):
// imported,pending,rejected,completed,cancelled
const DOC_STATUS_LABELS: Record<string, string> = {
  imported:  "นำเข้าแล้ว",
  pending:   "รอเซ็น",
  rejected:  "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
};

const DOC_STATUS_OPTIONS = ["", "imported", "pending", "rejected", "completed", "cancelled"];

function statusBadge(status: string) {
  const colors: Record<string, string> = {
    imported:  "bg-blue-100 text-blue-700",
    pending:   "bg-amber-100 text-amber-700",
    rejected:  "bg-red-100 text-red-700",
    completed: "bg-green-100 text-green-700",
    cancelled: "bg-gray-100 text-gray-500",
  };
  return colors[status] ?? "bg-gray-100 text-gray-500";
}

function isAdminRole(roles: string[]): boolean {
  return roles.some((r) => ["document_admin", "system_admin", "auditor"].includes(r));
}

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
    <main className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-0 z-10">
        <div className="max-w-3xl mx-auto flex items-center justify-between gap-2">
          <div>
            <h1 className="text-lg font-bold text-gray-900">เอกสารทั้งหมด</h1>
            {!loading && <p className="text-xs text-gray-500">{total} รายการ</p>}
          </div>
          <button
            onClick={() => router.push("/admin/workflows")}
            className="text-sm text-blue-600 px-3 py-1.5 border border-blue-200 rounded-lg"
          >
            Workflow Templates
          </button>
        </div>
      </header>

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-3">
        {/* Filters */}
        <div className="bg-white rounded-xl border border-gray-200 p-3 flex flex-col gap-2 sm:flex-row sm:items-center sm:gap-3">
          <input
            value={qInput}
            onChange={(e) => handleSearch(e.target.value)}
            placeholder="ค้นหาเลขเอกสาร..."
            className="flex-1 border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
          />
          <input
            value={docFormatCode}
            onChange={(e) => { setDocFormatCode(e.target.value.trim().toUpperCase()); setPage(1); }}
            placeholder="รูปแบบ (POP...)"
            className="w-32 border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
          />
          <select
            value={status}
            onChange={(e) => handleStatusChange(e.target.value)}
            className="border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
          >
            {DOC_STATUS_OPTIONS.map((s) => (
              <option key={s} value={s}>{s ? DOC_STATUS_LABELS[s] : "— สถานะทั้งหมด —"}</option>
            ))}
          </select>
        </div>

        {/* Loading */}
        {loading && (
          <div className="flex justify-center py-16">
            <div className="w-8 h-8 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
          </div>
        )}

        {/* Error */}
        {!loading && error && (
          <ErrorState code={error} onRetry={() => load(page, status, docFormatCode, q)} />
        )}

        {/* Empty */}
        {!loading && !error && docs.length === 0 && (
          <div className="flex flex-col items-center justify-center py-16 text-gray-400">
            <p className="text-sm">ไม่พบเอกสาร</p>
          </div>
        )}

        {/* List */}
        {!loading && !error && docs.length > 0 && (
          <div className="flex flex-col gap-2">
            {docs.map((doc) => (
              <button
                key={doc.id}
                onClick={() => router.push(`/admin/documents/${doc.id}`)}
                className="w-full text-left bg-white border border-gray-200 rounded-xl p-4 shadow-sm active:scale-[0.98] transition-transform"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex-1 min-w-0">
                    <p className="font-semibold text-gray-900 truncate">
                      {doc.doc_format_code} — {doc.doc_no}
                    </p>
                    <p className="text-xs text-gray-500 mt-0.5">
                      เวอร์ชัน {doc.workflow_version}
                      {doc.revision > 0 && ` · ครั้งที่ ${doc.revision + 1}`}
                    </p>
                    {doc.amount && (
                      <p className="text-sm font-medium text-blue-700 mt-1">
                        ฿{parseFloat(doc.amount).toLocaleString()}
                      </p>
                    )}
                  </div>
                  <span className={`text-xs px-2 py-1 rounded-full flex-shrink-0 ${statusBadge(doc.status)}`}>
                    {DOC_STATUS_LABELS[doc.status] ?? doc.status}
                  </span>
                </div>
                {doc.doc_date && (
                  <p className="text-xs text-gray-400 mt-2">{doc.doc_date}</p>
                )}
              </button>
            ))}
          </div>
        )}

        {/* Pagination */}
        {totalPages > 1 && (
          <div className="flex justify-center gap-4 mt-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
              className="px-4 py-2 border border-gray-300 rounded-lg text-sm disabled:opacity-40"
            >
              ← ก่อนหน้า
            </button>
            <span className="px-3 py-2 text-sm text-gray-600">{page}/{totalPages}</span>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
              className="px-4 py-2 border border-gray-300 rounded-lg text-sm disabled:opacity-40"
            >
              ถัดไป →
            </button>
          </div>
        )}
      </div>
    </main>
  );
}
