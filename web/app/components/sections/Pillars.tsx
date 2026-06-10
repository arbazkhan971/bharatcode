import { type ReactNode } from 'react';

import { Badge } from '../ui/Badge';
import { Section } from '../ui/Section';

/** Accent themes wired to the brand tokens (see globals.css). */
type Accent = 'green' | 'blue' | 'saffron';

type Pillar = {
  accent: Accent;
  icon: ReactNode;
  title: string;
  tag: string;
  blurb: string;
};

/**
 * Color mapping is intentional, not decorative:
 *  - green   → "data stays in India" / sovereignty cue (reserved in tokens)
 *  - blue    → secondary tech accent (open models)
 *  - saffron → India-inspired primary accent (cost)
 */
const ACCENTS: Record<
  Accent,
  { iconWrap: string; icon: string; ring: string; badge: 'green' | 'blue' | 'saffron' }
> = {
  green: {
    iconWrap: 'border-green/25 bg-green/10',
    icon: 'text-green',
    ring: 'hover:border-green/40 hover:shadow-[0_0_0_1px_rgb(var(--bc-green)/0.25),0_18px_50px_-24px_rgb(var(--bc-green)/0.45)]',
    badge: 'green',
  },
  blue: {
    iconWrap: 'border-blue/25 bg-blue/10',
    icon: 'text-blue',
    ring: 'hover:border-blue/40 hover:shadow-glow-blue',
    badge: 'blue',
  },
  saffron: {
    iconWrap: 'border-saffron/25 bg-saffron/10',
    icon: 'text-saffron',
    ring: 'hover:border-saffron/40 hover:shadow-glow',
    badge: 'saffron',
  },
};

const PILLARS: Pillar[] = [
  {
    accent: 'green',
    icon: <ShieldIcon />,
    title: 'Data sovereignty',
    tag: 'Local-first',
    blurb:
      'Your source never has to leave the country. BharatCode runs locally and connects only to the models you choose — including fully local Ollama and LM Studio, or India- and Asia-hosted inference. Built for banks, enterprises, and DPDP-regulated teams.',
  },
  {
    accent: 'blue',
    icon: <ChipIcon />,
    title: 'Open-weight first',
    tag: '10–20x cheaper',
    blurb:
      'Open-weight models are first-class citizens — Kimi K2, DeepSeek V3 / R1, and Qwen Coder run as well as frontier closed models for most coding work, at a fraction of the cost. Frontier-closed providers stay optional, never required.',
  },
  {
    accent: 'saffron',
    icon: <LedgerIcon />,
    title: 'Cost discipline',
    tag: 'INR-aware',
    blurb:
      'An INR-aware cost ledger tracks every token in rupees, so spend is legible to Indian teams from day one. A $200/mo plan is ₹17,000+ — BharatCode keeps that number in front of you, not hidden in a dashboard.',
  },
];

/**
 * Pillars — the three core positioning pillars rendered as feature cards.
 *
 * Static and server-rendered (no client hooks): icons are inline SVGs and all
 * copy is literal, so it static-exports cleanly. Card titles are <h3> under a
 * section <h2>.
 */
export function Pillars() {
  return (
    <Section id="pillars" width="content" spacing="lg">
      <div className="mx-auto max-w-2xl text-center">
        <Badge variant="saffron" size="sm" dot>
          Why BharatCode
        </Badge>
        <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          Built on three non-negotiables
        </h2>
        <p className="mt-4 text-pretty text-base leading-relaxed text-muted sm:text-lg">
          An open-source terminal coding agent designed for India — sovereign by
          default, open-weight by choice, and honest about cost.
        </p>
      </div>

      <ul className="mt-12 grid grid-cols-1 gap-5 sm:mt-14 md:grid-cols-3">
        {PILLARS.map((pillar) => (
          <PillarCard key={pillar.title} pillar={pillar} />
        ))}
      </ul>
    </Section>
  );
}

function PillarCard({ pillar }: { pillar: Pillar }) {
  const accent = ACCENTS[pillar.accent];

  return (
    <li
      className={`group relative flex flex-col rounded-2xl border border-border bg-surface p-6 transition-colors duration-200 sm:p-7 ${accent.ring}`}
    >
      <div
        className={`inline-flex h-12 w-12 items-center justify-center rounded-xl border ${accent.iconWrap} ${accent.icon}`}
      >
        {pillar.icon}
      </div>

      <div className="mt-5 flex flex-wrap items-center gap-x-3 gap-y-2">
        <h3 className="text-lg font-semibold tracking-tight text-fg">
          {pillar.title}
        </h3>
        <Badge variant={accent.badge} size="sm">
          {pillar.tag}
        </Badge>
      </div>

      <p className="mt-3 text-sm leading-relaxed text-muted">{pillar.blurb}</p>
    </li>
  );
}

/* --- Icons: inline SVGs matching the scaffold's conventions
   (viewBox 0 0 24 24, stroke="currentColor", aria-hidden). No icon dep. --- */

function ShieldIcon() {
  return (
    <svg
      width="22"
      height="22"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
      <path d="m9 12 2 2 4-4" />
    </svg>
  );
}

function ChipIcon() {
  return (
    <svg
      width="22"
      height="22"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="6" y="6" width="12" height="12" rx="2" />
      <path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" />
      <path d="M10 10h4v4h-4z" />
    </svg>
  );
}

function LedgerIcon() {
  return (
    <svg
      width="22"
      height="22"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M4 4h13a2 2 0 0 1 2 2v14H6a2 2 0 0 1-2-2V4Z" />
      <path d="M8 4v16" />
      <path d="M12 9h4M12 13h4" />
    </svg>
  );
}

export default Pillars;
