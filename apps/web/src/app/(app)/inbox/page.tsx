"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type Task } from "@/lib/api";
import { getAccessToken } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Icon, Spinner, StatusBadge } from "@/components/ui";

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
    <div className="min-h-screen bg-bg">
      {/* Header */}
      <div className="bg-surface border-b border-line px-4 lg:px-6 py-5 sticky top-0 z-10">
        <div className="max-w-2xl mx-auto">
          <h1 className="text-xl font-bold text-ink">กล่องเอกสารรอเซ็น</h1>
          {!loading && !error && (
            <p className="text-sm text-muted mt-0.5">
              {total > 0 ? `${total} รายการรอดำเนินการ` : "ไม่มีเอกสารที่รอเซ็น"}
            </p>
          )}
        </div>
      </div>

      <div className="max-w-2xl mx-auto px-4 lg:px-6 py-5">
        {loading && (
          <div className="flex items-center justify-center py-20 text-brand">
            <Spinner size="md" />
          </div>
        )}

        {!loading && error && (
          <ErrorState code={error} onRetry={() => load(page)} />
        )}

        {!loading && !error && tasks.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-center">
            <div className="w-16 h-16 rounded-full bg-success-bg flex items-center justify-center mb-4">
              <Icon name="check-circle" size={32} className="text-success" />
            </div>
            <p className="text-base font-semibold text-ink">ดำเนินการครบแล้ว</p>
            <p className="text-sm text-muted mt-1">ไม่มีเอกสารที่รอลายเซ็นของคุณ</p>
          </div>
        )}

        {!loading && !error && tasks.length > 0 && (
          <ul className="flex flex-col gap-3">
            {tasks.map((task) => {
              const hint = conditionHint(task.condition_type);
              return (
                <li key={task.id}>
                  <button
                    type="button"
                    onClick={() => router.push(`/documents/${task.document_id}?taskId=${task.id}`)}
                    className="w-full text-left bg-surface border border-line rounded-xl shadow-card hover:border-brand-300 hover:shadow-pop transition-all active:scale-[0.99] p-4"
                  >
                    <div className="flex items-start justify-between gap-3">
                      <div className="flex-1 min-w-0">
                        {/* Format badge */}
                        <span className="inline-block text-[11px] font-bold tracking-wider text-brand-700 bg-brand-50 rounded-md px-2 py-0.5 mb-2">
                          {task.doc_format_code}
                        </span>
                        {/* Doc number */}
                        <p className="font-semibold text-ink truncate text-[15px]">{task.doc_no}</p>
                        {/* Step info */}
                        <p className="text-sm text-muted mt-0.5">
                          ขั้นที่ {task.sequence_no}
                          {hint && <span className="text-subtle"> · {hint}</span>}
                        </p>
                        {/* Amount */}
                        {task.amount && (
                          <p className="text-base font-bold text-brand-700 mt-2">
                            ฿{parseFloat(task.amount).toLocaleString("th-TH", { minimumFractionDigits: 2 })}
                          </p>
                        )}
                        {/* Date */}
                        {task.doc_date && (
                          <p className="text-xs text-subtle mt-1">{task.doc_date}</p>
                        )}
                      </div>
                      <div className="flex flex-col items-end gap-2 flex-shrink-0">
                        <StatusBadge kind="task" status={task.status} />
                        <Icon name="chevron-right" size={18} className="text-subtle" />
                      </div>
                    </div>
                  </button>
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
              <Icon name="chevron-left" size={16} />
              ก่อนหน้า
            </Button>
            <span className="text-sm text-muted tabular-nums px-2">{page} / {totalPages}</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page === totalPages}
            >
              ถัดไป
              <Icon name="chevron-right" size={16} />
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
