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
  title: 'TUI & Slash Commands',
  description:
    'BharatCode’s interactive terminal UI and its slash commands — /model, /agent, /sessions, /fork, /diff, /status, /goal, /permissions, /budget, /yolo, /save, and custom Markdown prompts you invoke as /<name>.',
};

/**
 * Quick-reference of every built-in slash command. Mirrors the table pattern
 * used on the Providers page so the list is scannable before the prose dives
 * into each one. Commands that have a dedicated docs page are cross-linked in
 * the prose rather than fully re-documented here.
 */
type CommandRow = {
  command: string;
  summary: string;
};

const COMMANDS: CommandRow[] = [
  { command: '/help', summary: 'List commands and key bindings.' },
  { command: '/clear', summary: 'Clear the screen and start a fresh context.' },
  { command: '/model', summary: 'Switch the active model.' },
  { command: '/agent', summary: 'Switch the active agent.' },
  { command: '/sessions', summary: 'Browse and restore a past session.' },
  { command: '/fork', summary: 'Branch the current session into a new one.' },
  { command: '/diff', summary: 'Show diffs of recent edits.' },
  { command: '/status', summary: 'Show session, model, and context status.' },
  {
    command: '/goal',
    summary: 'Set, run, clear, or stop a bounded autonomous goal.',
  },
  {
    command: '/permissions',
    summary: 'Set the approval mode: read-only, auto, or full.',
  },
  { command: '/budget', summary: 'View the cost ledger and monthly budget.' },
  { command: '/yolo', summary: 'Bypass approvals for this session.' },
  { command: '/save', summary: 'Save the current session.' },
  { command: '/quit', summary: 'Exit the TUI.' },
];

