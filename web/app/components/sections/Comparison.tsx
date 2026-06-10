import { type ReactNode } from 'react';
import { Section } from '../ui/Section';
import { Badge } from '../ui/Badge';

/**
 * Comparison — an honest, side-by-side matrix of BharatCode against the
 * mainstream terminal coding agents (Claude Code, OpenCode, Codex CLI).
 *
 * Everything here is factual and conservative: licenses, host language,
 * provider model, and data-residency posture. No fabricated benchmarks,
 * no parity claims. The BharatCode column is visually highlighted because
 * it's our site — not because every cell is "best".
 */

/** A single cell value. */
type Cell =
  | { kind: 'yes'; note?: string }
  | { kind: 'no'; note?: string }
  | { kind: 'partial'; note?: string }
  | { kind: 'text'; note: string };

type Tool = {
  id: string;
  name: string;
  /** Short subtitle under the column header. */
  vendor: string;
  /** Highlight the whole column (BharatCode). */
  highlight?: boolean;
};

type Row = {
  label: string;
  /** Optional clarifying caption shown under the row label. */
  hint?: string;
  /** Cell values keyed by tool id. */
  cells: Record<string, Cell>;
};

const TOOLS: Tool[] = [
  { id: 'bharatcode', name: 'BharatCode', vendor: 'Open source', highlight: true },
  { id: 'claude', name: 'Claude Code', vendor: 'Anthropic' },
  { id: 'opencode', name: 'OpenCode', vendor: 'Open source' },
  { id: 'codex', name: 'Codex CLI', vendor: 'OpenAI' },
];

const ROWS: Row[] = [
  {
    label: 'Language',
    hint: 'What the agent itself is written in.',
    cells: {
      bharatcode: { kind: 'text', note: 'Go (CGO-free)' },
      claude: { kind: 'text', note: 'TypeScript / Node' },
      opencode: { kind: 'text', note: 'TypeScript' },
      codex: { kind: 'text', note: 'Rust' },
    },
  },
  {
    label: 'License',
    hint: 'Source code license.',
    cells: {
      bharatcode: { kind: 'text', note: 'MIT' },
      claude: { kind: 'text', note: 'Proprietary' },
      opencode: { kind: 'text', note: 'MIT' },
      codex: { kind: 'text', note: 'Apache-2.0' },
    },
  },
  {
    label: 'Open-weight-first',
    hint: 'Open models (Kimi K2, DeepSeek, Qwen) as first-class defaults.',
    cells: {
      bharatcode: { kind: 'yes', note: 'By design' },
      claude: { kind: 'no', note: 'Claude only' },
      opencode: { kind: 'partial', note: 'Supported, not default' },
      codex: { kind: 'no', note: 'OpenAI only' },
    },
  },
  {
    label: 'Local / data residency',
    hint: 'Can source code stay in India — local or India/Asia-hosted inference?',
    cells: {
      bharatcode: { kind: 'yes', note: 'Local-first' },
      claude: { kind: 'no', note: 'Cloud (US)' },
      opencode: { kind: 'partial', note: 'Provider-dependent' },
      codex: { kind: 'no', note: 'Cloud (US)' },
    },
  },
  {
    label: 'Cost-aware (INR)',
    hint: 'Built-in cost ledger that speaks rupees.',
    cells: {
      bharatcode: { kind: 'yes', note: 'INR ledger' },
      claude: { kind: 'no' },
      opencode: { kind: 'no' },
      codex: { kind: 'no' },
    },
  },
  {
    label: 'Vendor lock-in',
    hint: 'Tied to a single model vendor?',
    cells: {
      bharatcode: { kind: 'yes', note: '10+ providers' },
      claude: { kind: 'no', note: 'Anthropic' },
      opencode: { kind: 'yes', note: 'Multi-provider' },
      codex: { kind: 'no', note: 'OpenAI' },
    },
  },
];

/**
 * Rows where a "Yes" is the desirable outcome render the check in green.
 * "Vendor lock-in" is phrased as a positive cell ("no lock-in") so a green
 * check there still means "good" — i.e. the cell answers "lock-in avoided?".
 */
function CellMark({ cell }: { cell: Cell }) {
  if (cell.kind === 'text') {
    return (
      <span className="font-mono text-[0.8125rem] text-fg sm:text-sm">
        {cell.note}
      </span>
    );
  }

  const config = {
    yes: {
      icon: <CheckIcon />,
      ring: 'border-green/30 bg-green/10 text-green',
      sr: 'Yes',
    },
    partial: {
      icon: <DashIcon />,
      ring: 'border-saffron/30 bg-saffron/10 text-saffron',
      sr: 'Partial',
    },
    no: {
      icon: <CrossIcon />,
      ring: 'border-border-strong bg-surface text-faint',
      sr: 'No',
    },
  }[cell.kind];

  return (
    <span className="inline-flex flex-col items-center gap-1.5 text-center sm:flex-row sm:gap-2 sm:text-left">
      <span
        className={`inline-flex h-6 w-6 shrink-0 items-center justify-center rounded-full border ${config.ring}`}
      >
        {config.icon}
        <span className="sr-only">{config.sr}</span>
      </span>
      {cell.note ? (
        <span className="font-mono text-[0.6875rem] leading-tight text-muted sm:text-xs">
          {cell.note}
        </span>
      ) : null}
    </span>
  );
}

