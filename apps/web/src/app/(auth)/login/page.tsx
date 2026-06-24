"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { saveSession } from "@/lib/auth";
import { Button, Icon, Input } from "@/components/ui";

const ADMIN_ROLES = ["system_admin", "workflow_admin", "document_admin", "auditor"];

function landingFor(roles: string[]): string {
  return roles.some((r) => ADMIN_ROLES.includes(r)) ? "/admin/documents" : "/inbox";
}

export default function LoginPage() {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const result = await api.login(username, password);
      if (!result.success) {
        setError(
          result.error.code === "invalid_credentials"
            ? "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง"
            : result.error.message
        );
        return;
      }
      saveSession(result.data.access_token, result.data.refresh_token, result.data.user);
      router.replace(landingFor(result.data.user.roles ?? []));
    } catch {
      setError("ไม่สามารถเชื่อมต่อกับเซิร์ฟเวอร์ได้");
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="min-h-screen bg-bg flex flex-col items-center justify-center px-4 py-12">
      <div className="w-full max-w-sm">
        {/* Brand */}
        <div className="text-center mb-8">
          <div className="inline-flex items-center justify-center w-14 h-14 rounded-2xl bg-brand mb-4">
            <Icon name="document-check" size={28} className="text-white" />
          </div>
          <h1 className="text-2xl font-bold text-ink tracking-tight">PaperLess</h1>
          <p className="text-sm text-muted mt-1">ระบบเซ็นเอกสารอิเล็กทรอนิกส์</p>
        </div>

        {/* Login card */}
        <div className="bg-surface rounded-2xl shadow-card border border-line p-6">
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <Input
              label="ชื่อผู้ใช้"
              type="text"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              required
              placeholder="username"
            />

            <div className="flex flex-col gap-1.5">
              <label className="text-sm font-medium text-ink">รหัสผ่าน</label>
              <div className="relative">
                <input
                  type={showPassword ? "text" : "password"}
                  autoComplete="current-password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  required
                  placeholder="••••••••"
                  className="w-full h-11 px-3 pr-11 rounded-md bg-surface text-ink placeholder:text-subtle border border-line-strong transition-colors focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((v) => !v)}
                  className="absolute right-0 top-0 h-11 w-11 flex items-center justify-center text-subtle hover:text-muted"
                  tabIndex={-1}
                  aria-label={showPassword ? "ซ่อนรหัสผ่าน" : "แสดงรหัสผ่าน"}
                >
                  <Icon name="eye" size={18} />
                </button>
              </div>
            </div>

            {error && (
              <div className="flex items-start gap-2 bg-danger-bg border border-danger/30 rounded-lg px-3 py-2.5">
                <Icon name="exclamation-triangle" size={16} className="text-danger-fg flex-shrink-0 mt-0.5" />
                <p className="text-sm text-danger-fg">{error}</p>
              </div>
            )}

            <Button type="submit" loading={loading} size="lg" block className="mt-1">
              {loading ? "กำลังเข้าสู่ระบบ..." : "เข้าสู่ระบบ"}
            </Button>
          </form>
        </div>

        {/* Trust badges */}
        <div className="mt-6 grid grid-cols-3 gap-3">
          {[
            { icon: "shield" as const, label: "ความปลอดภัยขั้นสูง" },
            { icon: "check-circle" as const, label: "เชื่อถือได้ตามกฎหมาย" },
            { icon: "cloud" as const, label: "สำรองข้อมูลบนคลาวด์" },
          ].map((b) => (
            <div key={b.label} className="flex flex-col items-center gap-1.5 bg-surface rounded-xl border border-line p-3 text-center shadow-card">
              <Icon name={b.icon} size={18} className="text-brand-600" />
              <p className="text-[11px] text-muted leading-tight">{b.label}</p>
            </div>
          ))}
        </div>

        <p className="text-center text-xs text-subtle mt-6">
          © {new Date().getFullYear()} PaperLess · ระบบลายมือชื่ออิเล็กทรอนิกส์
        </p>
      </div>
    </main>
  );
}
