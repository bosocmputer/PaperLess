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
    <div className="min-h-screen bg-gray-50 flex flex-col">
      {/* Top nav */}
      <nav className="bg-white border-b border-gray-200 sticky top-0 z-20">
        <div className="max-w-3xl mx-auto px-4 py-0 flex items-center gap-1 h-12">
          {/* Logo / brand */}
          <span className="text-sm font-bold text-blue-700 mr-3 flex-shrink-0">PaperLess</span>

          {/* Nav links */}
          <div className="flex items-center gap-1 flex-1 overflow-x-auto scrollbar-none">
            {NAV_LINKS.map((link) => {
              const active = pathname.startsWith(link.href);
              return (
                <button
                  key={link.href}
                  type="button"
                  onClick={() => router.push(link.href)}
                  className={`px-3 py-1.5 rounded-lg text-sm font-medium whitespace-nowrap transition-colors ${
                    active
                      ? "bg-blue-50 text-blue-700"
                      : "text-gray-600 hover:bg-gray-100"
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
              <span className="text-xs text-gray-500 hidden sm:inline max-w-[100px] truncate">
                {displayName}
              </span>
            )}
            <button
              type="button"
              onClick={handleLogout}
              disabled={loggingOut}
              className="text-xs text-gray-500 px-2 py-1 border border-gray-200 rounded-lg hover:bg-gray-50 disabled:opacity-40"
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
