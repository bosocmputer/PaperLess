"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type DashboardStats } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Card, Spinner } from "@/components/ui";
import { cn } from "@/lib/cn";

const ADMIN_ROLES = ["document_admin", "system_admin", "auditor"];
const isAdmin = (roles: string[]) => roles.some((r) => ADMIN_ROLES.includes(r));

// Headline tiles: label, value source, tone.
type Tone = "warning" | "success" | "danger" | "info" | "neutral";
const toneClass: Record<Tone, string> = {
  warning: "bg-warning-bg text-warning-fg",
  success: "bg-success-bg text-success-fg",
  danger: "bg-danger-bg text-danger-fg",
  info: "bg-info-bg text-info-fg",
  neutral: "bg-surface-muted text-muted",
};

export default function DashboardPage() {
  const router = useRouter();
  const [stats, setStats] = useState<DashboardStats | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    if (!isAdmin(getUser<{ roles: string[] }>()?.roles ?? [])) { router.replace("/inbox"); return; }
    setLoading(true);
    setError(null);
    try {
      const res = await api.dashboardStats(token);
      if (!res.success) { setError(res.error.code); return; }
      setStats(res.data);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => { load(); }, [load]);

  const tiles: { label: string; value: number; tone: Tone }[] = stats ? [
    { label: "รอเซ็น", value: stats.by_status.pending ?? 0, tone: "warning" },
    { label: "เสร็จสิ้น", value: stats.by_status.completed ?? 0, tone: "success" },
    { label: "ส่งคืน", value: stats.by_status.rejected ?? 0, tone: "danger" },
    { label: "ซิงก์ล้มเหลว", value: stats.by_sync.sync_failed ?? 0, tone: "danger" },
  ] : [];

  return (
    <main className="min-h-screen">
      <header className="bg-surface border-b border-line px-4 py-4 sticky top-12 z-10">
        <div className="max-w-3xl mx-auto">
          <h1 className="text-lg font-bold text-ink">แดชบอร์ด</h1>
          {stats && <p className="text-sm text-muted">เอกสารทั้งหมด {stats.total} ฉบับ</p>}
        </div>
      </header>

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-4">
        {loading && <div className="flex justify-center py-16 text-brand"><Spinner size="md" /></div>}
        {!loading && error && <ErrorState code={error} onRetry={load} />}

        {!loading && !error && stats && (
          <>
            {/* Headline tiles */}
            <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
              {tiles.map((t) => (
                <div key={t.label} className={cn("rounded-lg p-4 flex flex-col gap-1", toneClass[t.tone])}>
                  <span className="text-3xl font-bold tabular-nums">{t.value}</span>
                  <span className="text-sm font-medium">{t.label}</span>
                </div>
              ))}
            </div>

            {/* By status breakdown */}
            <Card>
              <p className="text-sm font-semibold text-ink mb-3">แยกตามสถานะเอกสาร</p>
              <div className="flex flex-col gap-1.5">
                {Object.entries(stats.by_status).map(([k, v]) => (
                  <div key={k} className="flex items-center justify-between text-sm">
                    <span className="text-muted">{statusLabel(k)}</span>
                    <span className="text-ink font-semibold tabular-nums">{v}</span>
                  </div>
                ))}
              </div>
            </Card>

            {/* By format */}
            <Card>
              <p className="text-sm font-semibold text-ink mb-3">แยกตามรูปแบบเอกสาร</p>
              {stats.by_format.length === 0 ? (
                <p className="text-sm text-subtle">ยังไม่มีเอกสาร</p>
              ) : (
                <div className="flex flex-col gap-1.5">
                  {stats.by_format.map((f) => (
                    <div key={f.doc_format_code} className="flex items-center justify-between text-sm">
                      <span className="text-muted">{f.doc_format_code}</span>
                      <span className="text-ink font-semibold tabular-nums">{f.count}</span>
                    </div>
                  ))}
                </div>
              )}
            </Card>
          </>
        )}
      </div>
    </main>
  );
}

function statusLabel(code: string): string {
  const m: Record<string, string> = {
    imported: "นำเข้าแล้ว", pending: "รอเซ็น", rejected: "ส่งคืน",
    completed: "เสร็จสิ้น", cancelled: "ยกเลิก",
  };
  return m[code] ?? code;
}
