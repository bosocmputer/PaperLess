import { forwardRef } from "react";
import { cn } from "@/lib/cn";
import Spinner from "./Spinner";

type Variant = "primary" | "secondary" | "outline" | "ghost" | "danger";
type Size = "sm" | "md" | "lg";

interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: Size;
  /** Stretch to fill the container width — common for mobile primary actions. */
  block?: boolean;
  /** Shows a spinner and disables the button. */
  loading?: boolean;
}

const base =
  "inline-flex items-center justify-center gap-2 font-medium rounded-md " +
  "transition-[transform,background-color,border-color] active:scale-[0.98] " +
  "select-none disabled:opacity-45 disabled:pointer-events-none " +
  "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-offset-2";

const variants: Record<Variant, string> = {
  primary: "bg-brand text-white hover:bg-brand-700",
  secondary: "bg-surface-muted text-ink hover:bg-line",
  outline: "border border-line-strong bg-surface text-ink hover:bg-surface-muted",
  ghost: "text-brand-700 hover:bg-brand-50",
  danger: "bg-danger text-white hover:opacity-90",
};

// Sizes keep a 44px+ touch target on md/lg for finger signing on mobile.
const sizes: Record<Size, string> = {
  sm: "h-9 px-3 text-sm",
  md: "h-11 px-4 text-[15px]",
  lg: "h-[52px] px-6 text-base",
};

const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "primary", size = "md", block, loading, disabled, className, children, ...props },
  ref
) {
  return (
    <button
      ref={ref}
      disabled={disabled || loading}
      className={cn(base, variants[variant], sizes[size], block && "w-full", className)}
      {...props}
    >
      {loading && <Spinner size="sm" className="text-current" />}
      {children}
    </button>
  );
});

export default Button;
