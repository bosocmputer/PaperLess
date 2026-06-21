"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type TemplateRow, type TemplateDetail } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Card, Spinner, StatusBadge } from "@/components/ui";

function isWorkflowAdmin(roles: string[]): boolean {
  return roles.some((r) => ["workflow_admin", "system_admin"].includes(r));
}

type ActionState =
  | { status: "idle" }
  | { status: "loading"; id: string; action: string }
  | { status: "success"; message: string }
  | { status: "error"; message: string };

export default function AdminWorkflowsPage() {
  const router = useRouter();
  const [templates, setTemplates] = useState<TemplateRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [detail, setDetail] = useState<TemplateDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [actionState, setActionState] = useState<ActionState>({ status: "idle" });
  const [filterFormat, setFilterFormat] = useState("");

  const [userRoles, setUserRoles] = useState<string[]>([]);
  const canWrite = isWorkflowAdmin(userRoles);

  const loadTemplates = useCallback(async (fmt: string) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }

    const user = getUser<{ roles: string[] }>();
    if (!isWorkflowAdmin(user?.roles ?? [])) { router.replace("/inbox"); return; }
    setUserRoles(user?.roles ?? []);

    setLoading(true);
    setError(null);
    try {
      const res = await api.listTemplates(token, fmt || undefined);
      if (!res.success) {
        setError(res.error.code);
        return;
      }
      setTemplates(res.data ?? []);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => { loadTemplates(filterFormat); }, [loadTemplates, filterFormat]);

  const loadDetail = useCallback(async (id: string) => {
    const token = getAccessToken();
    if (!token) return;
    setDetailLoading(true);
    setDetail(null);
    try {
      const res = await api.getTemplate(token, id);
      if (res.success) setDetail(res.data);
    } finally {
      setDetailLoading(false);
    }
  }, []);

  useEffect(() => {
    if (selectedId) loadDetail(selectedId);
    else setDetail(null);
  }, [selectedId, loadDetail]);

  const handleAction = async (id: string, action: "clone" | "publish" | "deactivate") => {
    const token = getAccessToken();
    if (!token) return;
    setActionState({ status: "loading", id, action });
    try {
      let res;
      if (action === "clone") res = await api.cloneTemplate(token, id);
      else if (action === "publish") res = await api.publishTemplate(token, id);
      else res = await api.deactivateTemplate(token, id);

      if (!res.success) {
        setActionState({ status: "error", message: res.error.message || res.error.code });
      } else {
        const msg =
          action === "clone" ? `โคลนสำเร็จ — สร้าง Template ใหม่ (ID: ${(res.data as { id: string }).id})` :
          action === "publish" ? "เผยแพร่สำเร็จ" : "ปิดใช้งานสำเร็จ";
        setActionState({ status: "success", message: msg });
        // Re-fetch to get updated status — do NOT trust the action response status
        // (known misleading-200 wart when two publishes serialize; always re-fetch)
        await loadTemplates(filterFormat);
        if (selectedId) loadDetail(selectedId);
      }
    } catch {
      setActionState({ status: "error", message: "เกิดข้อผิดพลาดในการเชื่อมต่อ" });
    }
  };

  const isActing = (id: string, action: string) =>
    actionState.status === "loading" && actionState.id === id && actionState.action === action;

  return (
    <main className="min-h-screen">
      <header className="bg-surface border-b border-line px-4 py-4 sticky top-12 z-10">
        <div className="max-w-3xl mx-auto">
          <h1 className="text-lg font-bold text-ink">Workflow Templates</h1>
        </div>
      </header>

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-4">

        {/* Action feedback */}
        {actionState.status !== "idle" && actionState.status !== "loading" && (
          <div className={`rounded-lg px-4 py-3 text-sm flex items-center justify-between gap-2 border ${
            actionState.status === "success"
              ? "bg-success-bg text-success-fg border-success/30"
              : "bg-danger-bg text-danger-fg border-danger/30"
          }`}>
            <span>{actionState.message}</span>
            <button onClick={() => setActionState({ status: "idle" })} className="text-base opacity-60 touch-target px-1">×</button>
          </div>
        )}

        {/* Filter */}
        <Card padding="sm">
          <input
            value={filterFormat}
            onChange={(e) => { setFilterFormat(e.target.value.trim().toUpperCase()); setSelectedId(null); }}
            placeholder="กรองรูปแบบเอกสาร (POP, DEMO3...)"
            className="w-full border border-line-strong rounded-md px-3 h-11 text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
          />
        </Card>

        {loading && (
          <div className="flex justify-center py-16 text-brand">
            <Spinner size="md" />
          </div>
        )}
        {!loading && error && (
          <ErrorState code={error} onRetry={() => loadTemplates(filterFormat)} />
        )}
        {!loading && !error && templates.length === 0 && (
          <div className="text-center text-sm text-subtle py-16">ไม่พบ Template</div>
        )}

        {!loading && !error && templates.length > 0 && (
          <div className="flex flex-col gap-2">
            {templates.map((t) => (
              <Card key={t.id} padding="none" className="overflow-hidden">
                {/* Template row */}
                <button
                  onClick={() => setSelectedId(selectedId === t.id ? null : t.id)}
                  className="w-full text-left p-4 flex items-start gap-3"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-semibold text-ink text-sm">{t.doc_format_code}</span>
                      <span className="text-xs text-muted">v{t.version}</span>
                      <StatusBadge kind="template" status={t.status} />
                    </div>
                    <p className="text-xs text-muted mt-0.5 truncate">{t.name}</p>
                  </div>
                  <span className="text-subtle text-sm mt-0.5">{selectedId === t.id ? "▲" : "▼"}</span>
                </button>

                {/* Expanded detail */}
                {selectedId === t.id && (
                  <div className="border-t border-line px-4 pb-4">
                    {/* Actions */}
                    {canWrite && (
                      <div className="flex gap-2 mt-3 flex-wrap">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => handleAction(t.id, "clone")}
                          disabled={actionState.status === "loading"}
                          loading={isActing(t.id, "clone")}
                        >
                          {isActing(t.id, "clone") ? "กำลังโคลน..." : "โคลน"}
                        </Button>
                        {t.status === "draft" && (
                          <Button
                            size="sm"
                            onClick={() => handleAction(t.id, "publish")}
                            disabled={actionState.status === "loading"}
                            loading={isActing(t.id, "publish")}
                            className="bg-success hover:bg-success"
                          >
                            {isActing(t.id, "publish") ? "กำลังเผยแพร่..." : "เผยแพร่"}
                          </Button>
                        )}
                        {t.status === "active" && (
                          <Button
                            variant="outline"
                            size="sm"
                            onClick={() => handleAction(t.id, "deactivate")}
                            disabled={actionState.status === "loading"}
                            loading={isActing(t.id, "deactivate")}
                            className="border-danger/40 text-danger-fg"
                          >
                            {isActing(t.id, "deactivate") ? "กำลังปิด..." : "ปิดใช้งาน"}
                          </Button>
                        )}
                      </div>
                    )}

                    {/* Step detail */}
                    {detailLoading && (
                      <div className="flex justify-center py-6 text-brand">
                        <Spinner size="sm" />
                      </div>
                    )}
                    {!detailLoading && detail && detail.id === t.id && (
                      <div className="mt-4 flex flex-col gap-3">
                        {detail.effective_from && (
                          <p className="text-xs text-subtle">มีผลตั้งแต่: {detail.effective_from.slice(0, 19)}</p>
                        )}
                        {detail.steps.length === 0 ? (
                          <p className="text-sm text-subtle text-center py-2">ไม่มีขั้นตอน</p>
                        ) : (
                          detail.steps.map((step) => (
                            <div key={step.id} className="border border-line rounded-md p-3">
                              <div className="flex items-center gap-2 mb-1">
                                <span className="text-xs font-semibold text-muted w-5 flex-shrink-0">{step.sequence_no}</span>
                                <p className="text-sm font-medium text-ink">{step.position_name}</p>
                                <span className="text-xs text-subtle ml-auto">
                                  {step.condition_type === 1 && "คนใดคนหนึ่ง"}
                                  {step.condition_type === 2 && "ทุกคน"}
                                  {step.condition_type === 3 && "ภายนอก"}
                                </span>
                              </div>
                              {step.assignees.length > 0 ? (
                                <div className="ml-7 flex flex-col gap-0.5">
                                  {step.assignees.map((a) => (
                                    <p key={a.user_id} className="text-xs text-muted">
                                      {a.display_name} ({a.username})
                                    </p>
                                  ))}
                                </div>
                              ) : (
                                <p className="ml-7 text-xs text-subtle">
                                  {step.condition_type === 3 ? "ผู้เซ็นภายนอก (เชิญตอน import)" : "ไม่มีผู้รับผิดชอบ"}
                                </p>
                              )}
                            </div>
                          ))
                        )}
                      </div>
                    )}
                  </div>
                )}
              </Card>
            ))}
          </div>
        )}
      </div>
    </main>
  );
}
