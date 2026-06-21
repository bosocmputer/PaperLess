"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { getAccessToken, getUser } from "@/lib/auth";
import { Spinner } from "@/components/ui";

const ADMIN_ROLES = ["system_admin", "workflow_admin", "document_admin", "auditor"];

function isAdmin(roles: string[]): boolean {
  return roles.some((r) => ADMIN_ROLES.includes(r));
}

export default function RootPage() {
  const router = useRouter();

  useEffect(() => {
    const token = getAccessToken();
    if (!token) {
      router.replace("/login");
      return;
    }
    const user = getUser<{ roles: string[] }>();
    if (isAdmin(user?.roles ?? [])) {
      router.replace("/admin/documents");
    } else {
      router.replace("/inbox");
    }
  }, [router]);

  return (
    <div className="min-h-screen flex items-center justify-center text-brand">
      <Spinner size="md" />
    </div>
  );
}
