"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type DashboardStats } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Icon, Spinner } from "@/components/ui";
import { cn } from "@/lib/cn";

const ADMIN_ROLES = ["document_admin", "system_admin", "auditor"];
const isAdmin = (roles: string[]) => roles.some((r) => ADMIN_ROLES.includes(r));

type Tone = "warning" | "success" | "danger" | "info";

const toneConfig: Record<Tone, { bg: string; fg: string; iconBg: string; icon: "clock" | "check-circle" | "x-circle" | "exclamation-triangle" | "information-circle" }> = {
  warning: { bg: "bg-warning-bg", fg: "text-warning-fg", iconBg: "bg-warning/20", icon: "clock" },
  success: { bg: "bg-success-bg", fg: "text-success-fg", iconBg: "bg-success/20", icon: "check-circle" },
  danger: { bg: "bg-danger-bg", fg: "text-danger-fg", iconBg: "bg-danger/20", icon: "x-circle" },
  info: { bg: "bg-info-bg", fg: "text-info-fg", iconBg: "bg-info/20", icon: "information-circle" },
};

const STATUS_LABELS: Record<string, string> = {
  imported: "นำเข้าแล้ว",
  pending: "รอเซ็น",
  rejected: "ส่งคืน",
  completed: "เสร็จสิ้น",
  cancelled: "ยกเลิก",
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

  const tiles: { label: string; value: number; tone: Tone; sub: string }[] = stats ? [
    { label: "รอเซ็น", value: stats.by_status.pending ?? 0, tone: "warning", sub: "เอกสารรอดำเนินการ" },
    { label: "เสร็จสิ้น", value: stats.by_status.completed ?? 0, tone: "success", sub: "เซ็นครบแล้ว" },
    { label: "ส่งคืน", value: stats.by_status.rejected ?? 0, tone: "danger", sub: "ถูกปฏิเสธ" },
    { label: "ซิงก์ล้มเหลว", value: stats.by_sync?.sync_failed ?? 0, tone: "danger", sub: "ต้องตรวจสอบ" },
  ] : [];

  if (loading) {
    return (
      <div className="flex-1 flex items-center justify-center py-24 text-brand">
        <Spinner size="md" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center p-8">
        <ErrorState code={error} onRetry={load} />
      </div>
    );
  }

  return (
    <div className="min-h-screen">
      {/* Page header */}
      <div className="bg-surface border-b border-line px-6 py-5 sticky top-14 lg:top-0 z-10">
        <div className="max-w-6xl mx-auto flex items-center justify-between">
          <div>
            <h1 className="text-xl font-bold text-ink">แดชบอร์ด</h1>
            {stats && (
              <p className="text-sm text-muted mt-0.5">เอกสารทั้งหมด <span className="font-semibold text-ink">{stats.total}</span> ฉบับ</p>
            )}
          </div>
          <button
            onClick={() => router.push("/admin/documents")}
            className="text-sm font-medium text-brand-600 hover:text-brand-700 flex items-center gap-1"
          >
            ดูเอกสารทั้งหมด
            <Icon name="chevron-right" size={16} />
          </button>
        </div>
      </div>

      <div className="max-w-6xl mx-auto px-6 py-6 space-y-6">

        {/* Stat tiles */}
        {stats && (
          <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
            {tiles.map((t) => {
              const cfg = toneConfig[t.tone];
              return (
                <div key={t.label} className={cn("rounded-xl p-5 flex flex-col gap-3", cfg.bg)}>
                  <div className={cn("w-10 h-10 rounded-lg flex items-center justify-center", cfg.iconBg)}>
                    <Icon name={cfg.icon} size={20} className={cfg.fg} />
                  </div>
                  <div>
                    <p className={cn("text-3xl font-bold tabular-nums leading-none", cfg.fg)}>{t.value}</p>
                    <p className={cn("text-sm font-semibold mt-1", cfg.fg)}>{t.label}</p>
                    <p className={cn("text-xs mt-0.5 opacity-70", cfg.fg)}>{t.sub}</p>
                  </div>
                </div>
              );
            })}
          </div>
        )}

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* By status breakdown */}
          {stats && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">สถานะเอกสาร</p>
              <div className="flex flex-col gap-3">
                {Object.entries(stats.by_status).map(([k, v]) => {
                  const pct = stats.total > 0 ? Math.round((v / stats.total) * 100) : 0;
                  return (
                    <div key={k}>
                      <div className="flex items-center justify-between text-sm mb-1">
                        <span className="text-muted">{STATUS_LABELS[k] ?? k}</span>
                        <span className="font-semibold text-ink tabular-nums">{v}</span>
                      </div>
                      <div className="h-1.5 bg-surface-muted rounded-full overflow-hidden">
                        <div
                          className="h-full bg-brand rounded-full transition-all"
                          style={{ width: `${pct}%` }}
                        />
                      </div>
                    </div>
                  );
                })}
              </div>
            </div>
          )}

          {/* By format */}
          {stats && (
            <div className="bg-surface rounded-xl border border-line shadow-card p-5">
              <p className="text-sm font-semibold text-ink mb-4">รูปแบบเอกสาร</p>
              {stats.by_format.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-subtle gap-2">
                  <Icon name="file" size={32} className="opacity-30" />
                  <p className="text-sm">ยังไม่มีเอกสาร</p>
                </div>
              ) : (
                <div className="flex flex-col gap-3">
                  {stats.by_format.map((f) => {
                    const pct = stats.total > 0 ? Math.round((f.count / stats.total) * 100) : 0;
                    return (
                      <div key={f.doc_format_code}>
                        <div className="flex items-center justify-between text-sm mb-1">
                          <span className="font-medium text-ink">{f.doc_format_code}</span>
                          <span className="text-muted tabular-nums">{f.count} ฉบับ</span>
                        </div>
                        <div className="h-1.5 bg-surface-muted rounded-full overflow-hidden">
                          <div
                            className="h-full bg-brand-400 rounded-full transition-all"
                            style={{ width: `${pct}%` }}
                          />
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          )}
        </div>

        {/* Placeholder: Activity feed */}
        <div className="bg-surface rounded-xl border border-line shadow-card p-5">
          <div className="flex items-center justify-between mb-4">
            <p className="text-sm font-semibold text-ink">กิจกรรมล่าสุด</p>
            <span className="text-xs text-subtle bg-surface-muted px-2 py-1 rounded-md">เร็ว ๆ นี้</span>
          </div>
          <div className="flex flex-col gap-3">
            {[1, 2, 3].map((i) => (
              <div key={i} className="flex items-center gap-3 animate-pulse">
                <div className="w-8 h-8 rounded-full bg-surface-muted flex-shrink-0" />
                <div className="flex-1">
                  <div className="h-3 bg-surface-muted rounded w-2/3 mb-2" />
                  <div className="h-2.5 bg-surface-muted rounded w-1/3" />
                </div>
              </div>
            ))}
          </div>
          <p className="text-xs text-subtle text-center mt-4">Activity feed — จะเปิดใช้ในเวอร์ชันถัดไป</p>
        </div>

        {/* Placeholder: Chart */}
        <div className="bg-surface rounded-xl border border-line shadow-card p-5">
          <div className="flex items-center justify-between mb-4">
            <p className="text-sm font-semibold text-ink">แนวโน้มเอกสาร (รายเดือน)</p>
            <span className="text-xs text-subtle bg-surface-muted px-2 py-1 rounded-md">เร็ว ๆ นี้</span>
          </div>
          <div className="h-40 flex items-center justify-center rounded-lg bg-surface-muted">
            <div className="text-center">
              <Icon name="information-circle" size={28} className="text-subtle mx-auto mb-2" />
              <p className="text-xs text-subtle">กราฟแนวโน้ม — จะเปิดใช้ในเวอร์ชันถัดไป</p>
            </div>
          </div>
        </div>

      </div>
    </div>
  );
}