export default function CommandsPage() {
  return (
    <DocsPage
      eyebrow="Usage"
      title="TUI & Slash Commands"
      lede={
        <>
          Running <InlineCode>bharatcode</InlineCode> with no arguments launches
          the interactive terminal UI. Inside it you talk to the agent in plain
          language and steer the session with slash commands &mdash; switch
          models, branch sessions, review diffs, set a budget, or hand the agent
          an autonomous goal, all without leaving the keyboard.
        </>
      }
      prev={{ href: '/docs/agents-md', label: 'AGENTS.md' }}
      next={{ href: '/docs/tools', label: 'Built-in Tools' }}
    >
      <DocsH2 id="starting-the-tui">Starting the TUI</DocsH2>
      <DocsP>
        With no arguments, <InlineCode>bharatcode</InlineCode> opens the terminal
        UI in your current directory. Type a request, hit enter, and the agent
        reads files, runs tools, and proposes edits &mdash; pausing for your
        approval according to the current{' '}
        <DocsLinkInline href="/docs/permissions">permission mode</DocsLinkInline>.
      </DocsP>
      <CodeBlock language="bash" label="terminal" prompt code={'bharatcode'} />
      <DocsP>
        To pick up where you left off instead of starting empty, resume the most
        recent session straight from the shell:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode --continue'}
      />
      <DocsCallout tone="note" title="Headless mode">
        Everything below lives inside the interactive UI. If you want to script
        the agent &mdash; in CI, a Makefile, or a one-off &mdash; use{' '}
        <InlineCode>bharatcode run &quot;&lt;prompt&gt;&quot;</InlineCode>{' '}
        instead. See the{' '}
        <DocsLinkInline href="/docs/cli">CLI reference</DocsLinkInline> for{' '}
        <InlineCode>run</InlineCode> and its flags.
      </DocsCallout>

      <DocsH2 id="command-reference">Command reference</DocsH2>
      <DocsP>
        Slash commands are typed at the prompt inside the TUI. Type{' '}
        <InlineCode>/</InlineCode> to start one; <InlineCode>/help</InlineCode>{' '}
        lists every command and key binding. Here is the full set at a glance:
      </DocsP>
      <CommandTable />
      <DocsP>
        The sections below describe each command in turn. A handful of areas have
        their own deep-dive pages &mdash;{' '}
        <DocsLinkInline href="/docs/goal">autonomous goals</DocsLinkInline>,{' '}
        <DocsLinkInline href="/docs/sessions">sessions and forking</DocsLinkInline>
        , and{' '}
        <DocsLinkInline href="/docs/permissions">permissions</DocsLinkInline>{' '}
        &mdash; and are summarized here with a link to the full treatment.
      </DocsP>

      <DocsH2 id="session-basics">Session basics</DocsH2>

      <DocsH3 id="help">/help</DocsH3>
      <DocsP>
        Lists the available slash commands along with key bindings. Start here
        when you forget the name of a command or want to see what the UI can do.
      </DocsP>

      <DocsH3 id="clear">/clear</DocsH3>
      <DocsP>
        Clears the conversation and starts a fresh context. Use it when you
        finish one task and want to begin another without the previous
        conversation influencing the agent &mdash; it gives you a clean slate
        without quitting and relaunching.
      </DocsP>

      <DocsH3 id="status">/status</DocsH3>
      <DocsP>
        Shows the current session at a glance &mdash; the active model and agent,
        the approval mode in effect, and where the session stands. It is the
        quickest way to confirm what BharatCode is about to do before you send a
        prompt.
      </DocsP>

      <DocsH3 id="quit">/quit</DocsH3>
      <DocsP>
        Exits the TUI. Your session history is preserved and can be reopened
        later with <InlineCode>/sessions</InlineCode> or{' '}
        <InlineCode>bharatcode --continue</InlineCode>.
      </DocsP>

      <DocsH2 id="model-and-agent">Model &amp; agent</DocsH2>

      <DocsH3 id="model">/model</DocsH3>
      <DocsP>
        Opens the model picker so you can switch the active model mid-session.
        The list is drawn from the providers in your{' '}
        <DocsLinkInline href="/docs/configuration">config</DocsLinkInline> &mdash;
        the same set you see from{' '}
        <InlineCode>bharatcode models</InlineCode>. Switch to a cheaper
        open-weight model for routine edits and a stronger one for hard problems
        without restarting. See{' '}
        <DocsLinkInline href="/docs/providers">Providers &amp; Models</DocsLinkInline>{' '}
        for how the catalogue is defined.
      </DocsP>

      <DocsH3 id="agent">/agent</DocsH3>
      <DocsP>
        Switches the active agent for the session. Use it to move between the
        agent personas available in your configuration without leaving the TUI.
      </DocsP>

      <DocsH2 id="sessions-and-history">Sessions &amp; history</DocsH2>

      <DocsH3 id="sessions">/sessions</DocsH3>
      <DocsP>
        Browse your past sessions and restore one to continue the conversation
        where it ended. This is the in-TUI counterpart to launching with{' '}
        <InlineCode>bharatcode --continue</InlineCode>, except you choose{' '}
        <em>which</em> session to reopen rather than always taking the latest.
        For the full story on how sessions are stored and resumed, see{' '}
        <DocsLinkInline href="/docs/sessions">Sessions &amp; Fork</DocsLinkInline>.
      </DocsP>

      <DocsH3 id="fork">/fork</DocsH3>
      <DocsP>
        Branches the current session into a new one. Forking lets you explore an
        alternative approach from a known-good point &mdash; the original session
        stays intact, so you can try something risky in the branch and come back
        if it does not pan out. The{' '}
        <DocsLinkInline href="/docs/sessions">Sessions &amp; Fork</DocsLinkInline>{' '}
        page covers branching in depth.
      </DocsP>

      <DocsH3 id="save">/save</DocsH3>
      <DocsP>
        Saves the current session so you can return to it later via{' '}
        <InlineCode>/sessions</InlineCode>.
      </DocsP>

      <DocsH2 id="reviewing-changes">Reviewing changes</DocsH2>

      <DocsH3 id="diff">/diff</DocsH3>
      <DocsP>
        Shows the diffs for the agent&apos;s recent edits, so you can review
        exactly what changed before moving on or committing. It is the fastest
        way to audit a multi-file edit without switching to a separate Git tool.
      </DocsP>

      <DocsH2 id="autonomy-and-control">Autonomy &amp; control</DocsH2>

      <DocsH3 id="goal">/goal</DocsH3>
      <DocsP>
        Hands the agent a goal and lets it iterate autonomously within bounds.{' '}
        <InlineCode>/goal</InlineCode> takes a sub-command:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>/goal set &lt;goal&gt;</InlineCode> &mdash; define the goal
          to pursue.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal run</InlineCode> &mdash; start the autonomous loop,
          iterating toward the goal.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal stop</InlineCode> &mdash; halt the loop while keeping
          the goal set.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>/goal clear</InlineCode> &mdash; clear the current goal.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Because the iteration is bounded, the agent works toward the goal in a
        contained loop rather than running open-ended. The{' '}
        <DocsLinkInline href="/docs/goal">/goal Autonomous Mode</DocsLinkInline>{' '}
        page walks through the workflow end to end.
      </DocsP>

      <DocsH3 id="permissions">/permissions</DocsH3>
      <DocsP>
        Sets the approval mode that governs how much the agent can do on its own.
        There are three modes:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>read-only</InlineCode> &mdash; the agent can inspect your
          project but cannot make changes without approval.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>auto</InlineCode> &mdash; routine actions proceed
          automatically while riskier ones still ask.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>full</InlineCode> &mdash; the agent acts without pausing for
          per-action approval.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Approval modes sit on top of BharatCode&apos;s ask/allow/deny permission
        system. See{' '}
        <DocsLinkInline href="/docs/permissions">Permissions</DocsLinkInline> for
        how rules, scopes, and modes fit together.
      </DocsP>

      <DocsH3 id="yolo">/yolo</DocsH3>
      <DocsP>
        Bypasses approval prompts for the session &mdash; the in-TUI equivalent of
        launching with the <InlineCode>--yolo</InlineCode> flag. It is fast but
        unguarded, so reach for it only on throwaway work or when you fully trust
        the task.
      </DocsP>
      <DocsCallout tone="warn" title="Use with care">
        <InlineCode>/yolo</InlineCode> removes the approval gate, so the agent can
        edit files and run commands without asking. Prefer{' '}
        <InlineCode>/permissions auto</InlineCode> when you want speed but still
        want a check on the riskiest actions.
      </DocsCallout>

      <DocsH2 id="cost-control">Cost control</DocsH2>

      <DocsH3 id="budget">/budget</DocsH3>
      <DocsP>
        Shows the cost ledger and your monthly budget. BharatCode tracks spend in
        INR-aware terms and gates against a monthly limit, so{' '}
        <InlineCode>/budget</InlineCode> is where you check how much a session is
        costing and how much of the month&apos;s budget remains.
      </DocsP>

      <DocsH2 id="custom-prompts">Custom Markdown prompts</DocsH2>
      <DocsP>
        Beyond the built-ins, you can define your own slash commands as Markdown
        files. Drop a file at{' '}
        <InlineCode>~/.bharatcode/prompts/&lt;name&gt;.md</InlineCode> and it
        becomes available in the TUI as <InlineCode>/&lt;name&gt;</InlineCode>.
        The file&apos;s contents are the prompt sent to the agent, with two kinds
        of placeholder interpolated at call time:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>{'{{input}}'}</InlineCode> &mdash; replaced by everything you
          type after the command name.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>{'{{var}}'}</InlineCode> &mdash; a named placeholder you can
          reference anywhere in the prompt body.
        </DocsListItem>
      </DocsList>
      <DocsP>
        For example, create a reusable code-review command. The file name becomes
        the command name &mdash;{' '}
        <InlineCode>review.md</InlineCode> &rarr;{' '}
        <InlineCode>/review</InlineCode>:
      </DocsP>
      <CodeBlock
        language="markdown"
        label="~/.bharatcode/prompts/review.md"
        code={`Review the following changes for correctness, security, and clarity.

Focus area: {{focus}}

Pay special attention to error handling and edge cases, then list any
issues you find as a short, prioritized checklist.

Changes to review:
{{input}}`}
      />
      <DocsP>
        Invoke it from the TUI like any built-in command. Everything after{' '}
        <InlineCode>/review</InlineCode> fills <InlineCode>{'{{input}}'}</InlineCode>
        :
      </DocsP>
      <CodeBlock
        language="text"
        label="bharatcode TUI"
        code={'/review the diff in internal/auth/session.go'}
      />
      <DocsCallout tone="tip" title="Project-wide prompts">
        Because prompts are just Markdown files, you can keep a shared library in
        your dotfiles or check team conventions into{' '}
        <DocsLinkInline href="/docs/agents-md">AGENTS.md</DocsLinkInline> and a
        prompt registry, so everyone runs the same{' '}
        <InlineCode>/review</InlineCode> or <InlineCode>/explain</InlineCode>{' '}
        flow.
      </DocsCallout>
    </DocsPage>
  );
}

/**
 * Quick-reference command table. Scrolls horizontally on narrow screens so the
 * two columns stay readable on mobile, matching the Providers table pattern.
 */
function CommandTable() {
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-bg-elevated">
      <table className="w-full min-w-[28rem] border-collapse text-left text-sm">
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
          {COMMANDS.map((c) => (
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
