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
  title: 'Permissions & Cost',
  description:
    'How BharatCode gates tool calls — ask / allow / deny with once / session / project / forever scopes, the read-only / auto / full approval modes set via /permissions, config deny-list and auto-approve patterns, and the INR cost ledger with a monthly budget gate.',
};

/**
 * Permissions & Cost — the safety and accounting layer.
 *
 * Two intertwined systems: (1) the permission gate that decides whether a tool
 * call runs, asks, or is refused, and (2) the INR cost ledger that prices every
 * model call in rupees and stops you at a monthly budget. The config examples
 * mirror the `permissions` block already shipped on the Configuration page —
 * a `mode` plus a tool→verb map — rather than inventing new schema fields.
 */

const PERMISSIONS_CONFIG = `{
  "permissions": {
    "mode": "auto",          // read-only | auto | full
    "view": "allow",         // read tools: safe to auto-run
    "grep": "allow",
    "edit": "allow",         // exact match — auto-approve every edit call
    "multiedit": "allow",
    "write": "ask",          // ask before creating or overwriting files
    "bash": "ask"            // ask before each shell command
  }
}`;

const WILDCARD_CONFIG = `{
  "permissions": {
    "mode": "auto",
    "bash": "deny",          // exact: refuse the bash tool outright
    "web_fetch": "deny",     // and keep this repo off the network
    "web_search": "deny",
    "edit": "allow"          // but let edits through without asking
  }
}`;

const BUDGET_CONFIG = `{
  "budget": {
    "monthly_limit_inr": 2000
  }
}`;

