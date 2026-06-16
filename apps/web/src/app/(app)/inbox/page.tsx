"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type Task } from "@/lib/api";
import { getAccessToken } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";

export default function InboxPage() {
  const router = useRouter();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const pageSize = 20;

  const load = useCallback(async (p: number) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    setLoading(true);
    setError(null);
    try {
      const result = await api.inbox(token, p, pageSize);
      if (!result.success) {
        if (result.error.code === "unauthorized") { router.replace("/login"); return; }
        setError(result.error.code);
        return;
      }
      setTasks(result.data ?? []);
      setTotal(result.meta?.total ?? 0);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => { load(page); }, [load, page]);

  const totalPages = Math.ceil(total / pageSize);

  return (
    <main className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-0 z-10">
        <h1 className="text-lg font-bold text-gray-900">กล่องเอกสารรอเซ็น</h1>
        {!loading && <p className="text-xs text-gray-500">{total} เอกสาร</p>}
      </header>

      <div className="max-w-lg mx-auto px-4 py-4">
        {loading && (
          <div className="flex items-center justify-center py-16">
            <div className="w-8 h-8 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
          </div>
        )}

        {!loading && error && (
          <ErrorState code={error} onRetry={() => load(page)} />
        )}

        {!loading && !error && tasks.length === 0 && (
          <ErrorState code="no_pending_documents" />
        )}

        {!loading && !error && tasks.length > 0 && (
          <div className="flex flex-col gap-3">
            {tasks.map((task) => (
              <button
                key={task.id}
                onClick={() => router.push(`/documents/${task.document_id}?taskId=${task.id}`)}
                className="w-full text-left bg-white border border-gray-200 rounded-xl p-4 shadow-sm active:scale-[0.98] transition-transform"
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="flex-1 min-w-0">
                    <p className="font-semibold text-gray-900 truncate">
                      {task.doc_format_code} — {task.doc_no}
                    </p>
                    <p className="text-sm text-gray-500 mt-0.5">
                      ขั้นที่ {task.sequence_no}
                      {task.condition_type === 2 && " (ต้องเซ็นทุกคน)"}
                      {task.condition_type === 1 && " (คนใดคนหนึ่ง)"}
                    </p>
                    {task.amount && (
                      <p className="text-sm font-medium text-blue-700 mt-1">
                        ฿{parseFloat(task.amount).toLocaleString()}
                      </p>
                    )}
                  </div>
                  <span className="text-xs bg-amber-100 text-amber-700 px-2 py-1 rounded-full flex-shrink-0">
                    รอเซ็น
                  </span>
                </div>
                {task.doc_date && (
                  <p className="text-xs text-gray-400 mt-2">{task.doc_date}</p>
                )}
              </button>
            ))}
          </div>
        )}

        {totalPages > 1 && (
          <div className="flex justify-center gap-4 mt-6">
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
