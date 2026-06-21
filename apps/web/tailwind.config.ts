import type { Config } from "tailwindcss";

/*
  PaperLess Design System — Tailwind mapping.
  Colors reference CSS variables from globals.css so the whole app re-themes
  from one place. Use semantic names in components (bg-surface, text-muted,
  bg-brand, border-line) rather than raw palette steps where possible.
*/
const config: Config = {
  content: [
    "./src/pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/components/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    extend: {
      colors: {
        brand: {
          50: "var(--brand-50)",
          100: "var(--brand-100)",
          200: "var(--brand-200)",
          300: "var(--brand-300)",
          400: "var(--brand-400)",
          500: "var(--brand-500)",
          600: "var(--brand-600)",
          700: "var(--brand-700)",
          800: "var(--brand-800)",
          900: "var(--brand-900)",
          DEFAULT: "var(--brand-600)",
        },
        // Text
        ink: "var(--ink)",
        muted: "var(--muted)",
        subtle: "var(--subtle)",
        // Surfaces
        bg: "var(--bg)",
        surface: {
          DEFAULT: "var(--surface)",
          muted: "var(--surface-muted)",
        },
        // Lines (use as border-line / border-line-strong)
        line: {
          DEFAULT: "var(--line)",
          strong: "var(--line-strong)",
        },
        // Semantic tones
        success: {
          DEFAULT: "var(--success)",
          bg: "var(--success-bg)",
          fg: "var(--success-fg)",
        },
        warning: {
          DEFAULT: "var(--warning)",
          bg: "var(--warning-bg)",
          fg: "var(--warning-fg)",
        },
        danger: {
          DEFAULT: "var(--danger)",
          bg: "var(--danger-bg)",
          fg: "var(--danger-fg)",
        },
        info: {
          DEFAULT: "var(--info)",
          bg: "var(--info-bg)",
          fg: "var(--info-fg)",
        },
      },
      fontFamily: {
        sans: ["var(--font-sans)", "system-ui", "sans-serif"],
      },
      borderRadius: {
        sm: "8px",
        DEFAULT: "10px",
        md: "12px",
        lg: "16px",
        xl: "20px",
        "2xl": "24px",
      },
      boxShadow: {
        // Calm, low-contrast elevation
        card: "0 1px 2px rgba(15,23,42,0.04), 0 1px 3px rgba(15,23,42,0.06)",
        pop: "0 6px 16px rgba(15,23,42,0.08), 0 2px 6px rgba(15,23,42,0.06)",
        sheet: "0 -4px 16px rgba(15,23,42,0.08)",
      },
      ringColor: {
        DEFAULT: "var(--ring)",
      },
    },
  },
  plugins: [],
};
export default config;
