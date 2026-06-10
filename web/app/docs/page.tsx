import type { Metadata } from 'next';

import { CodeBlock } from '@/app/components/ui/CodeBlock';
import {
  DocsCallout,
  DocsH2,
  DocsLinkInline,
  DocsList,
  DocsListItem,
  DocsP,
  DocsPage,
  InlineCode,
} from './components/DocsPage';

export const metadata: Metadata = {
  title: 'Documentation',
  description:
    'BharatCode is a Go-native, MIT-licensed, open-weight-first terminal AI coding agent where your data stays in India.',
};

export default function IntroductionPage() {
  return (
    <DocsPage
      eyebrow="Getting Started"
      title="Introduction"
      lede={
        <>
          BharatCode is a Go-native, MIT-licensed CLI coding agent — OpenCode
          for India. It runs in your terminal, edits real files, runs commands,
          and keeps your data in India by connecting only to the models you
          choose: open-weight providers, India-hosted endpoints, or fully local
          models.
        </>
      }
      next={{ href: '/docs/installation', label: 'Installation' }}
    >
      <DocsP>
        BharatCode is a single static binary written in Go (1.25+, CGO-free).
        There is no daemon, no background service, and no required telemetry.
        You install one command, point it at a provider, and start working —
        either through an interactive terminal UI (TUI) or headlessly for CI and
        automation.
      </DocsP>

      <CodeBlock
        language="bash"
        label="install"
        prompt
        code={'brew install arbazkhan971/tap/bharatcode'}
      />

      <DocsH2 id="three-pillars">The three pillars</DocsH2>
      <DocsP>
        Everything in BharatCode follows from three commitments.
      </DocsP>
      <DocsList>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Data stays in India.
          </strong>{' '}
          BharatCode never phones home. It talks only to the model endpoints you
          configure — run fully local with{' '}
          <DocsLinkInline href="/docs/providers">
            Ollama or LM Studio
          </DocsLinkInline>{' '}
          so your source code never leaves your machine, or connect to
          open-weight and India-hosted providers you control.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Open-weight first.
          </strong>{' '}
          Open-weight models like DeepSeek and Moonshot/Kimi are first-class
          citizens, not an afterthought. Providers are config-driven, so you can
          mix hosted open-weight endpoints, frontier APIs, and local runtimes —
          and switch between them per session.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Yours to own.
          </strong>{' '}
          MIT-licensed and open source. An INR-aware cost ledger with a monthly
          budget gate keeps spend visible and bounded, and a permission system
          puts you in control of every file edit and shell command.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="what-you-get">What you get</DocsH2>
      <DocsList>
        <DocsListItem>
          An interactive TUI plus a headless mode —{' '}
          <InlineCode>bharatcode run &quot;...&quot;</InlineCode> for one-shot
          prompts and <InlineCode>--json</InlineCode> for an NDJSON event stream
          you can pipe into CI.
        </DocsListItem>
        <DocsListItem>
          13 built-in{' '}
          <DocsLinkInline href="/docs/tools">tools</DocsLinkInline> — view,
          edit, multiedit, write, ls, glob, grep, bash, web_fetch, web_search,
          diagnostics, todo — plus background job control.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/commands">Slash commands</DocsLinkInline>{' '}
          in the TUI for sessions, models, agents, diffs, permissions, budget,
          and your own custom prompts.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/goal">
            /goal autonomous mode
          </DocsLinkInline>{' '}
          for bounded, iterative work — set a goal and let the agent run to
          completion within limits you define.
        </DocsListItem>
        <DocsListItem>
          Integrations:{' '}
          <DocsLinkInline href="/docs/mcp">MCP</DocsLinkInline> servers,{' '}
          <DocsLinkInline href="/docs/lsp">LSP</DocsLinkInline> diagnostics in
          context, and shell-backed{' '}
          <DocsLinkInline href="/docs/hooks">lifecycle hooks</DocsLinkInline>.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="who-its-for">Who it&apos;s for</DocsH2>
      <DocsP>
        BharatCode is built for developers and teams who need to keep code and
        data within India&apos;s borders — banks, enterprises, and DPDP-regulated
        organizations — as well as individuals who want an open-source agent
        they can run locally and pay for in INR. If you want full control over
        which model sees your code and what it&apos;s allowed to do, this is for
        you.
      </DocsP>

      <DocsCallout tone="tip" title="New here?">
        Head to{' '}
        <DocsLinkInline href="/docs/installation">Installation</DocsLinkInline>{' '}
        to get the binary, then{' '}
        <DocsLinkInline href="/docs/quick-start">Quick Start</DocsLinkInline> to
        run your first session in under a minute.
      </DocsCallout>
    </DocsPage>
  );
}
