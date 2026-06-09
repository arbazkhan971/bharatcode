import { type ReactNode } from 'react';
import { Section } from '../ui/Section';
import { Badge } from '../ui/Badge';
import { Button } from '../ui/Button';
import { CodeBlock } from '../ui/CodeBlock';

const REPO_URL = 'https://github.com/arbazkhan971/bharatcode';
const DOCS_URL = 'https://github.com/arbazkhan971/bharatcode#readme';

type Step = {
  /** Sequential step label, e.g. "01". */
  n: string;
  /** Short title for the step. */
  title: string;
  /** Supporting sentence under the title. */
  blurb: ReactNode;
  /** Accent used for the step number + connecting rail. */
  accent: 'saffron' | 'blue' | 'green';
  /** The CodeBlock that demonstrates the step. */
  code: ReactNode;
};

const ACCENT_TEXT: Record<Step['accent'], string> = {
  saffron: 'text-saffron',
  blue: 'text-blue',
  green: 'text-green',
};

const ACCENT_RING: Record<Step['accent'], string> = {
  saffron: 'border-saffron/30 bg-saffron/10',
  blue: 'border-blue/30 bg-blue/10',
  green: 'border-green/30 bg-green/10',
};

/**
 * Install — getting-started section.
 *
 * Three stacked, numbered steps (install → hosted provider → fully local),
 * each paired with a copy-to-clipboard CodeBlock that mirrors the canonical
 * commands from the repo README verbatim. A power-user callout surfaces a few
 * real flags, and a CTA row links to the repo and docs.
 *
 * Server-rendered and accessible by default: an ordered list conveys step
 * order to assistive tech, and the only interactivity (copy) lives inside the
 * shared CodeBlock client component.
 */
