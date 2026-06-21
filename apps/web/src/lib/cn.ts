// Tiny className joiner — no runtime deps.
// Filters out falsy values so we can compose variants conditionally.
// NOTE: this does NOT resolve conflicting Tailwind classes (like clsx+tailwind-merge).
// Compose variants in a fixed order and let later classes win where intended.
export function cn(
  ...inputs: Array<string | false | null | undefined>
): string {
  return inputs.filter(Boolean).join(" ");
}
