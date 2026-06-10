import { type ReactNode } from 'react';
import { Section } from '@/app/components/ui/Section';
import { Badge } from '@/app/components/ui/Badge';
import { Button } from '@/app/components/ui/Button';
import { Terminal, type TerminalLine } from '@/app/components/ui/Terminal';

const REPO_URL = 'https://github.com/arbazkhan971/bharatcode';
const INSTALL_CMD = 'go install github.com/arbazkhan971/bharatcode@latest';

/**
 * Hero — the landing-page opener.
 *
 * Left column carries the brand promise (headline + subhead + CTAs); the
 * right column shows a stylized Terminal: the one-line install followed by a
 * faux BharatCode TUI session. Server-rendered and static-export friendly —
 * no client hooks live here. Decorative glow/grid layers are aria-hidden.
 */
export function Hero() {
  return (
    <Section
      as="header"
      id="hero"
      width="wide"
      spacing="none"
      className="relative isolate overflow-hidden"
      innerClassName="relative grid items-center gap-12 pb-16 pt-20 sm:pb-24 sm:pt-28 lg:grid-cols-[minmax(0,1fr)_minmax(0,1.05fr)] lg:gap-16 lg:pb-28 lg:pt-32"
    >
      {/* Decorative backdrop: soft saffron glow + faint grid. */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-0 -z-10 bg-saffron-glow"
      />
      <div
        aria-hidden="true"
        className="bc-grid-bg pointer-events-none absolute inset-0 -z-10"
      />

      {/* Left column — message + CTAs. */}
      <div className="flex flex-col items-start animate-fade-up">
        <div className="flex flex-wrap items-center gap-2">
          <Badge variant="saffron" dot>
            OpenCode for India
          </Badge>
          <Badge variant="green">MIT licensed</Badge>
          <Badge variant="neutral">Go-native</Badge>
        </div>

        <h1 className="mt-6 text-balance text-4xl font-semibold tracking-tight text-fg sm:text-5xl lg:text-6xl">
          OpenCode{' '}
          <span className="bg-gradient-to-r from-saffron to-saffron-soft bg-clip-text text-transparent">
            for India
          </span>
        </h1>

        <p className="mt-6 max-w-xl text-pretty text-base leading-relaxed text-muted sm:text-lg">
          An open-source terminal AI coding agent where{' '}
          <span className="font-medium text-fg">your data stays in India</span>.
          Local-first by design: it runs on your machine and connects to the
          models you choose — fully local{' '}
          <span className="font-mono text-sm text-fg">Ollama</span> /{' '}
          <span className="font-mono text-sm text-fg">LM&nbsp;Studio</span>, or
          India- and Asia-hosted inference. Your source never has to leave the
          country.
        </p>

        <div className="mt-9 flex flex-col gap-3 sm:flex-row sm:items-center">
          <Button
            href="#install"
            size="lg"
            trailing={<ArrowRightIcon />}
            className="font-mono"
          >
            Get started
          </Button>
          <Button
            href={REPO_URL}
            variant="secondary"
            size="lg"
            leading={<GitHubIcon />}
            rel="noopener noreferrer"
          >
            View on GitHub
          </Button>
          <Button href="/docs/" variant="ghost" size="lg">
            Read the docs
          </Button>
        </div>

        {/* Inline install hint mirroring the terminal demo. */}
        <div className="mt-6 flex items-center gap-3 font-mono text-sm text-muted">
          <span aria-hidden="true" className="select-none text-green">
            $
          </span>
          <code className="text-fg">{INSTALL_CMD}</code>
        </div>

        <p className="mt-6 text-sm text-faint">
          Open-weight models first-class — Kimi&nbsp;K2, DeepSeek&nbsp;V3/R1,
          Qwen&nbsp;Coder — with an INR-aware cost ledger. Built for banks,
          enterprises, and DPDP-regulated teams.
        </p>
      </div>

      {/* Right column — install + faux TUI session. */}
      <div className="relative animate-fade-up [animation-delay:120ms]">
        {/* Ambient glow behind the terminal. */}
        <div
          aria-hidden="true"
          className="pointer-events-none absolute -inset-4 -z-10 rounded-[2rem] bg-saffron/10 blur-3xl"
        />
        <Terminal title="bharatcode — ~/payments-api" lines={SESSION} cursor />
      </div>
    </Section>
  );
}

/** Faux TUI session lines. No fabricated metrics — just a plausible run. */
const SESSION: TerminalLine[] = [
  { type: 'command', text: INSTALL_CMD },
  { type: 'comment', text: '# installed bharatcode → ~/go/bin' },
  { type: 'spacer' },
  { type: 'command', text: 'bharatcode' },
  {
    type: 'output',
    text: (
      <>
        <span className="text-saffron">◆ BharatCode</span>{' '}
        <span className="text-faint">v0.1</span>{' '}
        <span className="text-muted">· model</span>{' '}
        <span className="text-blue">deepseek-v3</span>{' '}
        <span className="text-muted">· provider</span>{' '}
        <span className="text-green">ollama (local)</span>
      </>
    ),
  },
  { type: 'spacer' },
  {
    type: 'output',
    text: (
      <>
        <span className="text-saffron">›</span>{' '}
        <span className="text-fg">
          add idempotency keys to the refund handler
        </span>
      </>
    ),
  },
  {
    type: 'output',
    text: (
      <>
        <span className="text-blue">⊙ read</span>{' '}
        <span className="text-faint">internal/payments/refund.go</span>
      </>
    ),
  },
  {
    type: 'output',
    text: (
      <>
        <span className="text-blue">⊙ edit</span>{' '}
        <span className="text-faint">internal/payments/refund.go</span>{' '}
        <span className="text-green">+24</span>{' '}
        <span className="text-saffron">-3</span>
      </>
    ),
  },
  {
    type: 'output',
    text: (
      <>
        <span className="text-blue">⊙ bash</span>{' '}
        <span className="text-faint">go test ./internal/payments/...</span>
      </>
    ),
  },
  {
    type: 'success',
    text: (
      <>
        ✓ ok&nbsp;&nbsp;refund handler now idempotent — tests green
      </>
    ),
  },
  { type: 'spacer' },
  {
    type: 'output',
    text: (
      <>
        <span className="text-faint">ledger</span>{' '}
        <span className="text-muted">this turn</span>{' '}
        <span className="text-green">₹1.80</span>{' '}
        <span className="text-faint">· data never left your machine</span>
      </>
    ),
  },
];

function ArrowRightIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.25"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5 12h14" />
      <path d="m13 6 6 6-6 6" />
    </svg>
  );
}

function GitHubIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M12 2C6.48 2 2 6.58 2 12.25c0 4.53 2.87 8.37 6.84 9.73.5.1.68-.22.68-.49 0-.24-.01-.88-.01-1.73-2.78.62-3.37-1.37-3.37-1.37-.45-1.18-1.11-1.5-1.11-1.5-.91-.64.07-.62.07-.62 1 .07 1.53 1.05 1.53 1.05.89 1.56 2.34 1.11 2.91.85.09-.66.35-1.11.63-1.37-2.22-.26-4.56-1.14-4.56-5.06 0-1.12.39-2.03 1.03-2.75-.1-.26-.45-1.3.1-2.71 0 0 .84-.27 2.75 1.05a9.36 9.36 0 0 1 2.5-.34c.85 0 1.71.12 2.5.34 1.91-1.32 2.75-1.05 2.75-1.05.55 1.41.2 2.45.1 2.71.64.72 1.03 1.63 1.03 2.75 0 3.93-2.34 4.79-4.57 5.05.36.32.68.94.68 1.91 0 1.38-.01 2.49-.01 2.83 0 .27.18.6.69.49A10.02 10.02 0 0 0 22 12.25C22 6.58 17.52 2 12 2Z" />
    </svg>
  );
}

export default Hero;
