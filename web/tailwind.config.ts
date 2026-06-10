import type { Config } from 'tailwindcss';

/**
 * BharatCode design tokens.
 *
 * Source of truth for raw color channels lives in `app/globals.css` as CSS
 * custom properties (e.g. `--bg: 11 12 16`). Here we reference them with
 * `rgb(var(--token) / <alpha-value>)` so Tailwind opacity modifiers
 * (e.g. `bg-accent/20`) keep working and the palette can never drift between
 * the stylesheet and the config.
 *
 * Palette intent:
 *   - bg / surface / border : deep near-black dark theme
 *   - fg / muted            : foreground text ramp
 *   - saffron               : #FF9933 — India-inspired primary accent (used tastefully)
 *   - blue                  : tech blue — links / secondary accent
 *   - green                 : terminal green — success / "data stays in India" cues
 *   - india-green           : #138808 — flag green, reserved for sovereignty motifs
 */
const withAlpha = (token: string) => `rgb(var(${token}) / <alpha-value>)`;

const config: Config = {
  darkMode: 'class',
  content: [
    './app/**/*.{ts,tsx,mdx}',
    './components/**/*.{ts,tsx,mdx}',
  ],
  theme: {
    extend: {
      colors: {
        bg: withAlpha('--bc-bg'),
        'bg-elevated': withAlpha('--bc-bg-elevated'),
        surface: withAlpha('--bc-surface'),
        'surface-hover': withAlpha('--bc-surface-hover'),
        border: withAlpha('--bc-border'),
        'border-strong': withAlpha('--bc-border-strong'),
        fg: withAlpha('--bc-fg'),
        muted: withAlpha('--bc-muted'),
        faint: withAlpha('--bc-faint'),
        saffron: {
          DEFAULT: withAlpha('--bc-saffron'),
          soft: withAlpha('--bc-saffron-soft'),
        },
        blue: {
          DEFAULT: withAlpha('--bc-blue'),
        },
        green: {
          DEFAULT: withAlpha('--bc-green'),
        },
        'india-green': withAlpha('--bc-india-green'),
      },
      fontFamily: {
        sans: ['var(--font-sans)', 'ui-sans-serif', 'system-ui', 'sans-serif'],
        mono: [
          'var(--font-mono)',
          'ui-monospace',
          'SFMono-Regular',
          'Menlo',
          'monospace',
        ],
      },
      borderRadius: {
        xl: '0.875rem',
        '2xl': '1.125rem',
      },
      maxWidth: {
        content: '72rem',
      },
      boxShadow: {
        glow: '0 0 0 1px rgb(var(--bc-saffron) / 0.25), 0 8px 40px -12px rgb(var(--bc-saffron) / 0.35)',
        'glow-blue':
          '0 0 0 1px rgb(var(--bc-blue) / 0.25), 0 8px 40px -12px rgb(var(--bc-blue) / 0.35)',
        terminal: '0 24px 60px -24px rgb(0 0 0 / 0.7), 0 0 0 1px rgb(var(--bc-border) / 1)',
      },
      backgroundImage: {
        'grid-faint':
          'linear-gradient(rgb(var(--bc-border) / 0.5) 1px, transparent 1px), linear-gradient(90deg, rgb(var(--bc-border) / 0.5) 1px, transparent 1px)',
        'saffron-glow':
          'radial-gradient(60% 60% at 50% 0%, rgb(var(--bc-saffron) / 0.14) 0%, transparent 70%)',
      },
      keyframes: {
        'fade-up': {
          '0%': { opacity: '0', transform: 'translateY(8px)' },
          '100%': { opacity: '1', transform: 'translateY(0)' },
        },
        'cursor-blink': {
          '0%, 49%': { opacity: '1' },
          '50%, 100%': { opacity: '0' },
        },
      },
      animation: {
        'fade-up': 'fade-up 0.5s ease-out both',
        'cursor-blink': 'cursor-blink 1.1s step-end infinite',
      },
    },
  },
  plugins: [],
};

export default config;
