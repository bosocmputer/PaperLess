import { forwardRef } from "react";
import { cn } from "@/lib/cn";

interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  /** Use for non-interactive containers (default). */
  padding?: "none" | "sm" | "md" | "lg";
}

const pad = {
  none: "",
  sm: "p-3",
  md: "p-4",
  lg: "p-6",
};

/** A calm, low-elevation surface. The base building block for content. */
export const Card = forwardRef<HTMLDivElement, CardProps>(function Card(
  { padding = "md", className, ...props },
  ref
) {
  return (
    <div
      ref={ref}
      className={cn(
        "bg-surface border border-line rounded-lg shadow-card",
        pad[padding],
        className
      )}
      {...props}
    />
  );
});

interface CardButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  padding?: "sm" | "md" | "lg";
}

/** A tappable card — list rows that navigate. Keeps the calm surface but adds press feedback. */
export const CardButton = forwardRef<HTMLButtonElement, CardButtonProps>(function CardButton(
  { padding = "md", className, ...props },
  ref
) {
  return (
    <button
      ref={ref}
      className={cn(
        "block w-full text-left bg-surface border border-line rounded-lg shadow-card",
        "transition-[transform,border-color] active:scale-[0.99] hover:border-line-strong",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2",
        pad[padding],
        className
      )}
      {...props}
    />
  );
});

export default Card;
