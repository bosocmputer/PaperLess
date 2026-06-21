"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type Task } from "@/lib/api";
import { getAccessToken } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { CardButton, StatusBadge, Spinner, Button } from "@/components/ui";

// Short Thai hint for the step condition (mirrors domain.md condition_type).
function conditionHint(type: number): string | null {
  switch (type) {
    case 1: return "คนใดคนหนึ่งเซ็น";
    case 2: return "ต้องเซ็นทุกคน";
    case 3: return "ผู้เซ็นภายนอก";
    default: return null;
  }
}

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
    <main className="min-h-screen">
      <header className="bg-surface border-b border-line px-4 py-4 sticky top-0 z-10">
        <div className="max-w-lg mx-auto">
          <h1 className="text-lg font-bold text-ink">กล่องเอกสารรอเซ็น</h1>
          {!loading && !error && (
            <p className="text-sm text-muted">{total} รายการ</p>
          )}
        </div>
      </header>

      <div className="max-w-lg mx-auto px-4 py-4">
        {loading && (
          <div className="flex items-center justify-center py-16 text-brand">
            <Spinner size="md" />
          </div>
        )}

        {!loading && error && (
          <ErrorState code={error} onRetry={() => load(page)} />
        )}

        {!loading && !error && tasks.length === 0 && (
          <ErrorState code="no_pending_documents" />
        )}

        {!loading && !error && tasks.length > 0 && (
          <ul className="flex flex-col gap-3">
            {tasks.map((task) => {
              const hint = conditionHint(task.condition_type);
              return (
                <li key={task.id}>
                  <CardButton
                    onClick={() => router.push(`/documents/${task.document_id}?taskId=${task.id}`)}
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex-1 min-w-0">
                        <span className="inline-block text-[11px] font-semibold tracking-wide text-muted bg-surface-muted rounded px-1.5 py-0.5">
                          {task.doc_format_code}
                        </span>
                        <p className="font-semibold text-ink truncate mt-1.5">
                          {task.doc_no}
                        </p>
                        <p className="text-sm text-muted mt-0.5">
                          ขั้นที่ {task.sequence_no}
                          {hint && ` · ${hint}`}
                        </p>
                        {task.amount && (
                          <p className="text-base font-semibold text-brand-700 mt-1.5">
                            ฿{parseFloat(task.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}
                          </p>
                        )}
                      </div>
                      <StatusBadge kind="task" status={task.status} />
                    </div>
                    {task.doc_date && (
                      <p className="text-xs text-subtle mt-2">{task.doc_date}</p>
                    )}
                  </CardButton>
                </li>
              );
            })}
          </ul>
        )}

        {totalPages > 1 && (
          <div className="flex items-center justify-center gap-3 mt-6">
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page === 1}
            >
              ← ก่อนหน้า
            </Button>
            <span className="text-sm text-muted tabular-nums">{page}/{totalPages}</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
            >
              ถัดไป →
            </Button>
          </div>
        )}
      </div>
    </main>
  );
}
