"use client";

import { useEffect, useState, useCallback } from "react";
import { useParams, useRouter } from "next/navigation";
import { api, type TemplateDetail, type UserOption, type StepInput } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Card, Input, Spinner, StatusBadge } from "@/components/ui";

const CONDITIONS = [
  { value: 1, label: "คนใดคนหนึ่งเซ็น" },
  { value: 2, label: "ทุกคนต้องเซ็น" },
  { value: 3, label: "ผู้เซ็นภายนอก" },
];

// Local editable step. sequence_no is derived from list order on save, so it's
// always unique/contiguous and the user just reorders rows.
interface EditStep {
  key: string;
  position_code: string;
  position_name: string;
  condition_type: number;
  assignee_user_ids: number[];
}

let keySeq = 0;
const newKey = () => `s${++keySeq}`;

function isWorkflowAdmin(roles: string[]): boolean {
  return roles.some((r) => ["workflow_admin", "system_admin"].includes(r));
}

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
    setLoading(true);
    setError(null);
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
      })));
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [id, router]);

  useEffect(() => { load(); }, [load]);

  // ── step mutations (local) ──
  const patchStep = (key: string, patch: Partial<EditStep>) =>
    setSteps((cur) => cur.map((s) => (s.key === key ? { ...s, ...patch } : s)));

  const addStep = () =>
    setSteps((cur) => [...cur, { key: newKey(), position_code: "", position_name: "", condition_type: 1, assignee_user_ids: [] }]);

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

  // ── client-side validation mirroring the backend ──
  const validate = (): string | null => {
    if (steps.length === 0) return "ต้องมีอย่างน้อย 1 ขั้นตอน";
    const codes = new Set<string>();
    for (const s of steps) {
      if (!s.position_code.trim()) return "ทุกขั้นต้องมีรหัสตำแหน่ง (position code)";
      if (!s.position_name.trim()) return "ทุกขั้นต้องมีชื่อตำแหน่ง";
      const code = s.position_code.trim();
      if (codes.has(code)) return `รหัสตำแหน่งซ้ำ: ${code}`;
      codes.add(code);
      if (s.condition_type === 3) {
        if (s.assignee_user_ids.length > 0) return "ขั้นแบบผู้เซ็นภายนอก ต้องไม่มีผู้รับผิดชอบ";
      } else if (s.assignee_user_ids.length === 0) {
        return "ขั้นแบบในระบบ ต้องเลือกผู้เซ็นอย่างน้อย 1 คน";
      }
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
    setSaving(true);
    setMsg(null);
    const payload: StepInput[] = steps.map((s, i) => ({
      position_code: s.position_code.trim(),
      position_name: s.position_name.trim(),
      sequence_no: i + 1,
      condition_type: s.condition_type,
      assignee_user_ids: s.condition_type === 3 ? [] : s.assignee_user_ids,
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

  if (loading) {
    return <div className="min-h-screen flex items-center justify-center text-brand"><Spinner size="md" /></div>;
  }
  if (error || !tmpl) {
    return (
      <main className="min-h-screen flex flex-col">
        <header className="bg-surface border-b border-line px-4 py-3">
          <button onClick={() => router.push("/admin/workflows")} className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 rounded-md">← กลับ</button>
        </header>
        <div className="flex-1 flex items-center justify-center">
          <ErrorState code={error ?? "not_found"} onRetry={error === "network_error" ? load : undefined} />
        </div>
      </main>
    );
  }

  return (
    <main className="min-h-screen">
      <header className="bg-surface border-b border-line px-4 py-3 sticky top-12 z-10">
        <div className="max-w-3xl mx-auto flex items-center gap-3">
          <button onClick={() => router.push("/admin/workflows")} className="touch-target -ml-2 px-2 text-sm font-medium text-brand-700 flex-shrink-0 rounded-md">← กลับ</button>
          <div className="flex-1 min-w-0">
            <h1 className="text-base font-bold text-ink truncate">{tmpl.doc_format_code} · v{tmpl.version}</h1>
          </div>
          <StatusBadge kind="template" status={tmpl.status} />
        </div>
      </header>

      <div className="max-w-3xl mx-auto w-full px-4 py-4 flex flex-col gap-4">
        {msg && (
          <div className={`rounded-lg px-4 py-3 text-sm border ${
            msg.tone === "ok" ? "bg-success-bg text-success-fg border-success/30" : "bg-danger-bg text-danger-fg border-danger/30"
          }`}>
            {msg.text}
          </div>
        )}

        {/* Read-only notice for non-draft */}
        {!isDraft && (
          <Card className="flex flex-col gap-3">
            <p className="text-sm text-ink">
              เวอร์ชันนี้<strong>{tmpl.status === "active" ? "ใช้งานอยู่" : "ปิดใช้งานแล้ว"}</strong> — แก้ไขโดยตรงไม่ได้
              เพื่อความถูกต้องของเอกสารที่กำลังเซ็น ให้ <strong>โคลนเป็นเวอร์ชันใหม่</strong> แล้วแก้ที่ฉบับร่าง
            </p>
            <Button onClick={cloneToEdit} loading={saving} className="self-start">โคลนเพื่อแก้ไข</Button>
          </Card>
        )}

        {/* Name */}
        <Card className="flex flex-col gap-3">
          <Input
            label="ชื่อ Workflow"
            value={name}
            onChange={(e) => setName(e.target.value)}
            disabled={!isDraft}
            maxLength={200}
          />
          {isDraft && (
            <Button variant="outline" size="sm" onClick={saveName} className="self-start" disabled={!name.trim()}>
              บันทึกชื่อ
            </Button>
          )}
        </Card>

        {/* Steps */}
        <div className="flex items-center justify-between">
          <h2 className="text-sm font-semibold text-ink">ขั้นตอนการเซ็น ({steps.length})</h2>
          {isDraft && <Button size="sm" variant="outline" onClick={addStep}>+ เพิ่มขั้นตอน</Button>}
        </div>

        {steps.length === 0 && (
          <Card><p className="text-sm text-subtle text-center py-2">ยังไม่มีขั้นตอน — กด “เพิ่มขั้นตอน”</p></Card>
        )}

        {steps.map((s, idx) => (
          <Card key={s.key} className="flex flex-col gap-3">
            <div className="flex items-center justify-between gap-2">
              <span className="inline-flex items-center justify-center w-7 h-7 rounded-full bg-brand text-white text-sm font-bold">{idx + 1}</span>
              {isDraft && (
                <div className="flex items-center gap-1">
                  <Button variant="ghost" size="sm" onClick={() => move(idx, -1)} disabled={idx === 0} aria-label="เลื่อนขึ้น">↑</Button>
                  <Button variant="ghost" size="sm" onClick={() => move(idx, 1)} disabled={idx === steps.length - 1} aria-label="เลื่อนลง">↓</Button>
                  <Button variant="ghost" size="sm" onClick={() => removeStep(s.key)} className="text-danger-fg" aria-label="ลบ">ลบ</Button>
                </div>
              )}
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <Input label="รหัสตำแหน่ง" value={s.position_code} disabled={!isDraft}
                onChange={(e) => patchStep(s.key, { position_code: e.target.value.toUpperCase() })}
                placeholder="MAKER" maxLength={50} />
              <Input label="ชื่อตำแหน่ง" value={s.position_name} disabled={!isDraft}
                onChange={(e) => patchStep(s.key, { position_name: e.target.value })}
                placeholder="ผู้จัดทำ" maxLength={200} />
            </div>

            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">เงื่อนไข</label>
              <select
                value={s.condition_type}
                disabled={!isDraft}
                onChange={(e) => patchStep(s.key, { condition_type: Number(e.target.value), ...(Number(e.target.value) === 3 ? { assignee_user_ids: [] } : {}) })}
                className="h-11 px-3 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400 disabled:bg-surface-muted disabled:text-muted"
              >
                {CONDITIONS.map((c) => <option key={c.value} value={c.value}>{c.label}</option>)}
              </select>
            </div>

            {/* Assignees — hidden for external (condition 3) */}
            {s.condition_type !== 3 && (
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-ink">ผู้เซ็น ({s.assignee_user_ids.length})</label>
                <div className="flex flex-col gap-1 max-h-52 overflow-y-auto border border-line rounded-md p-2 bg-surface-muted">
                  {users.map((u) => {
                    const checked = s.assignee_user_ids.includes(Number(u.id));
                    return (
                      <label key={u.id} className={`flex items-center gap-2 px-2 py-1.5 rounded ${isDraft ? "cursor-pointer hover:bg-surface" : "opacity-70"}`}>
                        <input type="checkbox" checked={checked} disabled={!isDraft}
                          onChange={() => toggleAssignee(s.key, Number(u.id))}
                          className="w-4 h-4 rounded border-line-strong accent-brand-600" />
                        <span className="text-sm text-ink">{u.display_name} <span className="text-subtle">({u.username})</span></span>
                      </label>
                    );
                  })}
                </div>
              </div>
            )}
            {s.condition_type === 3 && (
              <p className="text-xs text-muted">ผู้เซ็นภายนอกถูกเชิญตอนนำเข้าเอกสาร — ไม่ต้องกำหนดที่นี่</p>
            )}
          </Card>
        ))}

        {/* Actions */}
        {isDraft && (
          <div className="flex gap-2 sticky bottom-0 bg-bg/90 backdrop-blur py-3">
            <Button onClick={saveSteps} loading={saving} block>บันทึกขั้นตอน</Button>
            <Button onClick={publish} variant="outline" loading={saving} disabled={steps.length === 0} block
              className="border-success/40 text-success-fg">เผยแพร่</Button>
          </div>
        )}
      </div>
    </main>
  );
}
