"use client";

import { useEffect, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { api, type AdminUser, type RoleOption } from "@/lib/api";
import { getAccessToken, getUser } from "@/lib/auth";
import ErrorState from "@/components/ErrorState";
import { Badge, Button, Icon, Input, Spinner } from "@/components/ui";

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
    setLoading(true); setError(null);
    try {
      const [uRes, rRes] = await Promise.all([api.listAdminUsers(token, inc), api.listRoles(token)]);
      if (!uRes.success) { setError(uRes.error.code); return; }
      if (rRes.success) setRoles(rRes.data ?? []);
      setUsers(uRes.data ?? []);
    } catch { setError("network_error"); } finally { setLoading(false); }
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
    setSubmitting(true); setFormErr(null);
    const res = form.mode === "create"
      ? await api.createUser(token, { username: form.username.trim(), display_name: form.display_name.trim(),
          email: form.email.trim() || undefined, phone: form.phone.trim() || undefined,
          roles: form.roles, password: form.password || undefined })
      : await api.updateUser(token, form.id!, { display_name: form.display_name.trim(),
          email: form.email.trim() || undefined, phone: form.phone.trim() || undefined,
          status: form.status, roles: form.roles, password: form.password || undefined });
    setSubmitting(false);
    if (!res.success) { setFormErr(res.error.message || res.error.code); return; }
    setForm(null);
    load(includeInactive);
  };

  return (
    <div className="min-h-screen">
      {/* Header */}
      <div className="bg-surface border-b border-line px-6 py-4 sticky top-14 lg:top-0 z-10">
        <div className="max-w-6xl mx-auto flex items-center justify-between gap-3">
          <div>
            <h1 className="text-xl font-bold text-ink">จัดการผู้ใช้</h1>
            {!loading && !error && (
              <p className="text-sm text-muted mt-0.5">ทั้งหมด {users.length} คน ในระบบ PaperLess</p>
            )}
          </div>
          <Button size="sm" onClick={openCreate}>
            <Icon name="plus" size={15} />
            เพิ่มผู้ใช้
          </Button>
        </div>
      </div>

      <div className="max-w-6xl mx-auto px-6 py-5 flex flex-col gap-4">
        {/* Toolbar */}
        <div className="flex items-center justify-end">
          <label className="flex items-center gap-2 text-sm text-muted cursor-pointer select-none">
            <input
              type="checkbox"
              checked={includeInactive}
              onChange={(e) => setIncludeInactive(e.target.checked)}
              className="w-4 h-4 rounded border-line-strong accent-brand-600"
            />
            แสดงผู้ใช้ที่ปิดใช้งานด้วย
          </label>
        </div>

        {loading && (
          <div className="flex justify-center py-20 text-brand"><Spinner size="md" /></div>
        )}
        {!loading && error && <ErrorState code={error} onRetry={() => load(includeInactive)} />}
        {!loading && !error && users.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-subtle gap-3">
            <Icon name="users" size={40} className="opacity-30" />
            <p className="text-sm">ไม่พบผู้ใช้</p>
          </div>
        )}

        {/* Table */}
        {!loading && !error && users.length > 0 && (
          <div className="bg-surface border border-line rounded-xl shadow-card overflow-hidden">
            {/* Desktop */}
            <div className="hidden md:block overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="border-b border-line bg-surface-muted">
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">ผู้ใช้</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">อีเมล</th>
                    <th className="text-left px-4 py-3 text-xs font-semibold text-muted uppercase tracking-wide">บทบาท</th>
                    <th className="px-4 py-3" />
                  </tr>
                </thead>
                <tbody className="divide-y divide-line">
                  {users.map((u) => (
                    <tr key={u.id} className="hover:bg-surface-muted/50 transition-colors">
                      <td className="px-4 py-3.5">
                        <div className="flex items-center gap-3">
                          <div className="w-9 h-9 rounded-full bg-brand-100 text-brand-700 flex items-center justify-center text-sm font-bold flex-shrink-0 select-none">
                            {u.display_name.charAt(0).toUpperCase()}
                          </div>
                          <div className="min-w-0">
                            <p className="font-semibold text-ink truncate">{u.display_name}</p>
                            <p className="text-xs text-subtle">@{u.username}</p>
                          </div>
                          {u.status === "inactive" && (
                            <Badge tone="neutral">ปิดใช้งาน</Badge>
                          )}
                        </div>
                      </td>
                      <td className="px-4 py-3.5 text-muted truncate max-w-[200px]">
                        {u.email || <span className="text-subtle">—</span>}
                      </td>
                      <td className="px-4 py-3.5">
                        <div className="flex gap-1.5 flex-wrap">
                          {u.roles.length === 0
                            ? <span className="text-xs text-subtle">ไม่มีบทบาท</span>
                            : u.roles.map((r) => <Badge key={r} tone="info">{roleName(r)}</Badge>)}
                        </div>
                      </td>
                      <td className="px-4 py-3.5 text-right">
                        <Button variant="outline" size="sm" onClick={() => openEdit(u)}>
                          <Icon name="edit" size={14} />
                          แก้ไข
                        </Button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            {/* Mobile cards */}
            <div className="md:hidden divide-y divide-line">
              {users.map((u) => (
                <div key={u.id} className="px-4 py-4 flex items-start justify-between gap-3">
                  <div className="flex items-start gap-3 flex-1 min-w-0">
                    <div className="w-9 h-9 rounded-full bg-brand-100 text-brand-700 flex items-center justify-center text-sm font-bold flex-shrink-0 select-none">
                      {u.display_name.charAt(0).toUpperCase()}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <p className="font-semibold text-ink truncate">{u.display_name}</p>
                        {u.status === "inactive" && <Badge tone="neutral">ปิดใช้งาน</Badge>}
                      </div>
                      <p className="text-xs text-subtle">@{u.username}</p>
                      {u.email && <p className="text-xs text-muted mt-0.5 truncate">{u.email}</p>}
                      <div className="flex gap-1.5 flex-wrap mt-2">
                        {u.roles.length === 0
                          ? <span className="text-xs text-subtle">ไม่มีบทบาท</span>
                          : u.roles.map((r) => <Badge key={r} tone="info">{roleName(r)}</Badge>)}
                      </div>
                    </div>
                  </div>
                  <Button variant="outline" size="sm" onClick={() => openEdit(u)}>แก้ไข</Button>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>

      {/* Create/edit modal */}
      {form && (
        <div className="fixed inset-0 z-50 flex items-end sm:items-center justify-center">
          <div className="absolute inset-0 bg-black/40" onClick={() => !submitting && setForm(null)} />
          <div className="relative bg-surface rounded-t-2xl sm:rounded-2xl w-full sm:max-w-md max-h-[90vh] overflow-y-auto shadow-pop p-5 flex flex-col gap-4">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-bold text-ink">{form.mode === "create" ? "เพิ่มผู้ใช้" : "แก้ไขผู้ใช้"}</h2>
              <button onClick={() => setForm(null)} className="text-subtle hover:text-ink touch-target -mr-2 px-2 flex items-center">
                <Icon name="x" size={20} />
              </button>
            </div>

            {form.mode === "create" ? (
              <Input label="ชื่อผู้ใช้ (username) *" value={form.username}
                onChange={(e) => setForm({ ...form, username: e.target.value })} placeholder="เช่น somchai" maxLength={100} />
            ) : (
              <div className="text-sm bg-surface-muted rounded-lg px-3 py-2.5">
                <span className="text-muted">ชื่อผู้ใช้: </span>
                <span className="font-medium text-ink">@{form.username}</span>
              </div>
            )}

            <Input label="ชื่อที่แสดง *" value={form.display_name}
              onChange={(e) => setForm({ ...form, display_name: e.target.value })} maxLength={200} />

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <Input label="อีเมล" type="email" value={form.email}
                onChange={(e) => setForm({ ...form, email: e.target.value })} />
              <Input label="เบอร์โทร" type="tel" value={form.phone}
                onChange={(e) => setForm({ ...form, phone: e.target.value })} />
            </div>

            {form.mode === "edit" && (
              <div className="flex flex-col gap-1.5">
                <label className="text-sm font-medium text-ink">สถานะ</label>
                <select value={form.status} onChange={(e) => setForm({ ...form, status: e.target.value })}
                  className="h-11 px-3 rounded-lg border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400">
                  <option value="active">ใช้งาน</option>
                  <option value="inactive">ปิดใช้งาน</option>
                </select>
              </div>
            )}

            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">บทบาท (roles)</label>
              <div className="flex flex-col gap-0.5 border border-line rounded-lg p-2 bg-surface-muted">
                {roles.map((r) => (
                  <label key={r.code} className="flex items-center gap-2 px-2 py-2 rounded-md cursor-pointer hover:bg-surface">
                    <input type="checkbox" checked={form.roles.includes(r.code)} onChange={() => toggleRole(r.code)}
                      className="w-4 h-4 rounded border-line-strong accent-brand-600" />
                    <span className="text-sm text-ink">{r.name} <span className="text-subtle">({r.code})</span></span>
                  </label>
                ))}
              </div>
            </div>

            <Input
              label={form.mode === "create" ? "รหัสผ่าน (ไม่บังคับ — ตั้งทีหลังได้)" : "ตั้งรหัสผ่านใหม่ (เว้นว่างถ้าไม่เปลี่ยน)"}
              type="password" value={form.password}
              onChange={(e) => setForm({ ...form, password: e.target.value })}
              hint="ถ้าไม่ตั้งรหัสผ่าน ผู้ใช้จะยังเข้าสู่ระบบไม่ได้จนกว่าจะตั้ง"
              autoComplete="new-password" />

            {formErr && (
              <div className="flex items-start gap-2 bg-danger-bg border border-danger/30 rounded-lg px-3 py-2.5">
                <Icon name="exclamation-triangle" size={16} className="text-danger-fg flex-shrink-0 mt-0.5" />
                <p className="text-sm text-danger-fg">{formErr}</p>
              </div>
            )}

            <div className="flex justify-end gap-2 pt-1">
              <Button variant="outline" onClick={() => setForm(null)} disabled={submitting}>ยกเลิก</Button>
              <Button onClick={save} loading={submitting}>{form.mode === "create" ? "สร้างผู้ใช้" : "บันทึก"}</Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
