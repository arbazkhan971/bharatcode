import { type ReactNode } from 'react';
import { Section } from '@/app/components/ui/Section';
import { Badge } from '@/app/components/ui/Badge';

/**
 * Providers — "works with your models" section.
 *
 * A responsive grid of the LLM providers BharatCode speaks to, marking which
 * ones serve open-weight models and which ones keep your source on-device or
 * inside India/Asia. Local + India/Asia-hosted inference is what lets your
 * code stay in the country.
 *
 * Self-contained, server-rendered (no client hooks). Uses the shared Section /
 * Badge primitives and the project design tokens.
 */

/** What deployment posture a provider gives you. */
type Hosting = 'local' | 'india-asia' | 'global';

type Provider = {
  name: string;
  /** Short, honest one-liner about what this provider is good for. */
  note: string;
  /** Serves open-weight model families (Kimi K2, DeepSeek, Qwen, etc.). */
  openWeight: boolean;
  /** Runs fully on your own machine — no data leaves the device. */
  local: boolean;
  hosting: Hosting;
};

const PROVIDERS: Provider[] = [
  {
    name: 'Ollama',
    note: 'Run open models fully on your own machine. Nothing leaves the box.',
    openWeight: true,
    local: true,
    hosting: 'local',
  },
  {
    name: 'LM Studio',
    note: 'Local model server with a friendly UI. Air-gap friendly.',
    openWeight: true,
    local: true,
    hosting: 'local',
  },
  {
    name: 'DeepSeek',
    note: 'DeepSeek V3 / R1 — strong open-weight coding & reasoning.',
    openWeight: true,
    local: false,
    hosting: 'global',
  },
  {
    name: 'Moonshot / Kimi',
    note: 'Kimi K2 open weights — long-context agentic coding.',
    openWeight: true,
    local: false,
    hosting: 'global',
  },
  {
    name: 'Groq',
    note: 'Open-weight models served at very high token throughput.',
    openWeight: true,
    local: false,
    hosting: 'global',
  },
  {
    name: 'Together',
    note: 'Hosted open weights — Qwen Coder, DeepSeek, Kimi and more.',
    openWeight: true,
    local: false,
    hosting: 'global',
  },
  {
    name: 'Fireworks',
    note: 'Fast inference for open-weight coding models.',
    openWeight: true,
    local: false,
    hosting: 'global',
  },
  {
    name: 'OpenRouter',
    note: 'One key, many models — route to open weights or India/Asia regions.',
    openWeight: true,
    local: false,
    hosting: 'india-asia',
  },
  {
    name: 'Google Gemini',
    note: 'Gemini frontier models when you want a closed, hosted option.',
    openWeight: false,
    local: false,
    hosting: 'global',
  },
  {
    name: 'OpenAI',
    note: 'GPT frontier models via the standard API.',
    openWeight: false,
    local: false,
    hosting: 'global',
  },
  {
    name: 'Anthropic',
    note: 'Claude frontier models via the standard API.',
    openWeight: false,
    local: false,
    hosting: 'global',
  },
];

/** Visual legend for the capability tags, kept in sync with the cards. */
const LEGEND: { label: string; description: string; variant: 'green' | 'saffron' | 'blue' }[] = [
  {
    label: 'Open-weight',
    description: 'Open-weight model families — 10–20× cheaper than frontier closed models.',
    variant: 'saffron',
  },
  {
    label: 'Local',
    description: 'Runs fully on your machine — source code never leaves the device.',
    variant: 'green',
  },
  {
    label: 'India / Asia',
    description: 'Region-pinnable inference so data can stay in the country.',
    variant: 'blue',
  },
];

