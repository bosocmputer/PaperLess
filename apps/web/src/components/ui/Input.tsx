import { forwardRef, useId } from "react";
import { cn } from "@/lib/cn";

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string;
  /** Error message — also flips the field to the danger style. */
  error?: string;
  hint?: string;
}

const fieldBase =
  "w-full h-11 px-3 rounded-md bg-surface text-ink placeholder:text-subtle " +
  "border border-line-strong transition-colors " +
  "focus:outline-none focus:ring-2 focus:ring-offset-1 focus:border-brand-400 " +
  "disabled:bg-surface-muted disabled:text-muted disabled:cursor-not-allowed";

const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { label, error, hint, id, className, ...props },
  ref
) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const describedBy = error ? `${inputId}-err` : hint ? `${inputId}-hint` : undefined;

  return (
    <div className="flex flex-col gap-1.5">
      {label && (
        <label htmlFor={inputId} className="text-sm font-medium text-ink">
          {label}
        </label>
      )}
      <input
        ref={ref}
        id={inputId}
        aria-invalid={error ? true : undefined}
        aria-describedby={describedBy}
        className={cn(fieldBase, error && "border-danger focus:border-danger", className)}
        {...props}
      />
      {error ? (
        <p id={`${inputId}-err`} className="text-xs text-danger-fg">
          {error}
        </p>
      ) : hint ? (
        <p id={`${inputId}-hint`} className="text-xs text-muted">
          {hint}
        </p>
      ) : null}
    </div>
  );
});

export default Input;
