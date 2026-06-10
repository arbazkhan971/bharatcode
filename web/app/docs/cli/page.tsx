import type { Metadata } from 'next';

import { CodeBlock } from '@/app/components/ui/CodeBlock';
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
  title: 'CLI Reference',
  description:
    'Complete reference for the bharatcode command line: the interactive TUI, headless run (--json, --output-last-message, BHARATCODE_HEADLESS), session resume with --continue, and the login, logout, auth chatgpt, models, sessions, stats, budget, update-providers, config, doctor, and version subcommands.',
};

/**
 * Top-of-page quick reference. Mirrors the CommandTable pattern from the
 * commands page so the full subcommand set is scannable before the prose
 * documents each one in turn. Kept strictly to the verified subcommands — no
 * invented flags or commands.
 */
type SubcommandRow = {
  command: string;
  summary: string;
};

const SUBCOMMANDS: SubcommandRow[] = [
  {
    command: 'bharatcode',
    summary: 'Launch the interactive terminal UI in the current directory.',
  },
  {
    command: 'bharatcode run',
    summary: 'Run the agent headlessly against a single prompt.',
  },
  { command: 'bharatcode login', summary: 'Authenticate a provider.' },
  { command: 'bharatcode logout', summary: 'Sign out of a provider.' },
  {
    command: 'bharatcode auth chatgpt',
    summary: 'Sign in with a ChatGPT subscription (experimental).',
  },
  {
    command: 'bharatcode models',
    summary: 'List the models available from your configured providers.',
  },
  {
    command: 'bharatcode sessions',
    summary: 'List and manage your saved sessions.',
  },
  {
    command: 'bharatcode stats',
    summary: 'Show usage statistics from the cost ledger.',
  },
  {
    command: 'bharatcode budget',
    summary: 'Show the INR-aware cost ledger and monthly budget.',
  },
  {
    command: 'bharatcode update-providers',
    summary: 'Refresh the provider and model catalogue.',
  },
  {
    command: 'bharatcode config',
    summary: 'Inspect your merged configuration.',
  },
  {
    command: 'bharatcode doctor',
    summary: 'Diagnose your environment and configuration.',
  },
  { command: 'bharatcode version', summary: 'Print the installed version.' },
];

