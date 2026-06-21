"use client";

import type { StepProgress } from "@/lib/api";
import { cn } from "@/lib/cn";

interface Props {
  steps: StepProgress[];
  currentSeq?: number;
}

const conditionLabel: Record<number, string> = {
  1: "คนใดคนหนึ่ง",
  2: "ทุกคน",
  3: "ผู้เซ็นภายนอก",
};

export default function WorkflowProgress({ steps, currentSeq }: Props) {
  if (!steps.length) return null;

  return (
    <div className="flex flex-col gap-1.5">
      <p className="text-xs text-muted font-semibold uppercase tracking-wide mb-1">ความคืบหน้า</p>
      {steps.map((step, i) => {
        const isCurrent = step.sequence_no === currentSeq;
        const isDone = step.complete;
        return (
          <div
            key={i}
            className={cn(
              "flex items-center gap-3 rounded-md px-3 py-2 border",
              isCurrent
                ? "bg-info-bg border-info/30"
                : isDone
                ? "bg-success-bg border-transparent"
                : "bg-surface-muted border-transparent"
            )}
          >
            <div
              className={cn(
                "w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold flex-shrink-0",
                isDone
                  ? "bg-success text-white"
                  : isCurrent
                  ? "bg-brand text-white"
                  : "bg-line-strong text-muted"
              )}
            >
              {isDone ? "✓" : step.sequence_no}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-ink">
                ขั้นที่ {step.sequence_no}{" "}
                <span className="text-subtle text-xs">({conditionLabel[step.condition_type] ?? "-"})</span>
              </p>
              {step.condition_type === 2 && (
                <p className="text-xs text-muted">{step.signed_count}/{step.total_count} เซ็นแล้ว</p>
              )}
            </div>
            {isCurrent && !isDone && (
              <span className="text-xs text-info-fg font-semibold flex-shrink-0">กำลังดำเนินการ</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
