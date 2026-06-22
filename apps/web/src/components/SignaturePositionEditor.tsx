"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import type { SigSlot } from "@/lib/api";
import { Button, Spinner } from "@/components/ui";
import { cn } from "@/lib/cn";

// One step the admin can place a signature box for.
export interface SlotStep {
  key: string;
  label: string;
  slot: SigSlot | null;
}

interface Props {
  steps: SlotStep[];
  onChange: (key: string, slot: SigSlot | null) => void;
  disabled?: boolean;
}

const MIN_NORM = 0.02; // ignore accidental tiny drags
const TARGET_WIDTH = 760; // css px the page is rendered at

const clamp01 = (n: number) => Math.max(0, Math.min(1, n));

// Distinct colors so multiple boxes are tellable apart.
const COLORS = ["#2e498a", "#15803d", "#b45309", "#b91c1c", "#7c3aed", "#0e7490"];

export default function SignaturePositionEditor({ steps, onChange, disabled }: Props) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const docRef = useRef<any>(null);

  const [numPages, setNumPages] = useState(0);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState<{ w: number; h: number } | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [activeKey, setActiveKey] = useState<string | null>(steps[0]?.key ?? null);

  // In-progress drag rectangle, in css px relative to the overlay.
  const [draft, setDraft] = useState<{ x: number; y: number; w: number; h: number } | null>(null);
  const dragStart = useRef<{ x: number; y: number } | null>(null);

  const colorFor = (key: string) => COLORS[Math.max(0, steps.findIndex((s) => s.key === key)) % COLORS.length];

  const loadPdf = useCallback(async (file: File) => {
    setError(null);
    setLoading(true);
    try {
      // Dynamic import keeps pdf.js out of SSR and the initial bundle.
      const pdfjs = await import("pdfjs-dist/legacy/build/pdf.mjs");
      pdfjs.GlobalWorkerOptions.workerSrc = "/pdf.worker.min.mjs";
      const buf = await file.arrayBuffer();
      const doc = await pdfjs.getDocument({ data: buf }).promise;
      docRef.current = doc;
      setNumPages(doc.numPages);
      setPage(1);
    } catch {
      setError("ไม่สามารถเปิดไฟล์ PDF นี้ได้");
      docRef.current = null;
      setNumPages(0);
    } finally {
      setLoading(false);
    }
  }, []);

  // Render the current page whenever it changes.
  useEffect(() => {
    const doc = docRef.current;
    const canvas = canvasRef.current;
    if (!doc || !canvas || numPages === 0) return;
    let cancelled = false;
    (async () => {
      const p = await doc.getPage(page);
      const base = p.getViewport({ scale: 1 });
      const scale = TARGET_WIDTH / base.width;
      const viewport = p.getViewport({ scale });
      const ratio = Math.max(window.devicePixelRatio || 1, 1);
      const ctx = canvas.getContext("2d");
      if (!ctx || cancelled) return;
      canvas.width = Math.floor(viewport.width * ratio);
      canvas.height = Math.floor(viewport.height * ratio);
      canvas.style.width = `${viewport.width}px`;
      canvas.style.height = `${viewport.height}px`;
      ctx.setTransform(ratio, 0, 0, ratio, 0, 0);
      await p.render({ canvasContext: ctx, viewport }).promise;
      if (!cancelled) setSize({ w: viewport.width, h: viewport.height });
    })();
    return () => { cancelled = true; };
  }, [page, numPages]);

  // ── Drawing (pointer events on the overlay) ──
  const overlayPoint = (e: React.PointerEvent) => {
    const rect = e.currentTarget.getBoundingClientRect();
    return { x: e.clientX - rect.left, y: e.clientY - rect.top };
  };

  const onPointerDown = (e: React.PointerEvent) => {
    if (disabled || !activeKey || !size) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    const pt = overlayPoint(e);
    dragStart.current = pt;
    setDraft({ x: pt.x, y: pt.y, w: 0, h: 0 });
  };
  const onPointerMove = (e: React.PointerEvent) => {
    if (!dragStart.current || !size) return;
    const pt = overlayPoint(e);
    const x = Math.min(dragStart.current.x, pt.x);
    const y = Math.min(dragStart.current.y, pt.y);
    setDraft({ x, y, w: Math.abs(pt.x - dragStart.current.x), h: Math.abs(pt.y - dragStart.current.y) });
  };
  const onPointerUp = () => {
    if (!dragStart.current || !size || !activeKey) { dragStart.current = null; setDraft(null); return; }
    const d = draft;
    dragStart.current = null;
    setDraft(null);
    if (!d) return;
    const nx = clamp01(d.x / size.w);
    const ny = clamp01(d.y / size.h);
    const nw = clamp01(d.w / size.w);
    const nh = clamp01(d.h / size.h);
    if (nw < MIN_NORM || nh < MIN_NORM) return; // ignore tiny/accidental
    onChange(activeKey, { page, x: nx, y: ny, w: Math.min(nw, 1 - nx), h: Math.min(nh, 1 - ny) });
  };

  const placedCount = steps.filter((s) => s.slot).length;

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <label className="inline-flex items-center h-9 px-3 text-sm font-medium rounded-md border border-line-strong bg-surface cursor-pointer hover:bg-surface-muted">
          เลือกไฟล์ PDF ตัวอย่าง
          <input type="file" accept="application/pdf" className="hidden"
            onChange={(e) => { const f = e.target.files?.[0]; if (f) loadPdf(f); e.target.value = ""; }} />
        </label>
        {numPages > 1 && (
          <div className="flex items-center gap-1">
            <Button variant="ghost" size="sm" onClick={() => setPage((p) => Math.max(1, p - 1))} disabled={page === 1}>←</Button>
            <span className="text-sm text-muted tabular-nums">หน้า {page}/{numPages}</span>
            <Button variant="ghost" size="sm" onClick={() => setPage((p) => Math.min(numPages, p + 1))} disabled={page === numPages}>→</Button>
          </div>
        )}
        <span className="text-xs text-muted ml-auto">วางกรอบแล้ว {placedCount}/{steps.length} ตำแหน่ง</span>
      </div>

      {error && <p className="text-sm text-danger-fg bg-danger-bg border border-danger/30 rounded-md px-3 py-2">{error}</p>}

      {numPages > 0 && (
        <>
          {/* Step picker — which position the next box belongs to */}
          <div className="flex flex-wrap gap-1.5">
            {steps.map((s) => (
              <button
                key={s.key}
                type="button"
                onClick={() => setActiveKey(s.key)}
                className={cn(
                  "text-xs px-2.5 py-1.5 rounded-full border font-medium inline-flex items-center gap-1.5",
                  activeKey === s.key ? "text-white border-transparent" : "bg-surface text-ink border-line-strong"
                )}
                style={activeKey === s.key ? { background: colorFor(s.key) } : undefined}
              >
                <span className="w-2 h-2 rounded-full" style={{ background: colorFor(s.key) }} />
                {s.label}{s.slot ? " ✓" : ""}
              </button>
            ))}
          </div>
          <p className="text-xs text-muted">
            เลือกตำแหน่งด้านบน แล้ว<strong>ลากกรอบ</strong>บนเอกสารเพื่อกำหนดจุดวางลายเซ็น (ลากใหม่เพื่อแก้)
          </p>

          {/* Canvas + overlay */}
          <div className="relative inline-block border border-line rounded-md overflow-hidden bg-surface-muted self-start">
            {loading && <div className="absolute inset-0 flex items-center justify-center text-brand z-10"><Spinner /></div>}
            <canvas ref={canvasRef} className="block" />
            {size && (
              <div
                className="absolute inset-0 touch-none cursor-crosshair"
                style={{ width: size.w, height: size.h }}
                onPointerDown={onPointerDown}
                onPointerMove={onPointerMove}
                onPointerUp={onPointerUp}
              >
                {/* existing boxes on this page */}
                {steps.filter((s) => s.slot && s.slot.page === page).map((s) => {
                  const sl = s.slot!;
                  const col = colorFor(s.key);
                  return (
                    <div key={s.key} className="absolute border-2 rounded-sm"
                      style={{ left: sl.x * size.w, top: sl.y * size.h, width: sl.w * size.w, height: sl.h * size.h,
                               borderColor: col, background: `${col}22` }}>
                      <span className="absolute -top-5 left-0 text-[10px] px-1 rounded text-white whitespace-nowrap"
                        style={{ background: col }}>{s.label}</span>
                    </div>
                  );
                })}
                {/* in-progress draft */}
                {draft && (
                  <div className="absolute border-2 border-dashed rounded-sm"
                    style={{ left: draft.x, top: draft.y, width: draft.w, height: draft.h,
                             borderColor: activeKey ? colorFor(activeKey) : "#2e498a",
                             background: activeKey ? `${colorFor(activeKey)}22` : "#2e498a22" }} />
                )}
              </div>
            )}
          </div>

          {/* Per-step summary + clear */}
          <div className="flex flex-col gap-1">
            {steps.map((s) => (
              <div key={s.key} className="flex items-center justify-between text-xs">
                <span className="text-muted">
                  <span className="inline-block w-2 h-2 rounded-full mr-1.5 align-middle" style={{ background: colorFor(s.key) }} />
                  {s.label}: {s.slot ? `หน้า ${s.slot.page}` : "ยังไม่กำหนด"}
                </span>
                {s.slot && !disabled && (
                  <button type="button" onClick={() => onChange(s.key, null)} className="text-danger-fg underline">ลบกรอบ</button>
                )}
              </div>
            ))}
          </div>
        </>
      )}

      {numPages === 0 && !loading && (
        <p className="text-sm text-subtle">อัปโหลด PDF ตัวอย่างของรูปแบบเอกสารนี้ เพื่อกำหนดตำแหน่งลายเซ็น</p>
      )}
    </div>
  );
}
