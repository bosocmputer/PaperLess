import { cn } from "@/lib/cn";

export type Tone = "neutral" | "info" | "success" | "warning" | "danger";

interface BadgeProps {
  tone?: Tone;
  /** Show a leading status dot. */
  dot?: boolean;
  children: React.ReactNode;
  className?: string;
}

const tones: Record<Tone, string> = {
  neutral: "bg-surface-muted text-muted",
  info: "bg-info-bg text-info-fg",
  success: "bg-success-bg text-success-fg",
  warning: "bg-warning-bg text-warning-fg",
  danger: "bg-danger-bg text-danger-fg",
};

const dotColor: Record<Tone, string> = {
  neutral: "bg-subtle",
  info: "bg-info",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

export default function Badge({ tone = "neutral", dot, children, className }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-semibold whitespace-nowrap",
        tones[tone],
        className
      )}
    >
      {dot && <span className={cn("w-1.5 h-1.5 rounded-full", dotColor[tone])} />}
      {children}
    </span>
  );
}
