import { cn } from "@/lib/cn";

interface SpinnerProps {
  size?: "sm" | "md" | "lg";
  className?: string;
  /** Accessible label; defaults to a Thai "loading" message. */
  label?: string;
}

const sizes = {
  sm: "w-4 h-4 border-2",
  md: "w-8 h-8 border-[3px]",
  lg: "w-12 h-12 border-4",
};

export default function Spinner({ size = "md", className, label = "กำลังโหลด" }: SpinnerProps) {
  return (
    <span
      role="status"
      aria-label={label}
      className={cn(
        "inline-block rounded-full animate-spin border-current border-t-transparent",
        sizes[size],
        className
      )}
    />
  );
}
