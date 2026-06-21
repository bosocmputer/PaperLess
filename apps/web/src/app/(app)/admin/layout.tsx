"use client";

import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { api } from "@/lib/api";
import { getAccessToken, getUser, clearSession, getRefreshToken } from "@/lib/auth";

// Phase C will add { href: "/admin/users", label: "ผู้ใช้", systemAdminOnly: true }.
// Held back until the page exists so system_admin doesn't get a 404 link.
const NAV_LINKS = [
  { href: "/admin/documents", label: "เอกสาร" },
  { href: "/admin/workflows", label: "ตั้งค่า Workflow" },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [displayName, setDisplayName] = useState("");
  const [loggingOut, setLoggingOut] = useState(false);

  useEffect(() => {
    const user = getUser<{ display_name: string }>();
    if (!user) return;
    setDisplayName(user.display_name);
  }, []);

  const handleLogout = async () => {
    const token = getAccessToken();
    const refresh = getRefreshToken();
    if (!token || !refresh) {
      clearSession();
      router.replace("/login");
      return;
    }
    setLoggingOut(true);
    try {
      await api.logout(token, refresh);
    } finally {
      clearSession();
      router.replace("/login");
    }
  };

  return (
    <div className="min-h-screen flex flex-col">
      {/* Top nav */}
      <nav className="bg-surface border-b border-line sticky top-0 z-20">
        <div className="max-w-3xl mx-auto px-4 py-0 flex items-center gap-1 h-12">
          {/* Logo / brand */}
          <span className="text-sm font-bold text-brand-700 mr-3 flex-shrink-0 tracking-tight">PaperLess</span>

          {/* Nav links */}
          <div className="flex items-center gap-1 flex-1 overflow-x-auto scrollbar-none">
            {NAV_LINKS.map((link) => {
              const active = pathname.startsWith(link.href);
              return (
                <button
                  key={link.href}
                  type="button"
                  onClick={() => router.push(link.href)}
                  className={`px-3 py-1.5 rounded-md text-sm font-medium whitespace-nowrap transition-colors ${
                    active
                      ? "bg-brand-50 text-brand-700"
                      : "text-muted hover:bg-surface-muted"
                  }`}
                >
                  {link.label}
                </button>
              );
            })}
          </div>

          {/* User info + logout */}
          <div className="flex items-center gap-2 flex-shrink-0 ml-2">
            {displayName && (
              <span className="text-xs text-muted hidden sm:inline max-w-[100px] truncate">
                {displayName}
              </span>
            )}
            <button
              type="button"
              onClick={handleLogout}
              disabled={loggingOut}
              className="text-xs text-muted px-2 py-1 border border-line rounded-md hover:bg-surface-muted disabled:opacity-40"
            >
              {loggingOut ? "..." : "ออกจากระบบ"}
            </button>
          </div>
        </div>
      </nav>

      {/* Page content */}
      {children}
    </div>
  );
}
