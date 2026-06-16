// In browser: use the Next.js proxy (/api/v1 → Go backend), no CORS needed.
// In SSR/build: not used (all fetches are client-side in this PWA).
const BASE = typeof window !== "undefined"
  ? "/api/v1"
  : (process.env.API_INTERNAL_URL ?? "http://localhost:8080") + "/api/v1";

export type ApiResult<T> =
  | { success: true; data: T; meta?: { total: number; page: number; size: number } }
  | { success: false; error: { code: string; message: string } };

async function request<T>(
  path: string,
  opts: RequestInit = {},
  token?: string
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Request-ID": crypto.randomUUID(),
    ...(opts.headers as Record<string, string>),
  };
  if (token) headers["Authorization"] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${path}`, { ...opts, headers });
  const json = await res.json().catch(() => ({ success: false, error: { code: "parse_error", message: "Invalid response" } }));
  return json as ApiResult<T>;
}

export interface User {
  id: number;
  username: string;
  display_name: string;
  roles: string[];
}

export interface LoginResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  user: User;
}

export interface Task {
  id: number;
  document_id: number;
  sequence_no: number;
  condition_type: number;
  status: string;
  opened_at: string | null;
  doc_format_code: string;
  doc_no: string;
  revision: number;
  doc_date: string | null;
  amount: string | null;
}

export interface StepProgress {
  sequence_no: number;
  condition_type: number;
  signed_count: number;
  total_count: number;
  complete: boolean;
}

export const api = {
  login: (username: string, password: string) =>
    request<LoginResponse>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),

  me: (token: string) => request<User>("/auth/me", {}, token),

  logout: (token: string, refresh_token: string) =>
    request("/auth/logout", {
      method: "POST",
      body: JSON.stringify({ refresh_token }),
    }, token),

  inbox: (token: string, page = 1, size = 20) =>
    request<Task[]>(`/signature-tasks/inbox?page=${page}&size=${size}`, {}, token),

  getTask: (token: string, taskId: number) =>
    request<Task>(`/signature-tasks/${taskId}`, {}, token),

  sign: (token: string, taskId: number, signatureImageHash: string, comment: string) =>
    request(`/signature-tasks/${taskId}/sign`, {
      method: "POST",
      body: JSON.stringify({ signature_image_hash: signatureImageHash, comment }),
    }, token),

  reject: (token: string, taskId: number, reason: string) =>
    request(`/signature-tasks/${taskId}/reject`, {
      method: "POST",
      body: JSON.stringify({ reason }),
    }, token),

  getDocument: (token: string, docId: number) =>
    request(`/documents/${docId}`, {}, token),

  workflowStatus: (token: string, docId: number) =>
    request<{ steps: StepProgress[]; total_steps: number; completed_steps: number }>(
      `/documents/${docId}/workflow-status`, {}, token
    ),

  originalPdfUrl: (docId: number) => `${BASE}/documents/${docId}/file/original`,
  finalPdfUrl: (docId: number) => `${BASE}/documents/${docId}/file/final`,
};
