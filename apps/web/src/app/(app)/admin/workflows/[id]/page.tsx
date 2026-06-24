"use client";

import { useEffect, useState, useCallback } from "react";
import { useParams, useRouter } from "next/navigation";
import { api, type TemplateDetail, type UserOption, type StepInput, type SigSlot } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Icon, Input, Spinner, StatusBadge } from "@/components/ui";
import SignaturePositionEditor from "@/components/SignaturePositionEditor";

const CONDITIONS = [
  { value: 1, label: "คนใดคนหนึ่งเซ็น" },
  { value: 2, label: "ทุกคนต้องเซ็น" },
  { value: 3, label: "ผู้เซ็นภายนอก" },
];

interface EditStep {
  key: string;
  position_code: string;
  position_name: string;
  condition_type: number;
  assignee_user_ids: number[];
  signature_slot: SigSlot | null;
}

let keySeq = 0;
const newKey = () => `s${++keySeq}`;

function isWorkflowAdmin(roles: string[]): boolean {
  return roles.some((r) => ["workflow_admin", "system_admin"].includes(r));
}

const selectCls = "h-11 px-3 rounded-lg border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400 disabled:bg-surface-muted disabled:text-muted";

export default function WorkflowEditPage() {
  const router = useRouter();
  const params = useParams();
  const id = typeof params.id === "string" ? params.id : "";

  const [tmpl, setTmpl] = useState<TemplateDetail | null>(null);
  const [users, setUsers] = useState<UserOption[]>([]);
  const [name, setName] = useState("");
  const [steps, setSteps] = useState<EditStep[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [msg, setMsg] = useState<{ tone: "ok" | "err"; text: string } | null>(null);

  const isDraft = tmpl?.status === "draft";

  const load = useCallback(async () => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    if (!isWorkflowAdmin(getUser<{ roles: string[] }>()?.roles ?? [])) { router.replace("/inbox"); return; }
    setLoading(true); setError(null);
    try {
      const [tRes, uRes] = await Promise.all([api.getTemplate(token, id), api.listUsers(token)]);
      if (!tRes.success) { setError(tRes.error.code); return; }
      if (uRes.success) setUsers(uRes.data ?? []);
      setTmpl(tRes.data);
      setName(tRes.data.name);
      setSteps(tRes.data.steps.map((s) => ({
        key: newKey(),
        position_code: s.position_code,
        position_name: s.position_name,
        condition_type: s.condition_type,
        assignee_user_ids: s.assignees.map((a) => Number(a.user_id)),
        signature_slot: s.signature_slot ?? null,
      })));
    } catch { setError("network_error"); } finally { setLoading(false); }
  }, [id, router]);

  useEffect(() => { load(); }, [load]);

  const patchStep = (key: string, patch: Partial<EditStep>) =>
    setSteps((cur) => cur.map((s) => (s.key === key ? { ...s, ...patch } : s)));

  const addStep = () =>
    setSteps((cur) => [...cur, { key: newKey(), position_code: "", position_name: "", condition_type: 1, assignee_user_ids: [], signature_slot: null }]);

  const removeStep = (key: string) => setSteps((cur) => cur.filter((s) => s.key !== key));

  const move = (idx: number, dir: -1 | 1) =>
    setSteps((cur) => {
      const next = [...cur];
      const j = idx + dir;
      if (j < 0 || j >= next.length) return cur;
      [next[idx], next[j]] = [next[j], next[idx]];
      return next;
    });

  const toggleAssignee = (key: string, userId: number) =>
    setSteps((cur) => cur.map((s) => {
      if (s.key !== key) return s;
      const has = s.assignee_user_ids.includes(userId);
      return { ...s, assignee_user_ids: has ? s.assignee_user_ids.filter((x) => x !== userId) : [...s.assignee_user_ids, userId] };
    }));

  const validate = (): string | null => {
    if (steps.length === 0) return "ต้องมีอย่างน้อย 1 ขั้นตอน";
    const codes = new Set<string>();
    for (const s of steps) {
      if (!s.position_code.trim()) return "ทุกขั้นต้องมีรหัสตำแหน่ง";
      if (!s.position_name.trim()) return "ทุกขั้นต้องมีชื่อตำแหน่ง";
      const code = s.position_code.trim();
      if (codes.has(code)) return `รหัสตำแหน่งซ้ำ: ${code}`;
      codes.add(code);
      if (s.condition_type === 3) { if (s.assignee_user_ids.length > 0) return "ขั้นผู้เซ็นภายนอก ต้องไม่มีผู้รับผิดชอบ"; }
      else if (s.assignee_user_ids.length === 0) return "ต้องเลือกผู้เซ็นอย่างน้อย 1 คน";
    }
    return null;
  };

  const saveName = async () => {
    const token = getAccessToken();
    if (!token || !name.trim()) return;
    const res = await api.updateTemplate(token, id, { name: name.trim() });
    setMsg(res.success ? { tone: "ok", text: "บันทึกชื่อแล้ว" } : { tone: "err", text: res.error.message || res.error.code });
  };

  const saveSteps = async () => {
    const token = getAccessToken();
    if (!token) return;
    const v = validate();
    if (v) { setMsg({ tone: "err", text: v }); return; }
    setSaving(true); setMsg(null);
    const payload: StepInput[] = steps.map((s, i) => ({
      position_code: s.position_code.trim(),
      position_name: s.position_name.trim(),
      sequence_no: i + 1,
      condition_type: s.condition_type,
      assignee_user_ids: s.condition_type === 3 ? [] : s.assignee_user_ids,
      signature_slot: s.signature_slot ?? null,
    }));
    const res = await api.updateSteps(token, id, payload);
    setSaving(false);
    if (!res.success) { setMsg({ tone: "err", text: res.error.message || res.error.code }); return; }
    setMsg({ tone: "ok", text: "บันทึกขั้นตอนแล้ว" });
    load();
  };

  const publish = async () => {
    const token = getAccessToken();
    if (!token) return;
    setSaving(true);
    const res = await api.publishTemplate(token, id);
    setSaving(false);
    if (!res.success) { setMsg({ tone: "err", text: res.error.message || res.error.code }); return; }
    setMsg({ tone: "ok", text: "เผยแพร่แล้ว — เวอร์ชันนี้ใช้งานอยู่" });
    load();
  };

  const cloneToEdit = async () => {
    const token = getAccessToken();
    if (!token) return;
    setSaving(true);
    const res = await api.cloneTemplate(token, id);
    setSaving(false);
    if (!res.success) { setMsg({ tone: "err", text: res.error.message || res.error.code }); return; }
    router.push(`/admin/workflows/${res.data.id}`);
  };

  if (loading) return <div className="min-h-screen flex items-center justify-center text-brand"><Spinner size="md" /></div>;
  if (error || !tmpl) {
    return (
      <div className="min-h-screen flex flex-col">
        <div className="bg-surface border-b border-line px-4 py-3 sticky top-14 lg:top-0 z-10">
          <button onClick={() => router.push("/admin/workflows")} className="flex items-center gap-1.5 text-sm font-medium text-brand-700 touch-target">
            <Icon name="arrow-left" size={16} /> กลับ
          </button>
        </div>
        <div className="flex-1 flex items-center justify-center p-8">
          <ErrorState code={error ?? "not_found"} onRetry={error === "network_error" ? load : undefined} />
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen">
      {/* Header */}
      <div className="bg-surface border-b border-line px-4 lg:px-6 py-3 sticky top-14 lg:top-0 z-10">
        <div className="max-w-4xl mx-auto flex items-center gap-3">
          <button onClick={() => router.push("/admin/workflows")} className="flex items-center gap-1 text-sm font-medium text-brand-700 touch-target flex-shrink-0">
            <Icon name="arrow-left" size={16} />
          </button>
          <div className="flex-1 min-w-0 flex items-center gap-2 flex-wrap">
            <span className="text-xs font-bold text-brand-700 bg-brand-50 rounded px-1.5 py-0.5">{tmpl.doc_format_code}</span>
            <h1 className="text-base font-bold text-ink">v{tmpl.version}</h1>
            <StatusBadge kind="template" status={tmpl.status} />
          </div>
        </div>
      </div>

      <div className="max-w-4xl mx-auto px-4 lg:px-6 py-5 flex flex-col gap-4">
        {/* Feedback */}
        {msg && (
          <div className={`flex items-center justify-between gap-3 rounded-xl px-4 py-3 text-sm border ${
            msg.tone === "ok" ? "bg-success-bg text-success-fg border-success/30" : "bg-danger-bg text-danger-fg border-danger/30"
          }`}>
            <div className="flex items-center gap-2">
              <Icon name={msg.tone === "ok" ? "check-circle" : "exclamation-triangle"} size={16} />
              {msg.text}
            </div>
            <button onClick={() => setMsg(null)} className="opacity-60 hover:opacity-100 touch-target px-1 flex items-center">
              <Icon name="x" size={15} />
            </button>
          </div>
        )}

        {/* Read-only notice */}
        {!isDraft && (
          <div className="bg-warning-bg border border-warning/30 rounded-xl p-4 flex flex-col sm:flex-row sm:items-center gap-3">
            <div className="flex items-start gap-2 flex-1">
              <Icon name="information-circle" size={18} className="text-warning-fg flex-shrink-0 mt-0.5" />
              <p className="text-sm text-warning-fg">
                เวอร์ชันนี้<strong>{tmpl.status === "active" ? " ใช้งานอยู่" : " ปิดใช้งานแล้ว"}</strong> — แก้ไขโดยตรงไม่ได้
                {" "}ให้ <strong>โคลนเป็นเวอร์ชันใหม่</strong> แล้วแก้ที่ฉบับร่าง
              </p>
            </div>
            <Button onClick={cloneToEdit} loading={saving} size="sm" className="flex-shrink-0">
              โคลนเพื่อแก้ไข
            </Button>
          </div>
        )}

        {/* Name */}
        <div className="bg-surface border border-line rounded-xl shadow-card p-5">
          <p className="text-sm font-semibold text-ink mb-3">ชื่อ Workflow</p>
          <div className="flex gap-2">
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              disabled={!isDraft}
              maxLength={200}
              placeholder="ชื่อ Workflow"
              className="flex-1"
            />
            {isDraft && (
              <Button variant="outline" onClick={saveName} disabled={!name.trim()}>
                บันทึกชื่อ
              </Button>
            )}
          </div>
        </div>

        {/* Steps header */}
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold text-ink">ขั้นตอนการเซ็น <span className="text-subtle">({steps.length})</span></h2>
          {isDraft && (
            <Button size="sm" variant="outline" onClick={addStep}>
              <Icon name="plus" size={15} />
              เพิ่มขั้นตอน
            </Button>
          )}
        </div>

        {steps.length === 0 && (
          <div className="bg-surface border border-line border-dashed rounded-xl p-8 text-center">
            <Icon name="workflow" size={32} className="text-subtle mx-auto mb-2" />
            <p className="text-sm text-subtle">ยังไม่มีขั้นตอน — กด &quot;เพิ่มขั้นตอน&quot;</p>
          </div>
        )}

        {steps.map((s, idx) => (
          <div key={s.key} className="bg-surface border border-line rounded-xl shadow-card">
            {/* Step header */}
            <div className="flex items-center justify-between gap-2 px-4 py-3 border-b border-line">
              <div className="flex items-center gap-3">
                <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-brand text-white text-sm font-bold flex-shrink-0">
                  {idx + 1}
                </span>
                <span className="text-sm font-semibold text-ink">
                  {s.position_name || `ขั้นที่ ${idx + 1}`}
                </span>
              </div>
              {isDraft && (
                <div className="flex items-center gap-1">
                  <button onClick={() => move(idx, -1)} disabled={idx === 0}
                    className="touch-target px-2 flex items-center text-muted disabled:opacity-30 hover:text-ink">
                    <Icon name="chevron-left" size={16} className="-rotate-90" />
                  </button>
                  <button onClick={() => move(idx, 1)} disabled={idx === steps.length - 1}
                    className="touch-target px-2 flex items-center text-muted disabled:opacity-30 hover:text-ink">
                    <Icon name="chevron-right" size={16} className="-rotate-90" />
                  </button>
                  <button onClick={() => removeStep(s.key)}
                    className="touch-target px-2 flex items-center text-danger-fg hover:bg-danger-bg rounded-lg">
                    <Icon name="x" size={16} />
                  </button>
                </div>
              )}
            </div>

            {/* Step body */}
            <div className="p-4 flex flex-col gap-4">
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <Input label="รหัสตำแหน่ง" value={s.position_code} disabled={!isDraft}
                  onChange={(e) => patchStep(s.key, { position_code: e.target.value.toUpperCase() })}
                  placeholder="MAKER" maxLength={50} />
                <Input label="ชื่อตำแหน่ง" value={s.position_name} disabled={!isDraft}
                  onChange={(e) => patchStep(s.key, { position_name: e.target.value })}
                  placeholder="ผู้จัดทำ" maxLength={200} />
              </div>

              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-ink">เงื่อนไขการเซ็น</label>
                <select
                  value={s.condition_type} disabled={!isDraft}
                  onChange={(e) => patchStep(s.key, {
                    condition_type: Number(e.target.value),
                    ...(Number(e.target.value) === 3 ? { assignee_user_ids: [] } : {}),
                  })}
                  className={selectCls}
                >
                  {CONDITIONS.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
                </select>
              </div>

              {s.condition_type !== 3 ? (
                <div className="flex flex-col gap-1.5">
                  <label className="text-sm font-medium text-ink">
                    ผู้เซ็น <span className="text-subtle">({s.assignee_user_ids.length} คนเลือก)</span>
                  </label>
                  <div className="flex flex-col gap-0.5 max-h-52 overflow-y-auto border border-line rounded-lg p-2 bg-surface-muted">
                    {users.map((u) => {
                      const checked = s.assignee_user_ids.includes(Number(u.id));
                      return (
                        <label key={u.id} className={`flex items-center gap-2 px-2 py-2 rounded-md ${isDraft ? "cursor-pointer hover:bg-surface" : "opacity-60"}`}>
                          <input type="checkbox" checked={checked} disabled={!isDraft}
                            onChange={() => toggleAssignee(s.key, Number(u.id))}
                            className="w-4 h-4 rounded border-line-strong accent-brand-600" />
                          <span className="text-sm text-ink">{u.display_name} <span className="text-subtle text-xs">({u.username})</span></span>
                        </label>
                      );
                    })}
                  </div>
                </div>
              ) : (
                <div className="flex items-start gap-2 bg-info-bg border border-info/20 rounded-lg px-3 py-2.5">
                  <Icon name="information-circle" size={15} className="text-info-fg flex-shrink-0 mt-0.5" />
                  <p className="text-xs text-info-fg">ผู้เซ็นภายนอกถูกเชิญตอนนำเข้าเอกสาร — ไม่ต้องกำหนดที่นี่</p>
                </div>
              )}
            </div>
          </div>
        ))}

        {/* Signature position editor */}
        {isDraft && steps.length > 0 && (
          <div className="bg-surface border border-line rounded-xl shadow-card p-5">
            <p className="text-sm font-semibold text-ink mb-1">ตำแหน่งลายเซ็นบนเอกสาร <span className="text-subtle font-normal">(ไม่บังคับ)</span></p>
            <p className="text-xs text-muted mb-4">อัปโหลด PDF ตัวอย่าง แล้วตีกรอบตำแหน่งลายเซ็นของแต่ละขั้น</p>
            <SignaturePositionEditor
              steps={steps.map((s) => ({
                key: s.key,
                label: s.position_name.trim() || s.position_code.trim() || "ขั้น",
                slot: s.signature_slot,
              }))}
              onChange={(key, slot) => patchStep(key, { signature_slot: slot })}
            />
            <p className="text-xs text-subtle mt-3">* กรอบจะถูกบันทึกเมื่อกด &quot;บันทึกขั้นตอน&quot;</p>
          </div>
        )}

        {/* Sticky action bar */}
        {isDraft && (
          <div className="sticky bottom-0 bg-bg/90 backdrop-blur-sm py-3 flex gap-2 border-t border-line mt-2 -mx-4 lg:-mx-6 px-4 lg:px-6">
            <Button onClick={saveSteps} loading={saving} block>
              <Icon name="check" size={16} />
              บันทึกขั้นตอน
            </Button>
            <Button onClick={publish} variant="outline" loading={saving}
              disabled={steps.length === 0} block
              className="border-success/40 text-success-fg hover:bg-success-bg">
              เผยแพร่
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