export default function CliReferencePage() {
  return (
    <DocsPage
      eyebrow="Reference"
      title="CLI Reference"
      lede={
        <>
          Every BharatCode workflow starts at the{' '}
          <InlineCode>bharatcode</InlineCode> command. With no arguments it opens
          the interactive TUI; with a subcommand it runs headlessly, manages
          providers and sessions, or reports on your environment. This page is
          the complete reference for the command line &mdash; the TUI itself and
          its slash commands are covered on the{' '}
          <DocsLinkInline href="/docs/commands">
            TUI &amp; Slash Commands
          </DocsLinkInline>{' '}
          page.
        </>
      }
      prev={{ href: '/docs/hooks', label: 'Hooks' }}
    >
      <DocsH2 id="synopsis">Synopsis</DocsH2>
      <DocsP>
        BharatCode follows the familiar <InlineCode>git</InlineCode>-style shape:
        a root command, an optional subcommand, then flags and arguments.
        Running it bare launches the interactive UI; pass a subcommand to do
        anything else.
      </DocsP>
      <CodeBlock
        language="text"
        label="usage"
        code={`bharatcode                       # launch the interactive TUI
bharatcode <subcommand> [flags]  # run a specific command
bharatcode [global flags]        # e.g. --continue, --yolo, --profile`}
      />
      <DocsP>Here is the full set of subcommands at a glance:</DocsP>
      <SubcommandTable />
      <DocsCallout tone="note" title="Verified surface only">
        This reference documents the flags BharatCode actually exposes. Where a
        subcommand takes no documented flags, none are listed &mdash; run{' '}
        <InlineCode>bharatcode &lt;subcommand&gt; --help</InlineCode> in your
        terminal to see the live options for the version you have installed.
      </DocsCallout>

      <DocsH2 id="bharatcode">bharatcode</DocsH2>
      <DocsP>
        With no arguments, <InlineCode>bharatcode</InlineCode> opens the
        interactive terminal UI in your current working directory. You talk to
        the agent in plain language and steer the session with{' '}
        <DocsLinkInline href="/docs/commands">slash commands</DocsLinkInline>.
        Project instructions from{' '}
        <DocsLinkInline href="/docs/agents-md">AGENTS.md</DocsLinkInline> (and{' '}
        <InlineCode>CLAUDE.md</InlineCode>) are ingested into the system prompt
        automatically.
      </DocsP>
      <CodeBlock language="bash" label="terminal" prompt code={'bharatcode'} />

      <DocsH2 id="run">bharatcode run</DocsH2>
      <DocsP>
        Runs the agent headlessly against a single prompt and exits &mdash; the
        non-interactive counterpart to the TUI. Use it in scripts, CI, Makefiles,
        or any place you want a one-shot agent run without a terminal UI.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode run "add a unit test for parseConfig"'}
      />

      <DocsH3 id="run-flags">Flags</DocsH3>
      <DocsList>
        <DocsListItem>
          <InlineCode>--json</InlineCode> &mdash; emit machine-readable NDJSON
          events (one JSON object per line) instead of human-formatted output, so
          you can stream and parse the run programmatically.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>--output-last-message &lt;file&gt;</InlineCode> &mdash; write
          the agent&apos;s final message to <InlineCode>&lt;file&gt;</InlineCode>,
          which is handy for capturing just the answer from a script.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Emit a stream of NDJSON events to consume the run from another program:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode run --json "summarize the changes in this repo"'}
      />
      <DocsP>
        Or capture only the final message to a file for use in a later step:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={
          'bharatcode run "write release notes for v1.2" --output-last-message notes.md'
        }
      />
      <DocsCallout tone="tip" title="Scripting the agent">
        <InlineCode>--json</InlineCode> and{' '}
        <InlineCode>--output-last-message</InlineCode> compose: stream NDJSON for
        live progress while still dropping the final answer into a file. Combine
        them with the global <InlineCode>--profile</InlineCode> flag to pin a
        specific model and budget for automated runs.
      </DocsCallout>

      <DocsH3 id="run-changed-files">Changed-file summary</DocsH3>
      <DocsP>
        Every <InlineCode>bharatcode run</InlineCode> finishes by printing a{' '}
        <InlineCode>Changed files:</InlineCode> block listing each path the agent
        created, modified, or deleted during the run, so a CI step or a reviewer
        can see the blast radius at a glance. The summary is emitted on both the
        human-formatted and <InlineCode>--json</InlineCode> output paths, and a
        run that touched no files prints nothing.
      </DocsP>
      <CodeBlock
        language="text"
        label="output"
        code={`Changed files:
- internal/config/parse.go (modified)
- internal/config/parse_test.go (created)`}
      />

      <DocsH3 id="run-verification">Verify before done</DocsH3>
      <DocsP>
        The agent does not claim success on unverified work. After it changes
        code it runs the project&apos;s own test, build, and lint commands and
        ends the turn with an explicit status &mdash; <strong>Verified</strong>{' '}
        (naming the commands it ran and the result it observed),{' '}
        <strong>Failed</strong> (the checks ran but did not pass), or{' '}
        <strong>Skipped</strong> with a named reason (
        <InlineCode>no_test_command</InlineCode>,{' '}
        <InlineCode>dependency_unavailable</InlineCode>, or{' '}
        <InlineCode>user_opted_out</InlineCode>). The same policy applies in the
        TUI.
      </DocsP>

      <DocsH2 id="login">bharatcode login</DocsH2>
      <DocsP>
        Authenticates a provider so the agent can call its models. Most providers
        read their key from an environment variable (for example{' '}
        <InlineCode>DEEPSEEK_API_KEY</InlineCode> or{' '}
        <InlineCode>GROQ_API_KEY</InlineCode>); see{' '}
        <DocsLinkInline href="/docs/providers">
          Providers &amp; Models
        </DocsLinkInline>{' '}
        for how each provider is wired up.
      </DocsP>
      <CodeBlock language="bash" label="terminal" prompt code={'bharatcode login'} />

      <DocsH2 id="logout">bharatcode logout</DocsH2>
      <DocsP>Signs out of a provider, clearing stored credentials.</DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode logout'}
      />

      <DocsH2 id="auth-chatgpt">bharatcode auth chatgpt</DocsH2>
      <DocsP>
        Signs in with a ChatGPT subscription so the{' '}
        <InlineCode>chatgpt</InlineCode> provider can drive a model with your own
        plan instead of an API key. It opens your browser for an OAuth (PKCE)
        flow, runs a loopback callback server, and stores the resulting tokens.
        Pass <InlineCode>--status</InlineCode> to print the signed-in account,
        plan, and token state, or <InlineCode>--logout</InlineCode> to remove the
        stored credentials.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={`bharatcode auth chatgpt            # OAuth (PKCE) sign-in via your browser
bharatcode auth chatgpt --status   # show the signed-in account, plan, and token state
bharatcode auth chatgpt --logout   # remove the stored credentials`}
      />
      <DocsCallout tone="warn" title="Experimental and unsupported">
        Sign in with ChatGPT relies on undocumented OpenAI endpoints that may
        change or break without notice, falls outside OpenAI&apos;s terms for
        third-party clients, and is intended for personal single-account use only
        (no account pooling). Once signed in,{' '}
        <InlineCode>bharatcode doctor</InlineCode> reports the subscription
        status alongside the rest of your environment.
      </DocsCallout>

      <DocsH2 id="models">bharatcode models</DocsH2>
      <DocsP>
        Lists the models available from your configured providers &mdash; the
        same catalogue the in-TUI <InlineCode>/model</InlineCode> picker draws
        from. Use it to confirm a provider is set up correctly and to find the
        exact model identifier to reference in your{' '}
        <DocsLinkInline href="/docs/configuration">config</DocsLinkInline>.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode models'}
      />

      <DocsH2 id="sessions">bharatcode sessions</DocsH2>
      <DocsP>
        Lists and manages your saved sessions from the command line &mdash; the
        shell-side companion to the TUI&apos;s <InlineCode>/sessions</InlineCode>{' '}
        command. To jump straight back into the most recent session instead of
        browsing, use the global{' '}
        <DocsLinkInline href="#global-options">
          <InlineCode>--continue</InlineCode>
        </DocsLinkInline>{' '}
        flag. See{' '}
        <DocsLinkInline href="/docs/sessions">Sessions &amp; Fork</DocsLinkInline>{' '}
        for how sessions are stored, resumed, and branched.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode sessions'}
      />

      <DocsH2 id="stats">bharatcode stats</DocsH2>
      <DocsP>
        Reports usage statistics drawn from BharatCode&apos;s cost ledger, so you
        can see how much you have used across sessions. For the spend total
        against your monthly limit specifically, use{' '}
        <InlineCode>bharatcode budget</InlineCode>.
      </DocsP>
      <CodeBlock language="bash" label="terminal" prompt code={'bharatcode stats'} />

      <DocsH2 id="budget">bharatcode budget</DocsH2>
      <DocsP>
        Shows the INR-aware cost ledger and your monthly budget. BharatCode
        tracks spend in rupee terms and gates new work against a monthly limit,
        and this command is where you check how much of the month&apos;s budget
        remains. The same view is available inside the TUI as{' '}
        <InlineCode>/budget</InlineCode>.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode budget'}
      />

      <DocsH2 id="update-providers">bharatcode update-providers</DocsH2>
      <DocsP>
        Refreshes the provider and model catalogue. Run it after a provider adds
        new models so that <InlineCode>bharatcode models</InlineCode> and the{' '}
        <InlineCode>/model</InlineCode> picker reflect the latest options.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode update-providers'}
      />

      <DocsH2 id="config">bharatcode config</DocsH2>
      <DocsP>
        Inspects your configuration. BharatCode merges a global{' '}
        <InlineCode>~/.config/bharatcode/config.json</InlineCode> with a
        project-level <InlineCode>./.bharatcode.json</InlineCode>, optionally
        overlaid by a named <InlineCode>--profile</InlineCode>, and this command
        helps you confirm what the effective configuration is. The full layering
        rules, fields, and examples live on the{' '}
        <DocsLinkInline href="/docs/configuration">
          Config files
        </DocsLinkInline>{' '}
        page.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode config'}
      />

      <DocsH2 id="doctor">bharatcode doctor</DocsH2>
      <DocsP>
        Runs environment and configuration diagnostics &mdash; a quick health
        check you can run when something is not working. It is the first thing to
        try when a provider, model, or integration is behaving unexpectedly. Each
        check is reported as <InlineCode>[OK]</InlineCode> or{' '}
        <InlineCode>[WARN]</InlineCode> (warnings are non-fatal and never block
        startup). When a <InlineCode>chatgpt</InlineCode> provider is enabled in
        your config,{' '}
        <InlineCode>doctor</InlineCode> also reports a{' '}
        <strong>ChatGPT subscription</strong> line &mdash; signed in as your
        account on its plan, or a warning to run{' '}
        <DocsLinkInline href="#auth-chatgpt">
          <InlineCode>bharatcode auth chatgpt</InlineCode>
        </DocsLinkInline>{' '}
        when you are not signed in yet.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode doctor'}
      />

      <DocsH2 id="version">bharatcode version</DocsH2>
      <DocsP>
        Prints the installed BharatCode version &mdash; useful when reporting an
        issue or confirming an{' '}
        <DocsLinkInline href="/docs/installation">upgrade</DocsLinkInline> took
        effect.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode version'}
      />

      <DocsH2 id="global-options">Global options</DocsH2>
      <DocsP>
        A few flags apply across the CLI rather than to a single subcommand:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>--continue</InlineCode> &mdash; resume the latest session.
          Running <InlineCode>bharatcode --continue</InlineCode> reopens your most
          recent conversation instead of starting fresh.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>--profile &lt;name&gt;</InlineCode> &mdash; overlay a named
          configuration profile on top of your global and project config, so you
          can switch between, say, a cheap default and a premium setup.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>--yolo</InlineCode> &mdash; bypass approval prompts for the
          run. It is fast but unguarded; see{' '}
          <DocsLinkInline href="/docs/permissions">Permissions</DocsLinkInline>{' '}
          for the safer approval modes.
        </DocsListItem>
      </DocsList>
      <DocsP>Pick up exactly where you left off:</DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode --continue'}
      />
      <DocsP>Launch the TUI with a named profile applied:</DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode --profile work'}
      />
      <DocsCallout tone="warn" title="--yolo skips approvals">
        <InlineCode>--yolo</InlineCode> removes the approval gate entirely, so the
        agent can edit files and run commands without asking. Reserve it for
        throwaway work or fully trusted tasks &mdash; prefer the{' '}
        <DocsLinkInline href="/docs/permissions">auto mode</DocsLinkInline> when
        you want speed with a check on the riskiest actions.
      </DocsCallout>

      <DocsH3 id="headless">Headless &amp; CI runs</DocsH3>
      <DocsP>
        For scripts, CI logs, and PTY captures, set{' '}
        <InlineCode>BHARATCODE_HEADLESS=1</InlineCode> to force the interactive
        TUI onto a quiet, non-rendering path so the output does not accumulate
        live-redraw noise. BharatCode also selects this path automatically when{' '}
        <InlineCode>CI</InlineCode> is set or the terminal reports as{' '}
        <InlineCode>dumb</InlineCode>. The variable only gates the TUI&rsquo;s
        renderer; <InlineCode>bharatcode run</InlineCode> is already
        non-interactive and is unaffected.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'BHARATCODE_HEADLESS=1 bharatcode'}
      />
    </DocsPage>
  );
}

/**
 * Quick-reference subcommand table. Scrolls horizontally on narrow screens so
 * the two columns stay readable on mobile, matching the Providers and Commands
 * table pattern.
 */
function SubcommandTable() {
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-bg-elevated">
      <table className="w-full min-w-[32rem] border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-border text-xs uppercase tracking-wider text-faint">
            <th scope="col" className="px-4 py-3 font-medium">
              Command
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              What it does
            </th>
          </tr>
        </thead>
        <tbody>
          {SUBCOMMANDS.map((c) => (
            <tr
              key={c.command}
              className="border-b border-border/60 transition-colors last:border-0 hover:bg-surface/40"
            >
              <th
                scope="row"
                className="whitespace-nowrap px-4 py-3 align-top font-medium"
              >
                <code className="font-mono text-[0.8125rem] text-fg">
                  {c.command}
                </code>
              </th>
              <td className="px-4 py-3 text-muted">{c.summary}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
