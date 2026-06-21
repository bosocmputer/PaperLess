import Badge, { type Tone } from "./Badge";

/*
  Maps backend status enums to a Thai label + a semantic tone.
  Enum values are authoritative in the DB CHECK constraints (see 0001_init.up.sql
  and src/lib/api.ts). Keep these maps in sync with those constraints.
  Unknown values fall back to a neutral badge showing the raw value (so a new
  backend state is visible, never silently hidden).
*/

type Entry = { label: string; tone: Tone; dot?: boolean };

// documents.status: imported,pending,rejected,completed,cancelled
const DOCUMENT: Record<string, Entry> = {
  imported: { label: "นำเข้าแล้ว", tone: "info" },
  pending: { label: "รอเซ็น", tone: "warning", dot: true },
  rejected: { label: "ส่งคืน", tone: "danger" },
  completed: { label: "เสร็จสิ้น", tone: "success", dot: true },
  cancelled: { label: "ยกเลิก", tone: "neutral" },
};

// documents.sync_status: not_required,sync_pending,synced,sync_failed,sync_unknown
const SYNC: Record<string, Entry> = {
  not_required: { label: "ไม่ต้องซิงก์", tone: "neutral" },
  sync_pending: { label: "รอซิงก์", tone: "info", dot: true },
  synced: { label: "ซิงก์แล้ว", tone: "success" },
  sync_failed: { label: "ซิงก์ล้มเหลว", tone: "danger", dot: true },
  sync_unknown: { label: "สถานะซิงก์ไม่ทราบ", tone: "warning" },
};

// signature_tasks.status
const TASK: Record<string, Entry> = {
  open: { label: "รอเซ็น", tone: "warning", dot: true },
  signed: { label: "เซ็นแล้ว", tone: "success" },
  skipped: { label: "ข้าม", tone: "neutral" },
  rejected: { label: "ส่งคืน", tone: "danger" },
};

// external_signers.status: pending,signed,expired,cancelled
const SIGNER: Record<string, Entry> = {
  pending: { label: "รอเซ็น", tone: "warning", dot: true },
  signed: { label: "เซ็นแล้ว", tone: "success" },
  expired: { label: "หมดอายุ", tone: "danger" },
  cancelled: { label: "ยกเลิก", tone: "neutral" },
};

// workflow_templates.status: draft,active,inactive
const TEMPLATE: Record<string, Entry> = {
  draft: { label: "ร่าง", tone: "warning" },
  active: { label: "ใช้งาน", tone: "success", dot: true },
  inactive: { label: "ปิดใช้", tone: "neutral" },
};

const MAPS = {
  document: DOCUMENT,
  sync: SYNC,
  task: TASK,
  signer: SIGNER,
  template: TEMPLATE,
} as const;

interface StatusBadgeProps {
  kind: keyof typeof MAPS;
  status: string | null | undefined;
  className?: string;
}

export default function StatusBadge({ kind, status, className }: StatusBadgeProps) {
  if (!status) return null;
  const entry = MAPS[kind][status] ?? { label: status, tone: "neutral" as Tone };
  return (
    <Badge tone={entry.tone} dot={entry.dot} className={className}>
      {entry.label}
    </Badge>
  );
}
