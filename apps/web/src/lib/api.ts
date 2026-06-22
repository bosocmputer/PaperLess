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

// Multipart import: do NOT set Content-Type — browser must set multipart boundary.
async function importRequest<T>(
  path: string,
  token: string,
  formData: FormData
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = {
    "X-Request-ID": crypto.randomUUID(),
    "Authorization": `Bearer ${token}`,
  };
  const res = await fetch(`${BASE}${path}`, { method: "POST", body: formData, headers });
  const json = await res.json().catch(() => ({ success: false, error: { code: "parse_error", message: "Invalid response" } }));
  return json as ApiResult<T>;
}

// External sign requests: token in X-Signer-Token header, never in URL.
async function externalRequest<T>(
  path: string,
  signerToken: string,
  opts: RequestInit = {}
): Promise<ApiResult<T>> {
  const headers: Record<string, string> = {
    "Content-Type": "application/json",
    "X-Request-ID": crypto.randomUUID(),
    "X-Signer-Token": signerToken,
    ...(opts.headers as Record<string, string>),
  };
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

export interface ExternalDocView {
  doc_id: number;
  doc_no: string;
  doc_format_code: string;
  signer_name: string;
  expires_at: string;
  task_id: number;
}

export interface InviteRequest {
  name: string;
  email?: string;
  phone?: string;
  expires_in_hours?: number;
}

export interface InviteResponse {
  external_signer_id: number;
  task_id: number;
  name: string;
  expires_at: string;
  token: string;
}

export interface ExternalSigner {
  id: number;
  name: string;
  email: string | null;
  phone: string | null;
  // Must match external_signers.status CHECK (0001_init.up.sql): pending,signed,expired,cancelled
  status: string;
  expires_at: string;
  otp_verified: boolean;
  created_at: string;
}

// Document list row from GET /documents (admin/auditor only)
export interface DocumentRow {
  id: string;
  doc_format_code: string;
  doc_no: string;
  revision: number;
  // Must match documents.status CHECK (0001_init.up.sql): imported,pending,rejected,completed,cancelled
  status: string;
  // Must match documents.sync_status CHECK: not_required,sync_pending,synced,sync_failed,sync_unknown
  sync_status: string | null;
  amount: string | null;
  doc_date: string | null;
  workflow_version: number;
  created_at: string;
}

// Full document detail from GET /documents/:id (same fields as DocumentRow)
export type DocumentDetail = DocumentRow;

// Audit log entry from GET /documents/:id/audit-logs
export interface AuditEntry {
  id: number;
  actor_type: string | null;
  actor_id: string | null;
  action: string;
  entity_type: string;
  entity_id: string;
  reason: string | null;
  created_at: string;
}

// Signature event from GET /documents/:id/audit-logs
export interface SigEvent {
  id: number;
  task_id: number;
  signer_type: string | null;
  signer_name: string;
  action: string;
  comment: string | null;
  ip_address: string | null;
  signed_at: string;
}

// Workflow template row from GET /workflow-templates
export interface TemplateRow {
  id: string;
  doc_format_code: string;
  name: string;
  version: number;
  // Must match workflow_templates.status CHECK: draft,active,inactive
  status: string;
  effective_from: string;
  created_at: string;
}

// Full template detail from GET /workflow-templates/:id
export interface TemplateAssignee {
  user_id: string;
  username: string;
  display_name: string;
  display_order: number | null;
}

// Signature box position, normalized to the page (0..1), top-left origin. page is 1-based.
export interface SigSlot {
  page: number;
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface TemplateStep {
  id: string;
  sequence_no: number;
  position_code: string;
  position_name: string;
  condition_type: number;
  signature_slot?: SigSlot | null;
  assignees: TemplateAssignee[];
}

export interface TemplateDetail {
  id: string;
  doc_format_code: string;
  name: string;
  version: number;
  status: string;
  effective_from: string;
  created_at: string;
  steps: TemplateStep[];
}

// Active user option for the workflow editor's assignee picker (GET /users)
export interface UserOption {
  id: string;
  username: string;
  display_name: string;
  status: string;
}

// One step in the workflow editor's replace-all payload (PUT /:id/steps).
// assignee_user_ids must be empty for condition_type 3 (external), ≥1 for 1/2.
export interface StepInput {
  position_code: string;
  position_name: string;
  sequence_no: number;
  condition_type: number;
  assignee_user_ids: number[];
  signature_slot?: SigSlot | null;
}

// Full user record for the admin user-management page (GET /admin/users)
export interface AdminUser {
  id: string;
  username: string;
  display_name: string;
  email: string;
  phone: string;
  status: string;
  roles: string[];
}

export interface RoleOption {
  code: string;
  name: string;
}

// Import result from POST /documents/import
export interface ImportResult {
  id: number;
  doc_format_code: string;
  doc_no: string;
  revision: number;
  status: string;
  duplicate: boolean;
}

// Resend response — raw token returned once, stored in useRef never in state
export interface ResendResponse {
  external_signer_id: number;
  name: string;
  expires_at: string;
  token: string;
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

  // signatureImage: base64 PNG (or data URL). Server stores the image + computes the hash.
  sign: (token: string, taskId: number, signatureImage: string, comment: string) =>
    request(`/signature-tasks/${taskId}/sign`, {
      method: "POST",
      body: JSON.stringify({ signature_image: signatureImage, comment }),
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

  // External sign API — token in header, never in URL
  externalView: (signerToken: string) =>
    externalRequest<ExternalDocView>("/external/document", signerToken),

  externalSign: (signerToken: string, signatureImage: string, consentText: string, requestId: string) =>
    externalRequest<{ signed: boolean }>("/external/sign", signerToken, {
      method: "POST",
      body: JSON.stringify({ signature_image: signatureImage, consent_text: consentText, request_id: requestId }),
    }),

  // Returns a fetch Response so the caller can stream the PDF into an <iframe> / blob URL.
  // Token sent as header; response is the raw PDF bytes.
  externalOriginalPdfHeaders: (signerToken: string): Record<string, string> => ({
    "X-Signer-Token": signerToken,
    "X-Request-ID": crypto.randomUUID(),
  }),

  externalOriginalPdfUrl: () => `${BASE}/external/document/file/original`,

  invite: (token: string, docId: number, body: InviteRequest) =>
    request<InviteResponse>(`/documents/${docId}/external-signers`, {
      method: "POST",
      body: JSON.stringify(body),
    }, token),

  listExternalSigners: (token: string, docId: number) =>
    request<ExternalSigner[]>(`/documents/${docId}/external-signers`, {}, token),

  cancelSigner: (token: string, docId: number, signerId: number) =>
    request(`/documents/${docId}/external-signers/${signerId}/cancel`, {
      method: "POST",
    }, token),

  // resend issues a fresh 64-char hex token; caller must hold it in useRef — never re-fetched
  resendSigner: (token: string, docId: number, signerId: number, expiresInHours?: number) =>
    request<ResendResponse>(`/documents/${docId}/external-signers/${signerId}/resend`, {
      method: "POST",
      body: JSON.stringify(expiresInHours != null ? { expires_in_hours: expiresInHours } : {}),
    }, token),

  // Admin document list
  listDocuments: (
    token: string,
    params: { page?: number; size?: number; status?: string; doc_format_code?: string; sync_status?: string; q?: string }
  ) => {
    const qs = new URLSearchParams();
    if (params.page) qs.set("page", String(params.page));
    if (params.size) qs.set("size", String(params.size));
    if (params.status) qs.set("status", params.status);
    if (params.doc_format_code) qs.set("doc_format_code", params.doc_format_code);
    if (params.sync_status) qs.set("sync_status", params.sync_status);
    if (params.q) qs.set("q", params.q);
    const query = qs.toString();
    return request<DocumentRow[]>(`/documents${query ? "?" + query : ""}`, {}, token);
  },

  getDocumentDetail: (token: string, docId: number) =>
    request<DocumentDetail>(`/documents/${docId}`, {}, token),

  getAuditLogs: (token: string, docId: number) =>
    request<{ audit_logs: AuditEntry[]; signature_events: SigEvent[] }>(`/documents/${docId}/audit-logs`, {}, token),

  // Workflow templates
  listTemplates: (token: string, docFormatCode?: string) => {
    const qs = docFormatCode ? `?doc_format_code=${encodeURIComponent(docFormatCode)}` : "";
    return request<TemplateRow[]>(`/workflow-templates${qs}`, {}, token);
  },

  getTemplate: (token: string, id: string) =>
    request<TemplateDetail>(`/workflow-templates/${id}`, {}, token),

  cloneTemplate: (token: string, id: string) =>
    request<{ id: string; doc_format_code: string; name: string; version: number; status: string }>(
      `/workflow-templates/${id}/clone`, { method: "POST" }, token
    ),

  publishTemplate: (token: string, id: string) =>
    request<{ id: string; status: string }>(`/workflow-templates/${id}/publish`, { method: "POST" }, token),

  deactivateTemplate: (token: string, id: string) =>
    request<{ id: string; status: string }>(`/workflow-templates/${id}/deactivate`, { method: "POST" }, token),

  // ── Workflow config editor (Phase B) ──
  listUsers: (token: string) => request<UserOption[]>("/users", {}, token),

  createTemplate: (token: string, body: { doc_format_code: string; name: string }) =>
    request<{ id: string; doc_format_code: string; name: string; version: number; status: string }>(
      "/workflow-templates", { method: "POST", body: JSON.stringify(body) }, token
    ),

  updateTemplate: (token: string, id: string, body: { name: string }) =>
    request<{ id: string; name: string }>(
      `/workflow-templates/${id}`, { method: "PUT", body: JSON.stringify(body) }, token
    ),

  updateSteps: (token: string, id: string, steps: StepInput[]) =>
    request<{ id: string; step_count: number }>(
      `/workflow-templates/${id}/steps`, { method: "PUT", body: JSON.stringify({ steps }) }, token
    ),

  // ── User management (Phase C, system_admin) ──
  listRoles: (token: string) => request<RoleOption[]>("/roles", {}, token),

  listAdminUsers: (token: string, includeInactive = false) =>
    request<AdminUser[]>(`/admin/users${includeInactive ? "?include_inactive=1" : ""}`, {}, token),

  createUser: (
    token: string,
    body: { username: string; display_name: string; email?: string; phone?: string; roles: string[]; password?: string }
  ) => request<AdminUser>("/admin/users", { method: "POST", body: JSON.stringify(body) }, token),

  updateUser: (
    token: string,
    id: string,
    body: { display_name: string; email?: string; phone?: string; status: string; roles: string[]; password?: string }
  ) => request<AdminUser>(`/admin/users/${id}`, { method: "PUT", body: JSON.stringify(body) }, token),

  importDocument: (
    token: string,
    fields: {
      file: File;
      doc_format_code: string;
      doc_no: string;
      revision?: number;
      doc_date?: string;
      amount?: string;
    }
  ) => {
    const fd = new FormData();
    fd.append("file", fields.file);
    fd.append("doc_format_code", fields.doc_format_code);
    fd.append("doc_no", fields.doc_no);
    if (fields.revision != null) fd.append("revision", String(fields.revision));
    if (fields.doc_date) fd.append("doc_date", fields.doc_date);
    if (fields.amount) fd.append("amount", fields.amount);
    return importRequest<ImportResult>("/documents/import", token, fd);
  },
};