export default function PermissionsPage() {
  return (
    <DocsPage
      eyebrow="Usage"
      title="Permissions & Cost"
      lede={
        <>
          BharatCode never silently runs commands or rewrites files behind your
          back. Every tool call passes through a permission gate, and every model
          call is priced in rupees against a monthly budget. This page covers
          both: how the <InlineCode>ask</InlineCode> / <InlineCode>allow</InlineCode>{' '}
          / <InlineCode>deny</InlineCode> gate works, the approval modes you switch
          between with <InlineCode>/permissions</InlineCode>, and the INR cost
          ledger that keeps spend visible and bounded.
        </>
      }
      prev={{ href: '/docs/sessions', label: 'Sessions & Fork' }}
      next={{ href: '/docs/mcp', label: 'MCP' }}
    >
      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="permission-model">The permission model</DocsH2>
      <DocsP>
        When the agent wants to do something that touches your machine — edit a
        file, overwrite one, or run a shell command — that call is intercepted
        before it executes. BharatCode resolves the call to one of three verbs:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>ask</InlineCode> — pause and prompt you to approve or
          reject the call. This is the default for the actions that change your
          system.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>allow</InlineCode> — run the call immediately, without
          interrupting you.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>deny</InlineCode> — refuse the call outright. The agent is
          told it was denied and works around it rather than waiting on you.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Read-only tools — <InlineCode>view</InlineCode>,{' '}
        <InlineCode>ls</InlineCode>, <InlineCode>glob</InlineCode>,{' '}
        <InlineCode>grep</InlineCode>, <InlineCode>diagnostics</InlineCode>,{' '}
        <InlineCode>todo</InlineCode>, and the web tools (
        <InlineCode>web_fetch</InlineCode>, <InlineCode>web_search</InlineCode>) —
        do not change anything on disk, so by default they run without asking. The
        four tools that <em>do</em> change your machine —{' '}
        <InlineCode>edit</InlineCode>, <InlineCode>multiedit</InlineCode>,{' '}
        <InlineCode>write</InlineCode>, and <InlineCode>bash</InlineCode> — are
        the ones the gate exists for. See{' '}
        <DocsLinkInline href="/docs/tools">Built-in Tools</DocsLinkInline> for the
        full read-only-vs-gated breakdown.
      </DocsP>

      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="scopes">Remembering an answer: scopes</DocsH2>
      <DocsP>
        When BharatCode asks and you approve, you don&apos;t have to re-answer the
        same question every time. Each approval can be remembered for a chosen
        scope, so a one-time &ldquo;yes&rdquo; can become a standing rule:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>once</InlineCode> — apply to just this call. The next call
          asks again.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>session</InlineCode> — remember for the rest of the current
          session. A new session starts fresh.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>project</InlineCode> — remember for this project. The
          decision sticks the next time you open BharatCode in the same
          repository.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>forever</InlineCode> — remember globally, across every
          project on this machine.
        </DocsListItem>
      </DocsList>
      <DocsCallout tone="note" title="Ask and scope are two separate choices">
        The verb (<InlineCode>ask</InlineCode> / <InlineCode>allow</InlineCode> /{' '}
        <InlineCode>deny</InlineCode>) decides <em>what happens</em>; the scope (
        <InlineCode>once</InlineCode> / <InlineCode>session</InlineCode> /{' '}
        <InlineCode>project</InlineCode> / <InlineCode>forever</InlineCode>)
        decides <em>how long the answer is remembered</em>. You only pick a scope
        when BharatCode asks — granting <InlineCode>project</InlineCode> or{' '}
        <InlineCode>forever</InlineCode> is the same as writing an{' '}
        <InlineCode>allow</InlineCode> (or <InlineCode>deny</InlineCode>) rule into
        your config by hand.
      </DocsCallout>

      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="approval-modes">Approval modes</DocsH2>
      <DocsP>
        The approval <em>mode</em> sets the overall posture — how eager BharatCode
        is to ask before acting. Switch modes mid-session with the{' '}
        <InlineCode>/permissions</InlineCode> slash command, or set a default in
        config. There are three:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>read-only</InlineCode> — only the read-only tools auto-run.
          Anything that would change a file or run a command is held back. Use
          this to let the agent explore and explain a codebase without touching
          it.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>auto</InlineCode> — the default. Read tools run freely; the
          system-changing tools ask, and your answers are remembered per the
          scope you choose. This is the everyday balance of speed and control.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>full</InlineCode> — allow everything, no prompts. This is
          the same posture as the <InlineCode>--yolo</InlineCode> flag: the agent
          edits, writes, and runs shell commands without stopping to ask.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Set the mode interactively from inside the TUI:
      </DocsP>
      <CodeBlock
        language="text"
        label="in the TUI"
        code={'/permissions read-only\n/permissions auto\n/permissions full'}
      />
      <DocsP>
        Or pick a default in config so every session starts in the posture you
        want. <InlineCode>mode</InlineCode> sits inside the{' '}
        <InlineCode>permissions</InlineCode> block:
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="~/.config/bharatcode/config.json"
        code={PERMISSIONS_CONFIG}
      />
      <DocsCallout tone="warn" title="full / --yolo skips every prompt">
        In <InlineCode>full</InlineCode> mode (or with{' '}
        <InlineCode>--yolo</InlineCode> / the <InlineCode>/yolo</InlineCode>{' '}
        command) the gate is bypassed entirely — file edits and shell commands
        run unattended. It is the right mode for a throwaway sandbox or a tightly
        scoped automation, but reach for it deliberately, not by habit.
      </DocsCallout>

      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="config-patterns">Deny-list and auto-approve patterns</DocsH2>
      <DocsP>
        Beyond the mode, you can pin individual tools to a verb in the{' '}
        <InlineCode>permissions</InlineCode> block. Each key is a tool name and
        each value is <InlineCode>ask</InlineCode>, <InlineCode>allow</InlineCode>,
        or <InlineCode>deny</InlineCode>. An <InlineCode>allow</InlineCode> entry
        is an <strong className="font-semibold text-fg">auto-approve</strong> rule
        for that tool; a <InlineCode>deny</InlineCode> entry is a{' '}
        <strong className="font-semibold text-fg">deny-list</strong> entry that
        refuses it. Patterns match in two forms:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <strong className="font-semibold text-fg">Exact match</strong> — a bare
          tool name like <InlineCode>edit</InlineCode> or{' '}
          <InlineCode>bash</InlineCode> matches calls to exactly that tool.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Wildcard</strong> —{' '}
          <InlineCode>&lt;tool&gt;:*</InlineCode> (for example{' '}
          <InlineCode>bash:*</InlineCode>) matches all calls to that tool.
        </DocsListItem>
      </DocsList>
      <DocsP>
        A common &ldquo;trusted repo, no network&rdquo; setup auto-approves edits
        while denying the shell and web tools entirely:
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="./.bharatcode.json"
        code={WILDCARD_CONFIG}
      />
      <DocsP>
        Because the project file is merged on top of the global one, a{' '}
        <InlineCode>deny</InlineCode> in <InlineCode>./.bharatcode.json</InlineCode>{' '}
        is a clean way to make a single repository stricter than your machine-wide
        default — see{' '}
        <DocsLinkInline href="/docs/configuration">Configuration</DocsLinkInline>{' '}
        for the global-then-project merge order.
      </DocsP>

      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="cost-ledger">The INR cost ledger</DocsH2>
      <DocsP>
        Every call to a model has a price, and BharatCode tracks it in rupees as
        you go. After each call the ledger records the cost of that turn in INR,
        accumulates it into your running spend, and surfaces the total so cost is
        never a surprise at the end of the month. This is what &ldquo;data — and
        accounting — stays in India&rdquo; looks like in practice: a transparent,
        rupee-denominated tally rather than an opaque dollar bill later.
      </DocsP>

      <DocsH3 id="budget-gate">The monthly budget gate</DocsH3>
      <DocsP>
        Set a monthly ceiling and BharatCode enforces it. The{' '}
        <InlineCode>budget</InlineCode> block in config holds a single number, your
        limit in rupees:
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="~/.config/bharatcode/config.json"
        code={BUDGET_CONFIG}
      />
      <DocsP>
        As spend climbs toward the limit the gate watches the running total; once
        the month&apos;s spend reaches <InlineCode>monthly_limit_inr</InlineCode>,
        the budget gate stops further calls so you can&apos;t blow past it
        unintentionally. The counter resets with the calendar month.
      </DocsP>
      <DocsP>
        Inspect spend and the remaining allowance from the command line at any
        time:
      </DocsP>
      <CodeBlock
        language="bash"
        label="check spend and budget"
        prompt
        code={'bharatcode stats\nbharatcode budget'}
      />
      <DocsP>
        <InlineCode>bharatcode stats</InlineCode> reports your usage and accrued
        cost, and <InlineCode>bharatcode budget</InlineCode> shows where you stand
        against the monthly limit. Inside the TUI, the{' '}
        <InlineCode>/budget</InlineCode> slash command shows the same figures
        without leaving your session.
      </DocsP>

      <DocsH3 id="tui-footer">Spend in the TUI footer</DocsH3>
      <DocsP>
        You don&apos;t have to run a command to see where you are. The TUI footer
        shows your spend as you work, so the rupee total is always in view while
        the agent runs — roughly:
      </DocsP>
      <CodeBlock
        language="text"
        label="TUI footer (illustrative)"
        code={'deepseek · auto · ₹12.40 spent · ₹2000 budget'}
      />
      <DocsCallout tone="tip" title="Numbers above are illustrative">
        The exact footer layout and figures depend on your provider, model, and
        usage. The only configured value here is{' '}
        <InlineCode>monthly_limit_inr</InlineCode> (shown as{' '}
        <InlineCode>2000</InlineCode>); the spend figure is whatever your session
        has actually cost so far.
      </DocsCallout>

      {/* ------------------------------------------------------------------ */}
      <DocsH2 id="putting-together">Putting it together</DocsH2>
      <DocsP>
        Permissions and cost are the two guardrails that let you hand work to an
        autonomous agent without losing the wheel. A typical setup keeps{' '}
        <InlineCode>auto</InlineCode> mode with the system-changing tools on{' '}
        <InlineCode>ask</InlineCode>, auto-approves the edits you trust per project
        with an <InlineCode>allow</InlineCode> rule, and pins a monthly INR budget
        so an over-eager session can never run up an unbounded bill.
      </DocsP>
      <DocsList>
        <DocsListItem>
          Tighten a single repo with a <InlineCode>deny</InlineCode> rule or{' '}
          <InlineCode>read-only</InlineCode> mode in{' '}
          <InlineCode>./.bharatcode.json</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          Switch posture on the fly with <InlineCode>/permissions</InlineCode>,
          and drop into <InlineCode>--yolo</InlineCode> only for a sandbox.
        </DocsListItem>
        <DocsListItem>
          Watch the footer, check <InlineCode>bharatcode budget</InlineCode>, and
          let the gate stop you at the limit.
        </DocsListItem>
      </DocsList>
      <DocsCallout tone="tip" title="Related">
        See{' '}
        <DocsLinkInline href="/docs/configuration">Configuration</DocsLinkInline>{' '}
        for how the <InlineCode>permissions</InlineCode> and{' '}
        <InlineCode>budget</InlineCode> blocks fit into the wider config, and{' '}
        <DocsLinkInline href="/docs/commands">
          TUI &amp; Slash Commands
        </DocsLinkInline>{' '}
        for <InlineCode>/permissions</InlineCode>, <InlineCode>/budget</InlineCode>,
        and <InlineCode>/yolo</InlineCode> in context.
      </DocsCallout>
    </DocsPage>
  );
}
