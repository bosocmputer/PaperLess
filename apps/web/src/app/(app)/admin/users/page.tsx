"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type AdminUser, type RoleOption } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Button, Card, Input, Spinner, Badge } from "@/components/ui";

function isSystemAdmin(roles: string[]): boolean {
  return roles.includes("system_admin");
}

interface FormState {
  mode: "create" | "edit";
  id?: string;
  username: string;
  display_name: string;
  email: string;
  phone: string;
  status: string;
  roles: string[];
  password: string;
}

const emptyForm = (): FormState => ({
  mode: "create", username: "", display_name: "", email: "", phone: "",
  status: "active", roles: [], password: "",
});

export default function AdminUsersPage() {
  const router = useRouter();
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [roles, setRoles] = useState<RoleOption[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [includeInactive, setIncludeInactive] = useState(false);

  const [form, setForm] = useState<FormState | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [formErr, setFormErr] = useState<string | null>(null);

  const roleName = (code: string) => roles.find((r) => r.code === code)?.name ?? code;

  const load = useCallback(async (inc: boolean) => {
    const token = getAccessToken();
    if (!token) { router.replace("/login"); return; }
    if (!isSystemAdmin(getUser<{ roles: string[] }>()?.roles ?? [])) { router.replace("/admin/documents"); return; }
    setLoading(true);
    setError(null);
    try {
      const [uRes, rRes] = await Promise.all([api.listAdminUsers(token, inc), api.listRoles(token)]);
      if (!uRes.success) { setError(uRes.error.code); return; }
      if (rRes.success) setRoles(rRes.data ?? []);
      setUsers(uRes.data ?? []);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [router]);

  useEffect(() => { load(includeInactive); }, [load, includeInactive]);

  const openCreate = () => { setFormErr(null); setForm(emptyForm()); };
  const openEdit = (u: AdminUser) => {
    setFormErr(null);
    setForm({ mode: "edit", id: u.id, username: u.username, display_name: u.display_name,
      email: u.email, phone: u.phone, status: u.status, roles: [...u.roles], password: "" });
  };

  const toggleRole = (code: string) =>
    setForm((f) => f && ({ ...f, roles: f.roles.includes(code) ? f.roles.filter((r) => r !== code) : [...f.roles, code] }));

  const save = async () => {
    if (!form) return;
    const token = getAccessToken();
    if (!token) return;
    if (form.mode === "create" && !form.username.trim()) { setFormErr("กรอกชื่อผู้ใช้ (username)"); return; }
    if (!form.display_name.trim()) { setFormErr("กรอกชื่อที่แสดง"); return; }
    if (form.password && (form.password.length < 6 || form.password.length > 72)) {
      setFormErr("รหัสผ่านต้อง 6–72 ตัวอักษร"); return;
    }
    setSubmitting(true);
    setFormErr(null);
    const res = form.mode === "create"
      ? await api.createUser(token, {
          username: form.username.trim(), display_name: form.display_name.trim(),
          email: form.email.trim() || undefined, phone: form.phone.trim() || undefined,
          roles: form.roles, password: form.password || undefined,
        })
      : await api.updateUser(token, form.id!, {
          display_name: form.display_name.trim(), email: form.email.trim() || undefined,
          phone: form.phone.trim() || undefined, status: form.status, roles: form.roles,
          password: form.password || undefined,
        });
    setSubmitting(false);
    if (!res.success) { setFormErr(res.error.message || res.error.code); return; }
    setForm(null);
    load(includeInactive);
  };

  return (
    <main className="min-h-screen">
      <header className="bg-surface border-b border-line px-4 py-4 sticky top-12 z-10">
        <div className="max-w-3xl mx-auto flex items-center justify-between gap-2">
          <div>
            <h1 className="text-lg font-bold text-ink">จัดการผู้ใช้</h1>
            {!loading && !error && <p className="text-sm text-muted">{users.length} คน</p>}
          </div>
          <Button size="sm" onClick={openCreate}>+ เพิ่มผู้ใช้</Button>
        </div>
      </header>

      <div className="max-w-3xl mx-auto px-4 py-4 flex flex-col gap-3">
        <label className="flex items-center gap-2 text-sm text-muted self-end cursor-pointer">
          <input type="checkbox" checked={includeInactive} onChange={(e) => setIncludeInactive(e.target.checked)}
            className="w-4 h-4 rounded border-line-strong accent-brand-600" />
          แสดงผู้ใช้ที่ปิดใช้งานด้วย
        </label>

        {loading && <div className="flex justify-center py-16 text-brand"><Spinner size="md" /></div>}
        {!loading && error && <ErrorState code={error} onRetry={() => load(includeInactive)} />}
        {!loading && !error && users.length === 0 && (
          <div className="text-center text-sm text-subtle py-16">ไม่พบผู้ใช้</div>
        )}

        {!loading && !error && users.map((u) => (
          <Card key={u.id} className="flex items-start justify-between gap-3">
            <div className="min-w-0 flex-1">
              <div className="flex items-center gap-2 flex-wrap">
                <p className="font-semibold text-ink truncate">{u.display_name}</p>
                <span className="text-xs text-muted">@{u.username}</span>
                {u.status === "inactive" && <Badge tone="neutral">ปิดใช้งาน</Badge>}
              </div>
              {(u.email || u.phone) && (
                <p className="text-xs text-muted mt-0.5 truncate">{[u.email, u.phone].filter(Boolean).join(" · ")}</p>
              )}
              <div className="flex gap-1.5 flex-wrap mt-2">
                {u.roles.length === 0
                  ? <span className="text-xs text-subtle">ไม่มีบทบาท</span>
                  : u.roles.map((r) => <Badge key={r} tone="info">{roleName(r)}</Badge>)}
              </div>
            </div>
            <Button variant="outline" size="sm" onClick={() => openEdit(u)}>แก้ไข</Button>
          </Card>
        ))}
      </div>

      {/* Create / edit dialog */}
      {form && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={() => !submitting && setForm(null)} />
          <div className="relative bg-surface rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-pop p-5 flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-bold text-ink">{form.mode === "create" ? "เพิ่มผู้ใช้" : "แก้ไขผู้ใช้"}</h2>
              <button onClick={() => setForm(null)} className="text-subtle text-2xl leading-none touch-target -mr-2 px-2">×</button>
            </div>

            {form.mode === "create" ? (
              <Input label="ชื่อผู้ใช้ (username) *" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="เช่น somchai" maxLength={100} />
            ) : (
              <div className="text-sm"><span className="text-muted">ชื่อผู้ใช้: </span><span className="font-medium text-ink">@{form.username}</span></div>
            )}

            <Input label="ชื่อที่แสดง *" value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })} maxLength={200} />
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <Input label="อีเมล" type="email" value={form.email} onChange={(e) => setForm({ ...form, email: e.target.value })} />
              <Input label="เบอร์โทร" type="tel" value={form.phone} onChange={(e) => setForm({ ...form, phone: e.target.value })} />
            </div>

            {form.mode === "edit" && (
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-ink">สถานะ</label>
                <select value={form.status} onChange={(e) => setForm({ ...form, status: e.target.value })}
                  className="h-11 px-3 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400">
                  <option value="active">ใช้งาน</option>
                  <option value="inactive">ปิดใช้งาน</option>
                </select>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">บทบาท (roles)</label>
              <div className="flex flex-col gap-1 border border-line rounded-md p-2 bg-surface-muted">
                {roles.map((r) => (
                  <label key={r.code} className="flex items-center gap-2 px-2 py-1.5 rounded cursor-pointer hover:bg-surface">
                    <input type="checkbox" checked={form.roles.includes(r.code)} onChange={() => toggleRole(r.code)}
                      className="w-4 h-4 rounded border-line-strong accent-brand-600" />
                    <span className="text-sm text-ink">{r.name} <span className="text-subtle">({r.code})</span></span>
                  </label>
                ))}
              </div>
            </div>

            <Input label={form.mode === "create" ? "รหัสผ่าน (ไม่บังคับ — ตั้งทีหลังได้)" : "ตั้งรหัสผ่านใหม่ (เว้นว่างถ้าไม่เปลี่ยน)"}
              type="password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })}
              hint="ถ้าไม่ตั้งรหัสผ่าน ผู้ใช้จะยังเข้าสู่ระบบไม่ได้จนกว่าจะตั้ง" autoComplete="new-password" />

            {formErr && <p className="text-sm text-danger-fg bg-danger-bg border border-danger/30 rounded-md px-3 py-2">{formErr}</p>}

            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setForm(null)} disabled={submitting}>ยกเลิก</Button>
              <Button onClick={save} loading={submitting}>{form.mode === "create" ? "สร้างผู้ใช้" : "บันทึก"}</Button>
            </div>
          </div>
        </div>
      )}
    </main>
  );
}
