import { type ReactNode, type SVGProps } from 'react';
import { Badge } from '../ui/Badge';
import { Section } from '../ui/Section';

/**
 * Features — a scannable grid of BharatCode's real capabilities.
 *
 * Server-rendered and self-contained: all icons are inline SVGs (no icon
 * dependency), and styling uses the shared design tokens. Each card describes
 * a capability that actually ships today — no fabricated claims.
 */

type AccentKey = 'saffron' | 'blue' | 'green';

type Feature = {
  title: string;
  /** Short monospace tag shown under the title (a command, flag, or label). */
  tag: string;
  description: string;
  icon: (props: SVGProps<SVGSVGElement>) => ReactNode;
  accent: AccentKey;
};

const ACCENT: Record<
  AccentKey,
  { icon: string; ring: string; tag: string; glow: string }
> = {
  saffron: {
    icon: 'text-saffron',
    ring: 'border-saffron/25 bg-saffron/10 group-hover:border-saffron/50',
    tag: 'text-saffron/90',
    glow: 'group-hover:shadow-glow',
  },
  blue: {
    icon: 'text-blue',
    ring: 'border-blue/25 bg-blue/10 group-hover:border-blue/50',
    tag: 'text-blue/90',
    glow: 'group-hover:shadow-glow-blue',
  },
  green: {
    icon: 'text-green',
    ring: 'border-green/25 bg-green/10 group-hover:border-green/50',
    tag: 'text-green/90',
    glow: '',
  },
};

const FEATURES: Feature[] = [
  {
    title: 'Rich Markdown TUI',
    tag: 'bubble tea',
    description:
      'A polished terminal UI built on Bubble Tea with full markdown rendering — headings, tables, and syntax-highlighted code blocks render inline as the agent works.',
    icon: TerminalIcon,
    accent: 'saffron',
  },
  {
    title: 'Agent Loop + 13 Tools',
    tag: 'read · edit · bash · grep …',
    description:
      'An autonomous agent loop wired to 13 built-in tools for reading, editing, searching, and running code — so the model can navigate and change your repo end to end.',
    icon: LoopIcon,
    accent: 'blue',
  },
  {
    title: 'MCP + LSP Integration',
    tag: 'mcp · lsp',
    description:
      'Connect external tools over the Model Context Protocol, and pull real diagnostics, types, and definitions straight from your project’s Language Servers.',
    icon: PlugIcon,
    accent: 'green',
  },
  {
    title: 'Autonomous /goal Mode',
    tag: '/goal',
    description:
      'Hand the agent a high-level objective and let it plan, execute, and iterate across multiple steps on its own — checking its own work as it goes.',
    icon: TargetIcon,
    accent: 'saffron',
  },
  {
    title: 'Permission & Approval Modes',
    tag: 'permission engine',
    description:
      'A permission engine gates every risky action behind configurable approval modes — review and approve edits and shell commands, or let trusted actions run.',
    icon: ShieldIcon,
    accent: 'blue',
  },
  {
    title: 'SQLite Sessions + Resume',
    tag: 'resume',
    description:
      'Every session is persisted to local SQLite, so you can close the terminal and resume a conversation later with its full history and context intact.',
    icon: DatabaseIcon,
    accent: 'green',
  },
  {
    title: 'Shell Hooks',
    tag: 'hooks',
    description:
      'Run your own shell commands at key points in the agent lifecycle — lint, format, test, or notify — to fit BharatCode into your existing workflow.',
    icon: HookIcon,
    accent: 'saffron',
  },
  {
    title: 'INR Cost Ledger',
    tag: '₹ per session',
    description:
      'A built-in, INR-aware cost ledger tracks token spend per session and model in real time — so a $200/mo bill reads as the ₹17,000+ it actually is.',
    icon: LedgerIcon,
    accent: 'blue',
  },
];