export function Comparison() {
  return (
    <Section
      id="comparison"
      width="content"
      spacing="lg"
      className="border-t border-border bg-bg"
    >
      <div className="mx-auto mb-12 max-w-2xl text-center">
        <Badge variant="saffron" size="md" className="mb-4">
          How it compares
        </Badge>
        <h2 className="text-balance text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          An honest look at the landscape
        </h2>
        <p className="mt-4 text-pretty text-base leading-relaxed text-muted sm:text-lg">
          Every tool here is good at something. BharatCode is built for teams in
          India that need their source to stay in India — open-weight-first,
          cost-aware, and free of single-vendor lock-in.
        </p>
      </div>

      {/* Desktop / tablet: real semantic table. */}
      <div className="hidden md:block">
        <DesktopTable />
      </div>

      {/* Mobile: stacked cards, one per tool. */}
      <div className="space-y-4 md:hidden">
        <MobileCards />
      </div>

      <p className="mt-8 text-center text-xs leading-relaxed text-faint">
        Comparison reflects each project&rsquo;s typical configuration and public
        licensing. Capabilities of fast-moving tools change — verify against
        upstream docs before standardising.
      </p>
    </Section>
  );
}

/* -------------------------------------------------------------------------- */
/* Desktop table                                                              */
/* -------------------------------------------------------------------------- */

function DesktopTable() {
  return (
    <div className="overflow-hidden rounded-2xl border border-border bg-surface/40">
      <table className="w-full border-collapse text-left">
        <caption className="sr-only">
          Feature comparison of BharatCode, Claude Code, OpenCode, and Codex CLI.
        </caption>
        <thead>
          <tr className="border-b border-border">
            <th scope="col" className="w-[28%] px-5 py-5 align-bottom lg:px-6">
              <span className="font-mono text-xs uppercase tracking-wider text-faint">
                Capability
              </span>
            </th>
            {TOOLS.map((tool) => (
              <th
                key={tool.id}
                scope="col"
                className={`relative px-4 py-5 align-bottom lg:px-5 ${
                  tool.highlight ? 'bg-saffron/[0.06]' : ''
                }`}
              >
                {tool.highlight ? (
                  <span
                    aria-hidden="true"
                    className="absolute inset-x-0 top-0 h-0.5 bg-saffron"
                  />
                ) : null}
                <span className="flex flex-col gap-1">
                  <span
                    className={`text-base font-semibold tracking-tight ${
                      tool.highlight ? 'text-saffron' : 'text-fg'
                    }`}
                  >
                    {tool.name}
                  </span>
                  <span className="font-mono text-[0.6875rem] uppercase tracking-wider text-faint">
                    {tool.vendor}
                  </span>
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {ROWS.map((row, i) => (
            <tr
              key={row.label}
              className={i % 2 === 1 ? 'bg-bg-elevated/40' : ''}
            >
              <th
                scope="row"
                className="px-5 py-4 align-top font-normal lg:px-6"
              >
                <span className="block text-sm font-medium text-fg">
                  {row.label}
                </span>
                {row.hint ? (
                  <span className="mt-0.5 block text-xs leading-snug text-faint">
                    {row.hint}
                  </span>
                ) : null}
              </th>
              {TOOLS.map((tool) => (
                <td
                  key={tool.id}
                  className={`px-4 py-4 align-middle lg:px-5 ${
                    tool.highlight
                      ? 'bg-saffron/[0.06] ring-1 ring-inset ring-saffron/10'
                      : ''
                  }`}
                >
                  <CellMark cell={row.cells[tool.id]} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Mobile cards                                                               */
/* -------------------------------------------------------------------------- */

function MobileCards() {
  return (
    <>
      {TOOLS.map((tool) => (
        <div
          key={tool.id}
          className={`rounded-2xl border p-5 ${
            tool.highlight
              ? 'border-saffron/40 bg-saffron/[0.06] shadow-glow'
              : 'border-border bg-surface/40'
          }`}
        >
          <div className="mb-4 flex items-center justify-between gap-3">
            <div className="flex flex-col">
              <span
                className={`text-lg font-semibold tracking-tight ${
                  tool.highlight ? 'text-saffron' : 'text-fg'
                }`}
              >
                {tool.name}
              </span>
              <span className="font-mono text-[0.6875rem] uppercase tracking-wider text-faint">
                {tool.vendor}
              </span>
            </div>
            {tool.highlight ? (
              <Badge variant="saffron" size="sm" dot>
                This is us
              </Badge>
            ) : null}
          </div>
          <dl className="divide-y divide-border/70">
            {ROWS.map((row) => (
              <Row key={row.label} term={row.label}>
                <CellMark cell={row.cells[tool.id]} />
              </Row>
            ))}
          </dl>
        </div>
      ))}
    </>
  );
}

function Row({ term, children }: { term: string; children: ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-4 py-2.5">
      <dt className="text-sm text-muted">{term}</dt>
      <dd className="text-right">{children}</dd>
    </div>
  );
}

/* -------------------------------------------------------------------------- */
/* Icons                                                                       */
/* -------------------------------------------------------------------------- */

function CheckIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M20 6 9 17l-5-5" />
    </svg>
  );
}

function CrossIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  );
}

function DashIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="3"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5 12h14" />
    </svg>
  );
}

export default Comparison;
