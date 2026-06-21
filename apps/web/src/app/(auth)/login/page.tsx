"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { saveSession } from "@/lib/auth";
import { Button, Card, Input } from "@/components/ui";

// Roles that land on the admin dashboard rather than the signer inbox.
// Keep in sync with the root redirect (src/app/page.tsx).
const ADMIN_ROLES = ["system_admin", "workflow_admin", "document_admin", "auditor"];

function landingFor(roles: string[]): string {
  return roles.some((r) => ADMIN_ROLES.includes(r)) ? "/admin/documents" : "/inbox";
}

export default function LoginPage() {
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const result = await api.login(username, password);
      if (!result.success) {
        setError(result.error.code === "invalid_credentials"
          ? "ชื่อผู้ใช้หรือรหัสผ่านไม่ถูกต้อง"
          : result.error.message);
        return;
      }
      saveSession(result.data.access_token, result.data.refresh_token, result.data.user);
      // Role-based landing: admins/auditors → dashboard, signers → inbox.
      router.replace(landingFor(result.data.user.roles ?? []));
    } catch {
      setError("ไม่สามารถเชื่อมต่อกับเซิร์ฟเวอร์ได้");
    } finally {
      setLoading(false);
    }
  };

  return (
    <main className="min-h-screen flex items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="text-center mb-8">
          <h1 className="text-2xl font-bold text-brand-700 tracking-tight">PaperLess</h1>
          <p className="text-sm text-muted mt-1">ระบบเซ็นเอกสารอิเล็กทรอนิกส์</p>
        </div>

        <Card padding="lg" className="rounded-2xl">
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
            <Input
              label="รหัสผ่าน"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              placeholder="••••••••"
            />

            {error && (
              <p className="text-sm text-danger-fg bg-danger-bg border border-danger/30 rounded-md px-3 py-2">{error}</p>
            )}

            <Button type="submit" loading={loading} size="lg" block>
              {loading ? "กำลังเข้าสู่ระบบ..." : "เข้าสู่ระบบ"}
            </Button>
          </form>
        </Card>
      </div>
    </main>
  );
}
