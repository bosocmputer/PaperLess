"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api, type Attachment } from "@/lib/api";
import { Spinner } from "@/components/ui";

interface Props {
  docId: number;
  token: string;
  canEdit: boolean;
}

function fmtSize(b: number | null): string {
  if (!b) return "";
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(0)} KB`;
  return `${(b / (1024 * 1024)).toFixed(1)} MB`;
}

function fileName(a: Attachment): string {
  const base = a.object_key.split("/").pop() || `ไฟล์ #${a.id}`;
  return base;
}

function icon(mime: string | null): string {
  if (!mime) return "📎";
  if (mime === "application/pdf") return "📄";
  if (mime.startsWith("image/")) return "🖼️";
  return "📎";
}

export default function Attachments({ docId, token, canEdit }: Props) {
  const [items, setItems] = useState<Attachment[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [uploading, setUploading] = useState(false);
  const [actionErr, setActionErr] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const res = await api.listAttachments(token, docId);
      if (!res.success) { setError(res.error.code); return; }
      setItems(res.data ?? []);
    } catch {
      setError("network_error");
    } finally {
      setLoading(false);
    }
  }, [docId, token]);

  useEffect(() => { load(); }, [load]);

  const onUpload = async (file: File) => {
    setUploading(true);
    setActionErr(null);
    const res = await api.uploadAttachment(token, docId, file);
    setUploading(false);
    if (fileRef.current) fileRef.current.value = "";
    if (!res.success) { setActionErr(res.error.message || res.error.code); return; }
    load();
  };

  const onDelete = async (id: number) => {
    if (!window.confirm("ลบไฟล์แนบนี้?")) return;
    setActionErr(null);
    const res = await api.deleteAttachment(token, id);
    if (!res.success) { setActionErr(res.error.message || res.error.code); return; }
    setItems((cur) => cur.filter((x) => x.id !== id));
  };

  const viewUrl = (id: number) => `${api.attachmentFileUrl(id)}?token=${encodeURIComponent(token)}`;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <p className="text-sm font-semibold text-ink">เอกสารแนบ {!loading && `(${items.length})`}</p>
        {canEdit && (
          <label className="inline-flex items-center h-9 px-3 text-sm font-medium rounded-md border border-line-strong bg-surface cursor-pointer hover:bg-surface-muted">
            {uploading ? "กำลังอัปโหลด..." : "+ แนบไฟล์"}
            <input ref={fileRef} type="file" accept="application/pdf,image/png,image/jpeg,image/gif" className="hidden"
              disabled={uploading}
              onChange={(e) => { const f = e.target.files?.[0]; if (f) onUpload(f); }} />
          </label>
        )}
      </div>

      {actionErr && <p className="text-xs text-danger-fg bg-danger-bg border border-danger/30 rounded-md px-3 py-2">{actionErr}</p>}

      {loading ? (
        <div className="flex justify-center py-4 text-brand"><Spinner size="sm" /></div>
      ) : error ? (
        <p className="text-sm text-danger-fg">โหลดไฟล์แนบไม่สำเร็จ</p>
      ) : items.length === 0 ? (
        <p className="text-sm text-subtle">ยังไม่มีเอกสารแนบ</p>
      ) : (
        <ul className="flex flex-col">
          {items.map((a) => (
            <li key={a.id} className="flex items-center gap-3 py-2 border-b border-line last:border-0">
              <span className="text-lg flex-shrink-0">{icon(a.mime_type)}</span>
              <div className="flex-1 min-w-0">
                <a href={viewUrl(a.id)} target="_blank" rel="noopener noreferrer"
                  className="text-sm text-brand-700 hover:underline truncate block">{fileName(a)}</a>
                <p className="text-xs text-subtle">{fmtSize(a.size_bytes)} · {a.created_at.slice(0, 10)}</p>
              </div>
              {canEdit && (
                <button onClick={() => onDelete(a.id)} className="text-xs text-danger-fg underline flex-shrink-0">ลบ</button>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
