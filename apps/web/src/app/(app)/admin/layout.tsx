"use client";

import { useEffect, useState } from "react";
import { usePathname, useRouter } from "next/navigation";
import { api } from "@/lib/api";
import { getAccessToken, getUser, clearSession, getRefreshToken } from "@/lib/auth";
import { Icon } from "@/components/ui";
import { cn } from "@/lib/cn";

const BASE_NAV = [
  { href: "/admin/dashboard", label: "แดชบอร์ด", icon: "dashboard" as const },
  { href: "/admin/documents", label: "เอกสาร", icon: "file" as const },
  { href: "/admin/workflows", label: "Workflow", icon: "workflow" as const },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [displayName, setDisplayName] = useState("");
  const [username, setUsername] = useState("");
  const [isSysAdmin, setIsSysAdmin] = useState(false);
  const [loggingOut, setLoggingOut] = useState(false);

  useEffect(() => {
    const user = getUser<{ display_name: string; username: string; roles: string[] }>();
    if (!user) return;
    setDisplayName(user.display_name ?? "");
    setUsername(user.username ?? "");
    setIsSysAdmin((user.roles ?? []).includes("system_admin"));
  }, []);

  const NAV_LINKS = isSysAdmin
    ? [...BASE_NAV, { href: "/admin/users", label: "ผู้ใช้", icon: "users" as const }]
    : BASE_NAV;

  const handleLogout = async () => {
    const token = getAccessToken();
    const refresh = getRefreshToken();
    if (!token || !refresh) { clearSession(); router.replace("/login"); return; }
    setLoggingOut(true);
    try { await api.logout(token, refresh); } finally { clearSession(); router.replace("/login"); }
  };

  const initial = displayName.charAt(0).toUpperCase() || "A";

  const NavLinks = () => (
    <nav className="flex flex-col gap-0.5 px-3 py-2">
      {NAV_LINKS.map((link) => {
        const active = pathname === link.href || (link.href !== "/admin/dashboard" && pathname.startsWith(link.href));
        return (
          <button
            key={link.href}
            type="button"
            onClick={() => { router.push(link.href); setSidebarOpen(false); }}
            className={cn(
              "w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-[14px] font-medium transition-colors text-left",
              active
                ? "bg-brand text-white shadow-sm"
                : "text-muted hover:bg-surface-muted hover:text-ink"
            )}
          >
            <Icon name={link.icon} size={18} />
            {link.label}
          </button>
        );
      })}
    </nav>
  );

  const SidebarInner = () => (
    <div className="flex flex-col h-full">
      {/* Brand */}
      <div className="px-5 py-5 border-b border-line">
        <p className="text-base font-bold text-brand-700 tracking-tight leading-none">PaperLess</p>
        <p className="text-[11px] text-subtle mt-1">ระบบเซ็นเอกสารอิเล็กทรอนิกส์</p>
      </div>

      {/* Navigation */}
      <div className="flex-1 overflow-y-auto py-2">
        <NavLinks />
      </div>

      {/* User + logout */}
      <div className="px-3 py-3 border-t border-line">
        <div className="flex items-center gap-3 px-3 py-2 mb-1">
          <div className="w-8 h-8 rounded-full bg-brand-100 text-brand-700 flex items-center justify-center text-sm font-bold flex-shrink-0 select-none">
            {initial}
          </div>
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-semibold text-ink truncate leading-tight">{displayName || "Admin"}</p>
            {username && <p className="text-[11px] text-subtle truncate">@{username}</p>}
          </div>
        </div>
        <button
          type="button"
          onClick={handleLogout}
          disabled={loggingOut}
          className="w-full flex items-center gap-3 px-3 py-2.5 rounded-lg text-[13px] font-medium text-muted hover:bg-surface-muted hover:text-ink transition-colors disabled:opacity-40"
        >
          <Icon name="logout" size={17} />
          {loggingOut ? "กำลังออก..." : "ออกจากระบบ"}
        </button>
      </div>
    </div>
  );

  return (
    <div className="flex min-h-screen bg-bg">
      {/* Desktop sidebar — fixed, always visible */}
      <aside className="hidden lg:flex flex-col w-56 xl:w-60 flex-shrink-0 bg-surface border-r border-line sticky top-0 h-screen overflow-hidden">
        <SidebarInner />
      </aside>

      {/* Mobile sidebar drawer */}
      {sidebarOpen && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <div
            className="absolute inset-0 bg-black/40 backdrop-blur-[2px]"
            onClick={() => setSidebarOpen(false)}
          />
          <aside className="absolute left-0 top-0 h-full w-60 bg-surface shadow-pop flex flex-col">
            <SidebarInner />
          </aside>
        </div>
      )}

      {/* Content column */}
      <div className="flex-1 flex flex-col min-w-0">
        {/* Mobile top bar */}
        <header className="lg:hidden bg-surface border-b border-line flex items-center gap-3 px-4 h-14 sticky top-0 z-30 flex-shrink-0">
          <button
            type="button"
            onClick={() => setSidebarOpen(true)}
            className="touch-target -ml-2 flex items-center justify-center px-2 text-muted"
            aria-label="เปิดเมนู"
          >
            <Icon name="menu" size={22} />
          </button>
          <p className="text-base font-bold text-brand-700 flex-1 tracking-tight">PaperLess</p>
        </header>

        {/* Page content */}
        {children}
      </div>
    </div>
  );
}