export function Providers() {
  return (
    <Section
      id="providers"
      width="wide"
      spacing="lg"
      className="border-t border-border bg-bg"
    >
      {/* Heading */}
      <div className="max-w-2xl">
        <Badge variant="green" dot>
          Bring your own models
        </Badge>
        <h2 className="mt-4 text-balance text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          Works with the models you choose
        </h2>
        <p className="mt-4 text-pretty text-base leading-relaxed text-muted sm:text-lg">
          BharatCode talks to 10+ providers through one agent. Point it at fully
          local runtimes or India / Asia-hosted inference and your{' '}
          <span className="font-medium text-fg">
            source code never has to leave the country
          </span>{' '}
          — or reach for frontier closed models when you want to. Your data, your
          call.
        </p>
      </div>

      {/* Legend */}
      <ul className="mt-8 flex flex-col gap-3 sm:flex-row sm:flex-wrap sm:items-center sm:gap-x-6 sm:gap-y-3">
        {LEGEND.map((item) => (
          <li key={item.label} className="flex items-start gap-2.5 sm:items-center">
            <Badge variant={item.variant} size="sm" className="mt-0.5 shrink-0 sm:mt-0">
              {item.label}
            </Badge>
            <span className="text-sm text-muted">{item.description}</span>
          </li>
        ))}
      </ul>

      {/* Provider grid — reads like a table on desktop, stacks on mobile. */}
      <ul
        role="list"
        className="mt-10 grid grid-cols-1 gap-px overflow-hidden rounded-2xl border border-border bg-border sm:grid-cols-2 lg:grid-cols-3"
      >
        {PROVIDERS.map((provider) => (
          <li key={provider.name}>
            <ProviderCard provider={provider} />
          </li>
        ))}
      </ul>

      {/* Sovereignty footnote */}
      <div className="mt-8 flex flex-col gap-3 rounded-xl border border-india-green/30 bg-india-green/5 p-4 sm:flex-row sm:items-center sm:gap-4">
        <ShieldIcon className="h-5 w-5 shrink-0 text-green" />
        <p className="text-sm leading-relaxed text-muted">
          <span className="font-medium text-fg">Data stays in India.</span>{' '}
          Local runtimes keep code on the device; region-pinned inference keeps
          it in-country. Built for banks, enterprises, and DPDP-regulated teams
          that can&apos;t ship source to a foreign cloud.
        </p>
      </div>
    </Section>
  );
}

function ProviderCard({ provider }: { provider: Provider }) {
  const { name, note, openWeight, local, hosting } = provider;

  return (
    <div className="group flex h-full flex-col gap-3 bg-surface p-5 transition-colors hover:bg-surface-hover">
      <div className="flex items-start justify-between gap-3">
        <h3 className="flex items-center gap-2 font-mono text-[0.9375rem] font-medium text-fg">
          <span
            aria-hidden="true"
            className={`h-1.5 w-1.5 shrink-0 rounded-full ${
              local
                ? 'bg-green'
                : hosting === 'india-asia'
                  ? 'bg-blue'
                  : openWeight
                    ? 'bg-saffron'
                    : 'bg-faint'
            }`}
          />
          {name}
        </h3>
        {local ? (
          <Badge variant="green" size="sm" className="shrink-0">
            Local
          </Badge>
        ) : null}
      </div>

      <p className="text-sm leading-relaxed text-muted">{note}</p>

      <div className="mt-auto flex flex-wrap items-center gap-1.5 pt-1">
        {openWeight ? (
          <Badge variant="saffron" size="sm">
            Open-weight
          </Badge>
        ) : (
          <Badge variant="neutral" size="sm">
            Closed
          </Badge>
        )}
        {hosting === 'india-asia' ? (
          <Badge variant="blue" size="sm">
            India / Asia
          </Badge>
        ) : null}
        {local ? (
          <span className="font-mono text-[0.6875rem] uppercase tracking-wider text-green/90">
            data stays on device
          </span>
        ) : null}
      </div>
    </div>
  );
}

function ShieldIcon({ className = '' }: { className?: string }): ReactNode {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={className}
      aria-hidden="true"
    >
      <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10Z" />
      <path d="m9 12 2 2 4-4" />
    </svg>
  );
}

export default Providers;
