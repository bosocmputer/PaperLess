# PaperLess Design System — "Trust & Calm"

Source of truth for the PaperLess frontend redesign. Tokens live in code
(`apps/web/src/app/globals.css` + `tailwind.config.ts`); this doc explains the
intent and the rules. If code and this doc disagree, **code wins** — update this
doc to match.

## Direction

A calm, professional, trustworthy look for a **legal e-signature product**.
Priorities, in order:

1. **Trust** — looks official enough for auditors, customers, and legal review.
2. **Legibility on mobile** — signers act on phones/tablets, often with a finger,
   sometimes outdoors. Big touch targets, strong contrast, generous spacing.
3. **Calm** — low-saturation navy/slate, soft elevation, no visual noise that
   competes with the document being signed.

## Tokens

All colors are CSS variables in `globals.css` and exposed to Tailwind as semantic
names. **Never hard-code hex in components.** Use the semantic name.

### Color — text
| Token | Tailwind | Use |
| --- | --- | --- |
| `--ink` | `text-ink` | Primary text, headings |
| `--muted` | `text-muted` | Secondary text, labels, metadata |
| `--subtle` | `text-subtle` | Placeholder, disabled, tertiary |

### Color — surfaces & lines
| Token | Tailwind | Use |
| --- | --- | --- |
| `--bg` | `bg-bg` | Page background (off-white) |
| `--surface` | `bg-surface` | Cards, sheets, headers |
| `--surface-muted` | `bg-surface-muted` | Inputs, table headers, sunken areas |
| `--line` | `border-line` | Default borders, dividers |
| `--line-strong` | `border-line-strong` | Input borders, emphasis |

### Color — brand (navy)
Scale `brand-50 … brand-900`, primary = `brand-600` (`bg-brand` / `text-brand`).
Use for primary actions, active states, links, focus.

### Color — semantic tones
Each tone has `DEFAULT` (solid), `-bg` (soft fill), `-fg` (text on soft fill).
| Tone | Meaning |
| --- | --- |
| `success` | completed, signed, synced |
| `warning` | awaiting action (pending / open / rorsen) |
| `danger` | rejected, failed, expired, destructive |
| `info` | in-progress / neutral-positive (brand-tinted) |

### Radius, elevation, focus
- Radius: `rounded-sm` 8px · `rounded-md` 12px · `rounded-lg` 16px (cards) · `rounded-xl` 20px · `rounded-full`.
- Shadow: `shadow-card` (resting surfaces) · `shadow-pop` (menus/popovers) · `shadow-sheet` (bottom sheets).
- Focus: global `:focus-visible` ring (`--ring`) + `focus-visible:ring-2` on controls. Never remove focus styling.

### Typography
- Font: **Sarabun** (Thai government standard), loaded via `next/font/google` →
  self-hosted at build, no runtime external request (on-prem safe).
- Body line-height `1.6` for comfortable Thai reading of dense documents.
- Weights in use: 300/400/500/600/700.

### Touch targets
- Minimum 44×44px for any tappable control (iOS guideline).
- `Button` `md`/`lg` sizes already satisfy this. Use `.touch-target` utility for ad-hoc controls.

## Components (`@/components/ui`)

Import from the barrel: `import { Button, Card, StatusBadge } from "@/components/ui"`.

| Component | Notes |
| --- | --- |
| `Button` | `variant` primary/secondary/outline/ghost/danger · `size` sm/md/lg · `block` · `loading` |
| `Card` / `CardButton` | Resting surface vs. tappable list row (press feedback + focus ring) |
| `Badge` | `tone` neutral/info/success/warning/danger · optional status `dot` |
| `StatusBadge` | Maps backend enums → Thai label + tone. `kind`: document / sync / task / signer / template |
| `Spinner` | `size` sm/md/lg, accessible `role=status` |
| `Input` | Label, `error`, `hint`, wired `aria-*` |

### StatusBadge ↔ backend enums
`StatusBadge` is the single place that translates DB status enums to Thai. The
maps mirror the `0001_init.up.sql` CHECK constraints. When the backend adds a
status, add it here too; unknown values render neutral with the raw value (never
hidden). Enums covered: `documents.status`, `documents.sync_status`,
`signature_tasks.status`, `external_signers.status`, `workflow_templates.status`.

## Rules

- Components use semantic tokens only — no raw hex, no arbitrary `bg-blue-600`.
- One theme today (light). A dark theme = a second variable block in `globals.css`;
  components need no changes because they reference tokens.
- Keep the document the visual hero — chrome stays calm and recedes.
- Every interactive element: visible focus, ≥44px touch target, clear disabled state.

## Status / rollout

- [x] Foundation: tokens, Tailwind mapping, Sarabun font, primitives (Button, Card, Badge, StatusBadge, Spinner, Input).
- [x] Shared components on tokens: `ErrorState`, `WorkflowProgress`.
- [x] All screens migrated to primitives + tokens: inbox, documents/[id] (sign + admin view), external/[token], login, root redirect, admin layout, admin/documents (+ import dialog), admin/documents/[id] (+ resend modal), admin/workflows.
- [x] Build + lint green; no remaining ad-hoc `gray-/blue-/amber-` palette in app screens.
- [ ] Manual QA on iOS Safari + Android Chrome (signing path) after deploy.
- [ ] Add later as needed: extract `PageHeader` to a primitive (currently duplicated), `Sheet`/`Modal` primitive (invite/resend/import dialogs hand-rolled), `Textarea`/`Select` primitives.

> Migration was presentation-only: no API calls, state machines, refs, or
> domain logic changed. `StatusBadge` is now the single source for status →
> Thai label/tone across all screens.
