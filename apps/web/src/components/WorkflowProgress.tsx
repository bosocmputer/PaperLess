"use client";

import type { StepProgress } from "@/lib/api";

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
    <div className="flex flex-col gap-1">
      <p className="text-xs text-gray-500 font-medium uppercase tracking-wide mb-1">ความคืบหน้า</p>
      {steps.map((step, i) => {
        const isCurrent = step.sequence_no === currentSeq;
        const isDone = step.complete;
        return (
          <div key={i} className={`flex items-center gap-3 rounded-lg px-3 py-2 ${
            isCurrent ? "bg-blue-50 border border-blue-200" : isDone ? "bg-green-50" : "bg-gray-50"
          }`}>
            <div className={`w-6 h-6 rounded-full flex items-center justify-center text-xs font-bold flex-shrink-0 ${
              isDone ? "bg-green-500 text-white" : isCurrent ? "bg-blue-600 text-white" : "bg-gray-300 text-gray-600"
            }`}>
              {isDone ? "✓" : step.sequence_no}
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-sm font-medium text-gray-800">
                ขั้นที่ {step.sequence_no} <span className="text-gray-400 text-xs">({conditionLabel[step.condition_type] ?? "-"})</span>
              </p>
              {step.condition_type === 2 && (
                <p className="text-xs text-gray-500">{step.signed_count}/{step.total_count} เซ็นแล้ว</p>
              )}
            </div>
            {isCurrent && !isDone && (
              <span className="text-xs text-blue-600 font-medium flex-shrink-0">กำลังดำเนินการ</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
