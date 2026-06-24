"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type TemplateRow, type TemplateDetail } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Icon, Input, Spinner, StatusBadge } from "@/components/ui";

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

  const [showCreate, setShowCreate] = useState(false);
  const [newFormat, setNewFormat] = useState("");
  const [newName, setNewName] = useState("");
  const [creating, setCreating] = useState(false);
  const [createErr, setCreateErr] = useState<string | null>(null);

  const handleCreate = async () => {
    const token = getAccessToken();
    if (!token) return;
    const fmt = newFormat.trim().toUpperCase();
    if (!fmt || !newName.trim()) { setCreateErr("กรอกรหัสรูปแบบและชื่อ"); return; }
    setCreating(true); setCreateErr(null);
    const res = await api.createTemplate(token, { doc_format_code: fmt, name: newName.trim() });
    setCreating(false);
    if (!res.success) { setCreateErr(res.error.message || res.error.code); return; }
    router.push(`/admin/workflows/${res.data.id}`);
  };

  const loadTemplates = useCallback(async (fmt: string) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    const user = getUser<{ roles: string[] }>();
    if (!isWorkflowAdmin(user?.roles ?? [])) { router.replace("/inbox"); return; }
    setUserRoles(user?.roles ?? []);
    setLoading(true); setError(null);
    try {
      const res = await api.listTemplates(token, fmt || undefined);
      if (!res.success) { setError(res.error.code); return; }
      setTemplates(res.data ?? []);
    } catch { setError("network_error"); } finally { setLoading(false); }
  }, [router]);

  useEffect(() => { loadTemplates(filterFormat); }, [loadTemplates, filterFormat]);

  const loadDetail = useCallback(async (id: string) => {
    const token = getAccessToken();
    if (!token) return;
    setDetailLoading(true); setDetail(null);
    try {
      const res = await api.getTemplate(token, id);
      if (res.success) setDetail(res.data);
    } finally { setDetailLoading(false); }
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
        const msg = action === "clone"
          ? `โคลนสำเร็จ — Template ใหม่ (ID: ${(res.data as { id: string }).id})`
          : action === "publish" ? "เผยแพร่สำเร็จ" : "ปิดใช้งานสำเร็จ";
        setActionState({ status: "success", message: msg });
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
    <div className="min-h-screen">
      {/* Header */}
      <div className="bg-surface border-b border-line px-6 py-4 sticky top-14 lg:top-0 z-10">
        <div className="max-w-5xl mx-auto flex items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-bold text-ink">Workflow Templates</h1>
            {!loading && !error && (
              <p className="text-sm text-muted mt-0.5">{templates.length} template</p>
            )}
          </div>
          {canWrite && (
            <Button size="sm" onClick={() => { setNewFormat(""); setNewName(""); setCreateErr(null); setShowCreate(true); }}>
              <Icon name="plus" size={15} />
              สร้าง Workflow
            </Button>
          )}
        </div>
      </div>

      {/* Create modal */}
      {showCreate && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
          <div className="bg-surface rounded-2xl shadow-pop w-full max-w-md p-6 flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-bold text-ink">สร้าง Workflow ใหม่</h2>
              <button onClick={() => setShowCreate(false)} className="text-subtle hover:text-ink touch-target -mr-2 px-2 flex items-center">
                <Icon name="x" size={20} />
              </button>
            </div>
            <Input label="รหัสรูปแบบเอกสาร *" value={newFormat}
              onChange={(e) => setNewFormat(e.target.value.toUpperCase())} placeholder="เช่น POP, INV" maxLength={50} />
            <Input label="ชื่อ Workflow *" value={newName}
              onChange={(e) => setNewName(e.target.value)} placeholder="เช่น ใบสั่งซื้อ (POP)" maxLength={200} />
            <p className="text-xs text-muted bg-info-bg border border-info/20 rounded-lg px-3 py-2.5">
              จะสร้างเป็น <strong>ฉบับร่าง</strong> แล้วพาไปหน้าแก้ไขขั้นตอน
            </p>
            {createErr && (
              <div className="flex items-start gap-2 bg-danger-bg border border-danger/30 rounded-lg px-3 py-2.5">
                <Icon name="exclamation-triangle" size={16} className="text-danger-fg flex-shrink-0 mt-0.5" />
                <p className="text-sm text-danger-fg">{createErr}</p>
              </div>
            )}
            <div className="flex gap-2 pt-1">
              <Button variant="outline" onClick={() => setShowCreate(false)} disabled={creating} block>ยกเลิก</Button>
              <Button onClick={handleCreate} loading={creating} disabled={!newFormat.trim() || !newName.trim()} block>
                สร้างฉบับร่าง
              </Button>
            </div>
          </div>
        </div>
      )}

      <div className="max-w-5xl mx-auto px-6 py-5 flex flex-col gap-4">
        {/* Feedback banner */}
        {actionState.status !== "idle" && actionState.status !== "loading" && (
          <div className={`flex items-center justify-between gap-3 rounded-xl px-4 py-3 text-sm border ${
            actionState.status === "success"
              ? "bg-success-bg text-success-fg border-success/30"
              : "bg-danger-bg text-danger-fg border-danger/30"
          }`}>
            <div className="flex items-center gap-2">
              <Icon name={actionState.status === "success" ? "check-circle" : "exclamation-triangle"} size={16} />
              {actionState.message}
            </div>
            <button onClick={() => setActionState({ status: "idle" })} className="touch-target px-1 flex items-center opacity-60 hover:opacity-100">
              <Icon name="x" size={16} />
            </button>
          </div>
        )}

        {/* Filter */}
        <div className="relative max-w-sm">
          <Icon name="search" size={16} className="absolute left-3 top-1/2 -translate-y-1/2 text-subtle pointer-events-none" />
          <input
            value={filterFormat}
            onChange={(e) => { setFilterFormat(e.target.value.trim().toUpperCase()); setSelectedId(null); }}
            placeholder="กรองรูปแบบ (POP, DEMO3...)"
            className="w-full h-10 pl-9 pr-3 rounded-lg border border-line-strong text-sm bg-surface text-ink placeholder:text-subtle focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
          />
        </div>

        {loading && <div className="flex justify-center py-20 text-brand"><Spinner size="md" /></div>}
        {!loading && error && <ErrorState code={error} onRetry={() => loadTemplates(filterFormat)} />}
        {!loading && !error && templates.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-subtle gap-3">
            <Icon name="workflow" size={40} className="opacity-30" />
            <p className="text-sm">ไม่พบ Template</p>
          </div>
        )}

        {!loading && !error && templates.length > 0 && (
          <div className="flex flex-col gap-3">
            {templates.map((t) => (
              <div key={t.id} className="bg-surface border border-line rounded-xl shadow-card overflow-hidden">
                {/* Card header — clickable */}
                <button
                  onClick={() => setSelectedId(selectedId === t.id ? null : t.id)}
                  className="w-full text-left p-4 flex items-start gap-4 hover:bg-surface-muted/50 transition-colors"
                >
                  {/* Format badge */}
                  <div className="w-12 h-12 rounded-xl bg-brand-50 border border-brand-100 flex items-center justify-center flex-shrink-0">
                    <span className="text-xs font-bold text-brand-700">{t.doc_format_code.slice(0, 4)}</span>
                  </div>

                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="font-semibold text-ink">{t.doc_format_code}</span>
                      <span className="text-xs text-subtle">v{t.version}</span>
                      <StatusBadge kind="template" status={t.status} />
                    </div>
                    <p className="text-sm text-muted mt-0.5 truncate">{t.name}</p>
                  </div>

                  <Icon
                    name={selectedId === t.id ? "chevron-left" : "chevron-right"}
                    size={18}
                    className={`text-subtle mt-1 transition-transform ${selectedId === t.id ? "-rotate-90" : "rotate-0"}`}
                  />
                </button>

                {/* Expanded content */}
                {selectedId === t.id && (
                  <div className="border-t border-line bg-surface-muted/30 px-4 py-4">
                    {/* Actions */}
                    {canWrite && (
                      <div className="flex gap-2 flex-wrap mb-4">
                        <Button size="sm" onClick={() => router.push(`/admin/workflows/${t.id}`)}>
                          <Icon name="pencil-square" size={14} />
                          {t.status === "draft" ? "แก้ไขขั้นตอน" : "เปิดดู / โคลน"}
                        </Button>
                        <Button variant="outline" size="sm" onClick={() => handleAction(t.id, "clone")}
                          disabled={actionState.status === "loading"} loading={isActing(t.id, "clone")}>
                          {isActing(t.id, "clone") ? "กำลังโคลน..." : "โคลน"}
                        </Button>
                        {t.status === "draft" && (
                          <Button size="sm" onClick={() => handleAction(t.id, "publish")}
                            disabled={actionState.status === "loading"} loading={isActing(t.id, "publish")}
                            className="bg-success hover:bg-success/90">
                            {isActing(t.id, "publish") ? "กำลังเผยแพร่..." : "เผยแพร่"}
                          </Button>
                        )}
                        {t.status === "active" && (
                          <Button variant="outline" size="sm" onClick={() => handleAction(t.id, "deactivate")}
                            disabled={actionState.status === "loading"} loading={isActing(t.id, "deactivate")}
                            className="border-danger/40 text-danger-fg hover:bg-danger-bg">
                            {isActing(t.id, "deactivate") ? "กำลังปิด..." : "ปิดใช้งาน"}
                          </Button>
                        )}
                      </div>
                    )}

                    {/* Step detail */}
                    {detailLoading && (
                      <div className="flex justify-center py-6 text-brand"><Spinner size="sm" /></div>
                    )}
                    {!detailLoading && detail && detail.id === t.id && (
                      <div className="flex flex-col gap-2">
                        {detail.effective_from && (
                          <p className="text-xs text-subtle mb-2">มีผลตั้งแต่: {detail.effective_from.slice(0, 19)}</p>
                        )}
                        {detail.steps.length === 0 ? (
                          <p className="text-sm text-subtle text-center py-4">ไม่มีขั้นตอน</p>
                        ) : (
                          detail.steps.map((step) => (
                            <div key={step.id} className="flex items-start gap-3 bg-surface rounded-lg border border-line p-3">
                              <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-brand text-white text-xs font-bold flex-shrink-0 mt-0.5">
                                {step.sequence_no}
                              </span>
                              <div className="flex-1 min-w-0">
                                <div className="flex items-center gap-2 flex-wrap">
                                  <p className="text-sm font-semibold text-ink">{step.position_name}</p>
                                  <span className="text-xs text-subtle">
                                    {step.condition_type === 1 && "คนใดคนหนึ่ง"}
                                    {step.condition_type === 2 && "ทุกคน"}
                                    {step.condition_type === 3 && "ผู้เซ็นภายนอก"}
                                  </span>
                                </div>
                                {step.assignees.length > 0 ? (
                                  <div className="mt-1 flex flex-wrap gap-1">
                                    {step.assignees.map((a) => (
                                      <span key={a.user_id} className="text-xs text-muted bg-surface-muted rounded px-1.5 py-0.5">
                                        {a.display_name}
                                      </span>
                                    ))}
                                  </div>
                                ) : (
                                  <p className="text-xs text-subtle mt-0.5">
                                    {step.condition_type === 3 ? "เชิญตอน import" : "ไม่มีผู้รับผิดชอบ"}
                                  </p>
                                )}
                              </div>
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
    </div>
  );
}
