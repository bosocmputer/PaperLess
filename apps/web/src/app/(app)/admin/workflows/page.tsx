"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type TemplateRow, type TemplateDetail } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";

// workflow_templates.status CHECK: draft,active,inactive
const TMPL_STATUS_LABELS: Record<string, string> = {
  draft:    "ร่าง",
  active:   "ใช้งาน",
  inactive: "ปิดใช้",
};

function statusBadge(status: string) {
  const colors: Record<string, string> = {
    draft:    "bg-amber-100 text-amber-700",
    active:   "bg-green-100 text-green-700",
    inactive: "bg-gray-100 text-gray-500",
  };
  return colors[status] ?? "bg-gray-100 text-gray-500";
}

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
    <main className="min-h-screen bg-gray-50">
      <header className="bg-white border-b border-gray-200 px-4 py-4 sticky top-0 z-10">
        <div className="max-w-3xl mx-auto flex items-center justify-between gap-2">
          <div>
            <h1 className="text-lg font-bold text-gray-900">Workflow Templates</h1>
          </div>
          <button
            onClick={() => router.push("/admin/documents")}
            className="text-sm text-blue-600 px-3 py-1.5 border border-blue-200 rounded-lg"
          >
            เอกสาร
          </button>
        </div>
      </header>

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-4">

        {/* Action feedback */}
        {actionState.status !== "idle" && actionState.status !== "loading" && (
          <div className={`rounded-xl px-4 py-3 text-sm flex items-center justify-between gap-2 ${
            actionState.status === "success"
              ? "bg-green-50 text-green-700 border border-green-200"
              : "bg-red-50 text-red-700 border border-red-200"
          }`}>
            <span>{actionState.message}</span>
            <button onClick={() => setActionState({ status: "idle" })} className="text-xs opacity-60">×</button>
          </div>
        )}

        {/* Filter */}
        <div className="bg-white rounded-xl border border-gray-200 p-3">
          <input
            value={filterFormat}
            onChange={(e) => { setFilterFormat(e.target.value.trim().toUpperCase()); setSelectedId(null); }}
            placeholder="กรองรูปแบบเอกสาร (POP, DEMO3...)"
            className="w-full border border-gray-300 rounded-lg px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-blue-400"
          />
        </div>

        {loading && (
          <div className="flex justify-center py-16">
            <div className="w-8 h-8 border-4 border-blue-600 border-t-transparent rounded-full animate-spin" />
          </div>
        )}
        {!loading && error && (
          <ErrorState code={error} onRetry={() => loadTemplates(filterFormat)} />
        )}
        {!loading && !error && templates.length === 0 && (
          <div className="text-center text-sm text-gray-400 py-16">ไม่พบ Template</div>
        )}

        {!loading && !error && templates.length > 0 && (
          <div className="flex flex-col gap-2">
            {templates.map((t) => (
              <div key={t.id} className="bg-white rounded-xl border border-gray-200 overflow-hidden">
                {/* Template row */}
                <button
                  onClick={() => setSelectedId(selectedId === t.id ? null : t.id)}
                  className="w-full text-left p-4 flex items-start gap-3"
                >
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-semibold text-gray-900 text-sm">{t.doc_format_code}</span>
                      <span className="text-xs text-gray-500">v{t.version}</span>
                      <span className={`text-xs px-2 py-0.5 rounded-full ${statusBadge(t.status)}`}>
                        {TMPL_STATUS_LABELS[t.status] ?? t.status}
                      </span>
                    </div>
                    <p className="text-xs text-gray-500 mt-0.5 truncate">{t.name}</p>
                  </div>
                  <span className="text-gray-400 text-sm mt-0.5">{selectedId === t.id ? "▲" : "▼"}</span>
                </button>

                {/* Expanded detail */}
                {selectedId === t.id && (
                  <div className="border-t border-gray-100 px-4 pb-4">
                    {/* Actions */}
                    {canWrite && (
                      <div className="flex gap-2 mt-3 flex-wrap">
                        <button
                          onClick={() => handleAction(t.id, "clone")}
                          disabled={actionState.status === "loading"}
                          className="px-3 py-1.5 border border-gray-300 rounded-lg text-xs text-gray-700 disabled:opacity-40"
                        >
                          {isActing(t.id, "clone") ? "กำลังโคลน..." : "โคลน"}
                        </button>
                        {t.status === "draft" && (
                          <button
                            onClick={() => handleAction(t.id, "publish")}
                            disabled={actionState.status === "loading"}
                            className="px-3 py-1.5 bg-green-600 text-white rounded-lg text-xs disabled:opacity-40"
                          >
                            {isActing(t.id, "publish") ? "กำลังเผยแพร่..." : "เผยแพร่"}
                          </button>
                        )}
                        {t.status === "active" && (
                          <button
                            onClick={() => handleAction(t.id, "deactivate")}
                            disabled={actionState.status === "loading"}
                            className="px-3 py-1.5 border border-red-200 text-red-600 rounded-lg text-xs disabled:opacity-40"
                          >
                            {isActing(t.id, "deactivate") ? "กำลังปิด..." : "ปิดใช้งาน"}
                          </button>
                        )}
                      </div>
                    )}

                    {/* Step detail */}
                    {detailLoading && (
                      <div className="flex justify-center py-6">
                        <div className="w-6 h-6 border-3 border-blue-600 border-t-transparent rounded-full animate-spin" />
                      </div>
                    )}
                    {!detailLoading && detail && detail.id === t.id && (
                      <div className="mt-4 flex flex-col gap-3">
                        {detail.effective_from && (
                          <p className="text-xs text-gray-400">มีผลตั้งแต่: {detail.effective_from.slice(0, 19)}</p>
                        )}
                        {detail.steps.length === 0 ? (
                          <p className="text-sm text-gray-400 text-center py-2">ไม่มีขั้นตอน</p>
                        ) : (
                          detail.steps.map((step) => (
                            <div key={step.id} className="border border-gray-100 rounded-lg p-3">
                              <div className="flex items-center gap-2 mb-1">
                                <span className="text-xs font-semibold text-gray-500 w-5 flex-shrink-0">{step.sequence_no}</span>
                                <p className="text-sm font-medium text-gray-800">{step.position_name}</p>
                                <span className="text-xs text-gray-400 ml-auto">
                                  {step.condition_type === 1 && "คนใดคนหนึ่ง"}
                                  {step.condition_type === 2 && "ทุกคน"}
                                  {step.condition_type === 3 && "ภายนอก"}
                                </span>
                              </div>
                              {step.assignees.length > 0 ? (
                                <div className="ml-7 flex flex-col gap-0.5">
                                  {step.assignees.map((a) => (
                                    <p key={a.user_id} className="text-xs text-gray-600">
                                      {a.display_name} ({a.username})
                                    </p>
                                  ))}
                                </div>
                              ) : (
                                <p className="ml-7 text-xs text-gray-400">
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
              </div>
            ))}
          </div>
        )}
      </div>
    </main>
  );
}