export function Features() {
  return (
    <Section id="features" spacing="lg">
      <div className="mx-auto max-w-2xl text-center">
        <Badge variant="saffron" size="sm">
          Capabilities
        </Badge>
        <h2 className="mt-4 text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          Everything you expect from a modern coding agent
        </h2>
        <p className="mt-4 text-base leading-relaxed text-muted sm:text-lg">
          A complete terminal agent — rich TUI, tools, protocols, and cost
          tracking — that runs locally and keeps your source where it belongs.
        </p>
      </div>

      <ul
        role="list"
        className="mt-12 grid grid-cols-1 gap-4 sm:mt-14 sm:grid-cols-2 lg:grid-cols-4"
      >
        {FEATURES.map((feature) => (
          <FeatureCard key={feature.title} feature={feature} />
        ))}
      </ul>
    </Section>
  );
}

function FeatureCard({ feature }: { feature: Feature }) {
  const accent = ACCENT[feature.accent];
  const Icon = feature.icon;

  return (
    <li
      className={`group relative flex flex-col rounded-2xl border border-border bg-surface p-5 transition-all duration-200 hover:-translate-y-0.5 hover:bg-surface-hover hover:border-border-strong ${accent.glow}`}
    >
      <span
        className={`inline-flex h-10 w-10 items-center justify-center rounded-xl border transition-colors duration-200 ${accent.ring}`}
      >
        <Icon className={`h-5 w-5 ${accent.icon}`} aria-hidden="true" />
      </span>

      <h3 className="mt-4 text-base font-semibold leading-tight text-fg">
        {feature.title}
      </h3>
      <p
        className={`mt-1 font-mono text-[0.6875rem] uppercase tracking-wider ${accent.tag}`}
      >
        {feature.tag}
      </p>
      <p className="mt-3 text-sm leading-relaxed text-muted">
        {feature.description}
      </p>
    </li>
  );
}

/* -------------------------------------------------------------------------- */
/* Inline icons — 24×24, currentColor, 1.75 stroke. No external dependency.   */
/* -------------------------------------------------------------------------- */

const ICON_BASE: SVGProps<SVGSVGElement> = {
  viewBox: '0 0 24 24',
  fill: 'none',
  stroke: 'currentColor',
  strokeWidth: 1.75,
  strokeLinecap: 'round',
  strokeLinejoin: 'round',
};

function TerminalIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <rect x="3" y="4" width="18" height="16" rx="2" />
      <path d="m7 9 3 3-3 3" />
      <path d="M13 15h4" />
    </svg>
  );
}

function LoopIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <path d="M21 12a9 9 0 1 1-3.5-7.1" />
      <path d="M21 4v5h-5" />
    </svg>
  );
}

function PlugIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <path d="M12 22v-5" />
      <path d="M9 8V2" />
      <path d="M15 8V2" />
      <path d="M18 8v3a6 6 0 0 1-12 0V8z" />
    </svg>
  );
}

function TargetIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <circle cx="12" cy="12" r="9" />
      <circle cx="12" cy="12" r="5" />
      <circle cx="12" cy="12" r="1.25" fill="currentColor" stroke="none" />
    </svg>
  );
}

function ShieldIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
      <path d="m9 12 2 2 4-4" />
    </svg>
  );
}

function DatabaseIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <ellipse cx="12" cy="5" rx="8" ry="3" />
      <path d="M4 5v6c0 1.66 3.58 3 8 3s8-1.34 8-3V5" />
      <path d="M4 11v6c0 1.66 3.58 3 8 3s8-1.34 8-3v-6" />
    </svg>
  );
}

function HookIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <path d="M18 4v8a6 6 0 0 1-12 0" />
      <circle cx="6" cy="16" r="2.5" />
      <path d="M18 4h3" />
    </svg>
  );
}

function LedgerIcon(props: SVGProps<SVGSVGElement>) {
  return (
    <svg {...ICON_BASE} {...props}>
      <rect x="4" y="3" width="16" height="18" rx="2" />
      <path d="M8 3v18" />
      <path d="M12 8h4" />
      <path d="M12 12h4" />
      <path d="M12 16h4" />
    </svg>
  );
}

export default Features;
