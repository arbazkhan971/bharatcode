import type { Metadata } from 'next';

import { CodeBlock } from '@/app/components/ui/CodeBlock';
import { Terminal } from '@/app/components/ui/Terminal';
import {
  DocsCallout,
  DocsH2,
  DocsH3,
  DocsLinkInline,
  DocsList,
  DocsListItem,
  DocsP,
  DocsPage,
  InlineCode,
} from '../components/DocsPage';

export const metadata: Metadata = {
  title: 'Quick Start',
  description:
    'Run your first BharatCode session in under a minute — set a provider key (or go fully local with Ollama), start the TUI, and learn headless mode for CI.',
};

export default function QuickStartPage() {
  return (
    <DocsPage
      eyebrow="Getting Started"
      title="Quick Start"
      lede={
        <>
          From zero to your first edit in under a minute. Point BharatCode at a
          model, start the terminal UI, and type a prompt — or run a fully local
          session with Ollama where your code never leaves your machine.
        </>
      }
      prev={{ href: '/docs/installation', label: 'Installation' }}
      next={{ href: '/docs/configuration', label: 'Config files' }}
    >
      <DocsP>
        This page assumes you already have the <InlineCode>bharatcode</InlineCode>{' '}
        binary on your <InlineCode>PATH</InlineCode>. If not, head to{' '}
        <DocsLinkInline href="/docs/installation">Installation</DocsLinkInline>{' '}
        first — one line with Homebrew, npm, or the install script. You can
        confirm the install at any time:
      </DocsP>

      <CodeBlock
        language="bash"
        label="verify the install"
        prompt
        code={'bharatcode version'}
      />

      <DocsP>
        BharatCode needs a model to talk to. You have two ways to start: a hosted
        open-weight provider (one environment variable), or a fully local model
        with Ollama (no key, no network). Pick whichever fits — both reach a
        working session in the same number of steps.
      </DocsP>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="first-run">Your first run with a hosted provider</DocsH2>
      <DocsP>
        Open-weight providers like DeepSeek are first-class in BharatCode. Each
        provider reads its API key from an environment variable, so the only
        setup is exporting one key. Using DeepSeek as the example:
      </DocsP>

      <CodeBlock
        language="bash"
        label="set a provider key"
        prompt
        code={'export DEEPSEEK_API_KEY=sk-your-key-here'}
      />

      <DocsP>
        Now launch the interactive terminal UI. Running{' '}
        <InlineCode>bharatcode</InlineCode> with no subcommand starts the TUI in
        your current directory:
      </DocsP>

      <CodeBlock language="bash" label="start the TUI" prompt code={'bharatcode'} />

      <DocsP>
        Type a request at the prompt and press Enter. BharatCode plans the work,
        calls its tools to read and edit real files, and shows you a diff before
        anything is written to disk:
      </DocsP>

      <Terminal
        title="bharatcode — ~/project"
        lines={[
          { type: 'comment', text: '# in the TUI, type a prompt' },
          {
            type: 'command',
            prompt: '›',
            text: 'add input validation to the signup handler',
          },
          { type: 'spacer' },
          {
            type: 'output',
            text: 'Reading internal/api/signup.go …',
          },
          {
            type: 'output',
            text: 'Proposing edit to internal/api/signup.go (review the diff)',
          },
          {
            type: 'success',
            text: '✓ applied · DeepSeek · ₹0.42 this session',
          },
        ]}
      />

      <DocsCallout tone="tip" title="Switching models">
        Use the <InlineCode>/model</InlineCode> slash command inside the TUI to
        pick a different provider or model for the current session, or run{' '}
        <InlineCode>bharatcode models</InlineCode> to list everything configured.
        See{' '}
        <DocsLinkInline href="/docs/providers">Providers &amp; Models</DocsLinkInline>{' '}
        for the full list and how to add your own.
      </DocsCallout>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="fully-local">A fully local run with Ollama</DocsH2>
      <DocsP>
        If you want your source code to never leave your machine, run against a
        local model. BharatCode supports{' '}
        <DocsLinkInline href="/docs/providers">Ollama and LM Studio</DocsLinkInline>{' '}
        as local providers — there is no API key and no outbound network call to
        a hosted endpoint. Start with Ollama running locally:
      </DocsP>

      <CodeBlock
        language="bash"
        label="pull any local coding model, then serve it with Ollama"
        prompt
        code={['ollama pull qwen2.5-coder', 'ollama serve'].join('\n')}
      />

      <DocsP>
        With the Ollama provider configured (see{' '}
        <DocsLinkInline href="/docs/providers">Providers &amp; Models</DocsLinkInline>),
        just start the TUI as before — no key needed:
      </DocsP>

      <CodeBlock language="bash" label="start the TUI" prompt code={'bharatcode'} />

      <DocsP>
        Inside the session, use <InlineCode>/model</InlineCode> to select your
        local model, then work exactly as you would with a hosted provider. Every
        token is processed locally, so this is the path for air-gapped machines
        and code that must stay on-device.
      </DocsP>

      <DocsCallout tone="note" title="Data stays in India — or on your machine">
        BharatCode never phones home. It connects only to the model endpoints you
        configure. A local provider keeps everything on your machine; a hosted
        open-weight or India-hosted provider keeps your data with a model you
        chose.
      </DocsCallout>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="headless">Headless mode for scripts and CI</DocsH2>
      <DocsP>
        Outside the TUI, <InlineCode>bharatcode run</InlineCode> executes a single
        prompt non-interactively and prints the result. This is the building block
        for scripts, git hooks, and CI jobs:
      </DocsP>

      <CodeBlock
        language="bash"
        label="one-shot prompt"
        prompt
        code={'bharatcode run "fix the failing test"'}
      />

      <DocsH3 id="json-events">Structured events with --json</DocsH3>
      <DocsP>
        For automation, add <InlineCode>--json</InlineCode> to emit a stream of
        newline-delimited JSON (NDJSON) events instead of human-readable output.
        Each line is a complete JSON object, so you can pipe it straight into a
        log processor or parse it line by line:
      </DocsP>

      <CodeBlock
        language="bash"
        label="NDJSON event stream for CI"
        prompt
        code={'bharatcode run --json "fix the failing test"'}
      />

      <DocsP>
        To capture just the agent&apos;s final answer to a file — handy for
        posting a summary back to a PR or storing a result —
        add <InlineCode>--output-last-message</InlineCode>:
      </DocsP>

      <CodeBlock
        language="bash"
        label="write the final message to a file"
        prompt
        code={
          'bharatcode run --output-last-message result.txt "fix the failing test"'
        }
      />

      <DocsCallout tone="tip" title="CI tip">
        Combine headless mode with{' '}
        <DocsLinkInline href="/docs/permissions">permission modes</DocsLinkInline>{' '}
        and the{' '}
        <DocsLinkInline href="/docs/budget">INR budget gate</DocsLinkInline> so an
        automated run can only touch what you allow and can&apos;t blow past a
        monthly spend cap.
      </DocsCallout>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="resuming">Resuming a session</DocsH2>
      <DocsP>
        BharatCode keeps your sessions, so you can pick up where you left off. The{' '}
        <InlineCode>--continue</InlineCode> flag resumes the most recent session
        with its full history intact:
      </DocsP>

      <CodeBlock
        language="bash"
        label="resume the latest session"
        prompt
        code={'bharatcode --continue'}
      />

      <DocsP>
        Inside the TUI you can also browse and restore older sessions with the{' '}
        <InlineCode>/sessions</InlineCode> command, or branch the current one into
        a new line of work with <InlineCode>/fork</InlineCode>. See{' '}
        <DocsLinkInline href="/docs/sessions">Sessions &amp; Fork</DocsLinkInline>{' '}
        for the details.
      </DocsP>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="tui-tour">A quick tour of the TUI</DocsH2>
      <DocsP>
        The interactive UI has three things to know: the chat transcript, the
        prompt input, and a footer that always shows your session cost in rupees.
        Everything else is driven by slash commands.
      </DocsP>

      <DocsList>
        <DocsListItem>
          <strong className="font-semibold text-fg">Chat transcript.</strong> Your
          messages, the agent&apos;s reasoning, tool calls, and proposed diffs
          scroll here. File edits are shown as diffs you can review before they
          land.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Prompt input.</strong> Type a
          request, or start a line with <InlineCode>/</InlineCode> to run a slash
          command.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">INR cost footer.</strong> A
          running, rupee-denominated cost ledger for the session, so spend is
          always visible. Pair it with a monthly{' '}
          <DocsLinkInline href="/docs/budget">budget gate</DocsLinkInline>.
        </DocsListItem>
      </DocsList>

      <Terminal
        title="bharatcode — ~/project"
        lines={[
          { type: 'output', text: 'you › refactor the auth middleware' },
          {
            type: 'output',
            text: 'bharatcode › editing internal/auth/middleware.go …',
          },
          { type: 'success', text: '✓ 1 file changed · diff shown above' },
          { type: 'spacer' },
          {
            type: 'comment',
            text: '────────────────────────────────────────────',
          },
          {
            type: 'output',
            text: 'DeepSeek · read-only · session ₹1.27 / ₹500 budget',
          },
        ]}
      />

      <DocsH3 id="slash-commands">Slash commands</DocsH3>
      <DocsP>
        Slash commands run from the prompt and control the session itself. A few
        you&apos;ll reach for immediately:
      </DocsP>

      <DocsList>
        <DocsListItem>
          <InlineCode>/help</InlineCode> — list every available command.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/model</InlineCode> — switch the provider or model for this
          session.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/diff</InlineCode> — review pending changes;{' '}
          <InlineCode>/status</InlineCode> — see session and context state.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/permissions</InlineCode> — change the approval mode between
          read-only, auto, and full.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal</InlineCode> — set and run an autonomous, bounded task
          loop;{' '}
          <DocsLinkInline href="/docs/goal">learn more</DocsLinkInline>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/clear</InlineCode> to reset the conversation,{' '}
          <InlineCode>/save</InlineCode> to persist it, and{' '}
          <InlineCode>/quit</InlineCode> to exit.
        </DocsListItem>
      </DocsList>

      <DocsP>
        You can also define your own <InlineCode>/&lt;name&gt;</InlineCode> prompts
        as Markdown files in <InlineCode>~/.bharatcode/prompts/</InlineCode>. The
        full list — including <InlineCode>/agent</InlineCode>,{' '}
        <InlineCode>/fork</InlineCode>, <InlineCode>/budget</InlineCode>,{' '}
        <InlineCode>/yolo</InlineCode>, and custom prompts — lives in{' '}
        <DocsLinkInline href="/docs/commands">
          TUI &amp; Slash Commands
        </DocsLinkInline>
        .
      </DocsP>

      {/* ----------------------------------------------------------------- */}
      <DocsH2 id="next-steps">Next steps</DocsH2>
      <DocsList>
        <DocsListItem>
          <DocsLinkInline href="/docs/configuration">Config files</DocsLinkInline>{' '}
          — tune the global and per-project config, and overlay named profiles.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/providers">
            Providers &amp; Models
          </DocsLinkInline>{' '}
          — add DeepSeek, Moonshot/Kimi, Groq, Ollama, and more.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/tools">Built-in Tools</DocsLinkInline> and{' '}
          <DocsLinkInline href="/docs/commands">Slash Commands</DocsLinkInline> —
          everything the agent can do.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/permissions">Permissions</DocsLinkInline> —
          control what edits and commands need approval.
        </DocsListItem>
      </DocsList>
    </DocsPage>
  );
}
