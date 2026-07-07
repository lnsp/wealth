/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  darkMode: 'class',
  theme: {
    extend: {
      fontFamily: {
        serif: ['"EB Garamond"', 'Georgia', '"Times New Roman"', 'serif'],
        sans: ['Inter', '-apple-system', 'BlinkMacSystemFont', '"SF Pro Display"', '"Helvetica Neue"', 'Arial', 'sans-serif'],
        mono: ['"SF Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      colors: {
        // Heritage tokens — swap in dark mode via CSS custom properties
        parchment: 'var(--color-parchment)',
        'parchment-deep': 'var(--color-parchment-deep)',
        ink: 'var(--color-ink)',
        'ink-body': 'var(--color-ink-body)',
        'ink-muted': 'var(--color-ink-muted)',
        divider: 'var(--color-divider)',
        inset: 'var(--color-inset)',
        // Accents — swap in dark mode for contrast (all WCAG AA on every surface)
        forest: 'var(--color-forest)',
        'forest-light': 'var(--color-forest-light)',
        gold: 'var(--color-gold)',
        sage: 'var(--color-sage)',
        claret: 'var(--color-claret)',
        amber: 'var(--color-amber)',
        walnut: 'var(--color-walnut)',
        slate: 'var(--color-slate)',
        // Semantic status aliases
        semantic: {
          success: 'var(--color-sage)',
          danger: 'var(--color-claret)',
          warning: 'var(--color-amber)',
          info: 'var(--color-forest)',
          muted: 'var(--color-ink-muted)',
        },
        // Chart palette
        chart: {
          1: 'var(--color-forest)', 2: 'var(--color-gold)', 3: 'var(--color-sage)',
          4: 'var(--color-walnut)', 5: 'var(--color-slate)', 6: 'var(--color-claret)',
        },
      },
      borderRadius: {
        'apple': '8px',
        'apple-lg': '12px',
        'apple-xl': '16px',
      },
      boxShadow: {
        // Reserve for elevated overlays only. Default elevation is hairline border-divider.
        'apple-sm': '0 1px 2px rgba(0, 0, 0, 0.04)',
        'apple': '0 2px 4px rgba(0, 0, 0, 0.05)',
        'apple-lg': '0 8px 24px rgba(0, 0, 0, 0.08)',
      },
      fontSize: {
        // Editorial scale. `display` and `title` default to EB Garamond.
        'display': ['44px', { lineHeight: '52px', fontWeight: '400', letterSpacing: '-0.025em', fontFamily: '"EB Garamond", Georgia, "Times New Roman", serif' }],
        'title': ['26px', { lineHeight: '32px', fontWeight: '400', letterSpacing: '-0.015em', fontFamily: '"EB Garamond", Georgia, "Times New Roman", serif' }],
        'heading': ['20px', { lineHeight: '28px', fontWeight: '500', letterSpacing: '-0.005em' }],
        'label': ['11px', { lineHeight: '16px', fontWeight: '500', letterSpacing: '0.1em' }],
      },
      transitionDuration: {
        DEFAULT: '350ms',
      },
      transitionTimingFunction: {
        DEFAULT: 'cubic-bezier(0.16, 1, 0.3, 1)',
      },
    },
  },
  plugins: [],
}