export function Install() {
  const steps: Step[] = [
    {
      n: '01',
      title: 'Install',
      accent: 'saffron',
      blurb: (
        <>
          A single static binary — no daemon, no telemetry. Use Homebrew, npm, or
          the install script; no Go toolchain required.
        </>
      ),
      code: (
        <div className="space-y-3">
          <CodeBlock
            language="bash"
            label="homebrew"
            prompt
            code={'brew install arbazkhan971/tap/bharatcode'}
          />
          <CodeBlock
            language="bash"
            label="npm"
            prompt
            code={'npm install -g bharatcode-cli'}
          />
          <CodeBlock
            language="bash"
            label="curl (macOS / linux)"
            prompt
            code={
              'curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh | sh'
            }
          />
          <CodeBlock
            language="powershell"
            label="windows (powershell)"
            prompt
            code={
              'irm https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.ps1 | iex'
            }
          />
        </div>
      ),
    },
    {
      n: '02',
      title: 'Run with a hosted open-weight provider',
      accent: 'blue',
      blurb: (
        <>
          Export one key and go. Open-weight models like DeepSeek V3/R1 run
          10–20x cheaper than frontier closed models.
        </>
      ),
      code: (
        <CodeBlock
          language="bash"
          label="hosted provider"
          prompt
          code={
            'export DEEPSEEK_API_KEY=...        # or MOONSHOT_API_KEY, GROQ_API_KEY, etc.\nbharatcode'
          }
        />
      ),
    },
    {
      n: '03',
      title: 'Run fully local',
      accent: 'green',
      blurb: (
        <>
          No API key, no network round-trip — your source code never leaves your
          machine. Point at a local Ollama or LM Studio model.
        </>
      ),
      code: (
        <div className="space-y-3">
          <CodeBlock
            language="bash"
            label="ollama"
            prompt
            code={'bharatcode --provider ollama --model qwen2.5-coder:32b'}
          />
          <CodeBlock
            language="bash"
            label="lm studio"
            prompt
            code={
              'bharatcode --provider lmstudio --model qwen2.5-coder-32b-instruct'
            }
          />
        </div>
      ),
    },
  ];

  return (
    <Section
      id="install"
      width="content"
      spacing="lg"
      className="relative border-t border-border"
    >
      {/* Section heading */}
      <div className="mx-auto max-w-2xl text-center">
        <Badge variant="saffron" dot>
          Get started
        </Badge>
        <h2 className="mt-5 text-balance text-3xl font-semibold tracking-tight text-fg sm:text-4xl">
          Up and running in one command
        </h2>
        <p className="mt-4 text-pretty text-base leading-relaxed text-muted sm:text-lg">
          Install the binary, then point it at a hosted open-weight provider or
          a fully local model. Same agent either way — your data stays in India.
        </p>
      </div>

      {/* Stacked, numbered steps */}
      <ol className="mx-auto mt-12 max-w-3xl space-y-10 sm:mt-16 sm:space-y-12">
        {steps.map((step, i) => (
          <li
            key={step.n}
            className="relative grid grid-cols-[2.5rem_1fr] gap-x-4 gap-y-3 sm:gap-x-6"
          >
            {/* Number column spans both rows so its rail can run full height. */}
            <div className="relative row-span-2 flex justify-center">
              {/* Connecting rail: from just below this circle down past the
                  code block to the next step's circle. Hidden on the last. */}
              {i < steps.length - 1 ? (
                <span
                  aria-hidden="true"
                  className="absolute top-12 -bottom-12 w-px bg-gradient-to-b from-border-strong via-border to-transparent sm:-bottom-14"
                />
              ) : null}
              <span
                aria-hidden="true"
                className={`relative flex h-10 w-10 shrink-0 items-center justify-center rounded-full border font-mono text-sm font-semibold ${ACCENT_RING[step.accent]} ${ACCENT_TEXT[step.accent]}`}
              >
                {step.n}
              </span>
            </div>

            {/* Step copy */}
            <div className="min-w-0 pt-1">
              <h3 className="text-lg font-medium text-fg">
                <span className="sr-only">{`Step ${step.n}: `}</span>
                {step.title}
              </h3>
              <p className="mt-1.5 text-sm leading-relaxed text-muted">
                {step.blurb}
              </p>
            </div>

            {/* Code block — second row of the right column. */}
            <div className="col-start-2 min-w-0">{step.code}</div>
          </li>
        ))}
      </ol>

      {/* Power-user callout: a few real flags */}
      <div className="mx-auto mt-10 max-w-3xl rounded-2xl border border-border bg-surface/50 p-5 sm:mt-12 sm:p-6">
        <p className="text-xs font-medium uppercase tracking-wider text-faint">
          A few real flags
        </p>
        <ul className="mt-3 space-y-2 text-sm leading-relaxed text-muted">
          <li className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
            <code className="rounded-md border border-border bg-bg-elevated px-1.5 py-0.5 font-mono text-[0.8125rem] text-fg">
              bharatcode --continue
            </code>
            <span>resume your most recent session.</span>
          </li>
          <li className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
            <code className="rounded-md border border-border bg-bg-elevated px-1.5 py-0.5 font-mono text-[0.8125rem] text-fg">
              bharatcode run --json
            </code>
            <span>headless NDJSON event stream for CI and automation.</span>
          </li>
          <li className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
            <code className="rounded-md border border-border bg-bg-elevated px-1.5 py-0.5 font-mono text-[0.8125rem] text-fg">
              /goal
            </code>
            <span>kick off bounded autonomous work from inside the TUI.</span>
          </li>
        </ul>
      </div>

      {/* CTA row */}
      <div className="mx-auto mt-10 flex max-w-3xl flex-col items-center gap-3 sm:mt-12 sm:flex-row sm:justify-center">
        <Button
          href={REPO_URL}
          variant="primary"
          size="md"
          target="_blank"
          rel="noreferrer"
          leading={<GitHubIcon />}
        >
          View on GitHub
        </Button>
        <Button
          href={DOCS_URL}
          variant="secondary"
          size="md"
          target="_blank"
          rel="noreferrer"
          trailing={<ArrowIcon />}
        >
          Read the docs
        </Button>
      </div>
    </Section>
  );
}

function GitHubIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <path d="M12 2C6.48 2 2 6.48 2 12c0 4.42 2.87 8.17 6.84 9.5.5.09.68-.22.68-.48 0-.24-.01-.87-.01-1.71-2.78.6-3.37-1.34-3.37-1.34-.45-1.16-1.11-1.47-1.11-1.47-.91-.62.07-.61.07-.61 1 .07 1.53 1.03 1.53 1.03.89 1.53 2.34 1.09 2.91.83.09-.65.35-1.09.63-1.34-2.22-.25-4.55-1.11-4.55-4.94 0-1.09.39-1.98 1.03-2.68-.1-.25-.45-1.27.1-2.65 0 0 .84-.27 2.75 1.02.8-.22 1.65-.33 2.5-.33.85 0 1.7.11 2.5.33 1.91-1.29 2.75-1.02 2.75-1.02.55 1.38.2 2.4.1 2.65.64.7 1.03 1.59 1.03 2.68 0 3.84-2.34 4.69-4.57 4.94.36.31.68.92.68 1.85 0 1.34-.01 2.42-.01 2.75 0 .27.18.58.69.48A10.01 10.01 0 0 0 22 12c0-5.52-4.48-10-10-10z" />
    </svg>
  );
}

function ArrowIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M5 12h14" />
      <path d="m12 5 7 7-7 7" />
    </svg>
  );
}

export default Install;
