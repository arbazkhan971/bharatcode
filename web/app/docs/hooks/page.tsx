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
  title: 'Hooks',
  description:
    'Shell-backed lifecycle hooks in BharatCode: run your own commands on PreToolUse, PostToolUse, OnError, and OnSession events, matched by glob or regex against the tool. Hooks receive a JSON payload on stdin and BHARATCODE_* env vars, and a PreToolUse hook can veto a tool call.',
};

const EVENT_FLOW = `OnSession      session starts
   │
   ├─ PreToolUse    before a tool runs   ← can block the call
   │      │
   │   (tool executes)
   │      │
   ├─ PostToolUse   after a tool succeeds
   │
   └─ OnError       a tool returns an error`;

const STDIN_PAYLOAD = `{
  "event": "PreToolUse",
  "tool": "bash",
  "args": {
    "command": "rm -rf build/"
  },
  "session_id": "01HZX9Q...K3"
}`;

const BLOCK_CONFIG = `{
  "hooks": [
    {
      "event": "PreToolUse",
      "match": "bash",
      "command": "~/.bharatcode/hooks/guard-bash.sh"
    }
  ]
}`;

const GUARD_BASH = `#!/usr/bin/env bash
# guard-bash.sh — veto destructive recursive deletes.
# stdin: the PreToolUse JSON payload. stdout: a JSON block decision.

payload="$(cat)"
command="$(printf '%s' "$payload" | jq -r '.args.command')"

if printf '%s' "$command" | grep -Eq 'rm[[:space:]]+(-[a-zA-Z]*r[a-zA-Z]*[[:space:]]+)?-?[a-zA-Z]*f'; then
  echo '{"block": true, "reason": "Refusing to run a recursive force delete."}'
  exit 0
fi

# Nothing printed (or an empty object) means: allow the call.
echo '{}'`;

const GOFMT_CONFIG = `{
  "hooks": [
    {
      "event": "PostToolUse",
      "match": "edit|multiedit|write",
      "command": "~/.bharatcode/hooks/gofmt-on-write.sh"
    }
  ]
}`;

const GOFMT_HOOK = `#!/usr/bin/env bash
# gofmt-on-write.sh — format Go files right after the agent writes them.

payload="$(cat)"
path="$(printf '%s' "$payload" | jq -r '.args.path')"

case "$path" in
  *.go) gofmt -w "$path" ;;
esac`;

const SESSION_HOOK_CONFIG = `{
  "hooks": [
    {
      "event": "OnSession",
      "match": "*",
      "command": "git status --short >> ~/.bharatcode/session-start.log"
    }
  ]
}`;

export default function HooksPage() {
  return (
    <DocsPage
      eyebrow="Integrations"
      title="Hooks"
      lede={
        <>
          Lifecycle hooks let you run your own shell commands at key moments in a
          session — before and after every tool call, when a tool errors, and
          when a session starts. A <InlineCode>PreToolUse</InlineCode> hook can
          even <strong className="font-semibold text-fg">veto</strong> a tool
          call before it runs, so you can enforce policy that lives entirely in
          your own scripts.
        </>
      }
      prev={{ href: '/docs/lsp', label: 'LSP' }}
      next={{ href: '/docs/cli', label: 'CLI Reference' }}
    >
      <DocsP>
        Hooks are the extension point for wiring BharatCode into the tooling you
        already run by hand — formatters, linters, audit logs, guardrails. They
        are <strong className="font-semibold text-fg">shell-backed</strong>: each
        hook is just a command (or a script you point at) that BharatCode invokes
        at the right moment, hands a JSON description of what is happening, and —
        for the pre-tool case — listens for a decision. No plugin API, no
        compiled extension. If you can write a shell script, you can write a hook.
      </DocsP>

      <DocsH2 id="events">The four lifecycle events</DocsH2>
      <DocsP>
        A hook subscribes to one of four events. Together they cover the full arc
        of a tool call, plus the start of a session:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>PreToolUse</InlineCode> — fires{' '}
          <em>before</em> a tool runs. This is the only event that can{' '}
          <strong className="font-semibold text-fg">block</strong> the call; use
          it for guardrails and policy checks.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>PostToolUse</InlineCode> — fires{' '}
          <em>after</em> a tool completes successfully. Use it for follow-up work
          like formatting a file the agent just wrote.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>OnError</InlineCode> — fires when a tool returns an error.
          Use it to log failures or send a notification.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>OnSession</InlineCode> — fires when a session starts. Use it
          to record context or prime your environment.
        </DocsListItem>
      </DocsList>
      <CodeBlock label="lifecycle order" code={EVENT_FLOW} />

      <DocsH2 id="matching">Matching a hook to a tool</DocsH2>
      <DocsP>
        Each hook carries a <InlineCode>match</InlineCode> pattern that is tested
        against the name of the tool involved in the event — for example{' '}
        <InlineCode>bash</InlineCode>, <InlineCode>edit</InlineCode>,{' '}
        <InlineCode>multiedit</InlineCode>, <InlineCode>write</InlineCode>,{' '}
        <InlineCode>web_fetch</InlineCode>, and the rest of the{' '}
        <DocsLinkInline href="/docs/tools">built-in tools</DocsLinkInline>. The
        pattern can be a <strong className="font-semibold text-fg">glob</strong>{' '}
        (<InlineCode>*</InlineCode> matches every tool) or a{' '}
        <strong className="font-semibold text-fg">regex</strong> (
        <InlineCode>edit|multiedit|write</InlineCode> matches all three of the
        file-writing tools). A hook only fires when its event occurs{' '}
        <em>and</em> the tool name matches its pattern.
      </DocsP>

      <DocsH2 id="payload">What a hook receives</DocsH2>
      <DocsP>
        When a hook fires, BharatCode runs its command and writes a JSON payload
        to the command&apos;s <InlineCode>stdin</InlineCode>. The payload
        describes the event in full:
      </DocsP>
      <CodeBlock language="json" label="stdin payload" code={STDIN_PAYLOAD} />
      <DocsList>
        <DocsListItem>
          <InlineCode>event</InlineCode> — which lifecycle event fired (
          <InlineCode>PreToolUse</InlineCode>,{' '}
          <InlineCode>PostToolUse</InlineCode>, <InlineCode>OnError</InlineCode>,
          or <InlineCode>OnSession</InlineCode>).
        </DocsListItem>
        <DocsListItem>
          <InlineCode>tool</InlineCode> — the name of the tool the event is about.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>args</InlineCode> — the arguments the tool was called with
          (for <InlineCode>bash</InlineCode> that is the{' '}
          <InlineCode>command</InlineCode>; for the file tools, the{' '}
          <InlineCode>path</InlineCode> and edit details).
        </DocsListItem>
        <DocsListItem>
          <InlineCode>session_id</InlineCode> — the id of the current session, so
          you can correlate hook runs with a single conversation.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Alongside the JSON on stdin, BharatCode exports a set of{' '}
        <InlineCode>BHARATCODE_*</InlineCode> environment variables into the
        hook&apos;s process, mirroring the same fields. Read whichever is more
        convenient — pipe the stdin JSON through a parser like{' '}
        <InlineCode>jq</InlineCode> for structured access, or reach for an
        environment variable when you just need one value in a one-liner.
      </DocsP>

      <DocsH2 id="blocking">Blocking a tool call</DocsH2>
      <DocsP>
        A <InlineCode>PreToolUse</InlineCode> hook can stop a tool from running.
        Because the hook is handed the call&apos;s payload <em>before</em> the
        tool executes, it can inspect exactly what the agent is about to do and
        return a <strong className="font-semibold text-fg">block decision</strong>
        . The decision is JSON written to the hook&apos;s{' '}
        <InlineCode>stdout</InlineCode>: set <InlineCode>block</InlineCode> to{' '}
        <InlineCode>true</InlineCode> to veto the call, and add an optional{' '}
        <InlineCode>reason</InlineCode> string that is surfaced back to the agent
        so it understands why.
      </DocsP>
      <CodeBlock
        language="json"
        label="block decision (stdout)"
        code={'{"block": true, "reason": "Refusing to run a recursive force delete."}'}
      />
      <DocsCallout tone="note" title="Allowing is the default">
        Only <InlineCode>PreToolUse</InlineCode> hooks are consulted for a
        decision. If the hook prints nothing, an empty object, or anything without{' '}
        <InlineCode>block: true</InlineCode>, the call proceeds. The other three
        events run for their side effects and cannot stop a tool.
      </DocsCallout>

      <DocsH2 id="config">Configuring hooks</DocsH2>
      <DocsP>
        Hooks live in your{' '}
        <DocsLinkInline href="/docs/configuration">configuration</DocsLinkInline>{' '}
        as a <InlineCode>hooks</InlineCode> array. Each entry names the{' '}
        <InlineCode>event</InlineCode> to subscribe to, the{' '}
        <InlineCode>match</InlineCode> pattern for the tool, and the{' '}
        <InlineCode>command</InlineCode> to run. Put hooks in your global config (
        <InlineCode>~/.config/bharatcode/config.json</InlineCode>) to apply them
        everywhere, or in a project&apos;s{' '}
        <InlineCode>./.bharatcode.json</InlineCode> to scope them to one
        repository.
      </DocsP>

      <DocsH2 id="example-block">Example: block a recursive delete</DocsH2>
      <DocsP>
        A common guardrail is to make sure the agent never runs a recursive force
        delete. Register a <InlineCode>PreToolUse</InlineCode> hook on the{' '}
        <InlineCode>bash</InlineCode> tool that inspects the command and vetoes it
        when it looks like an <InlineCode>rm -rf</InlineCode>:
      </DocsP>
      <CodeBlock
        language="json"
        label="~/.config/bharatcode/config.json"
        code={BLOCK_CONFIG}
      />
      <DocsP>
        The script reads the JSON payload from stdin, pulls out{' '}
        <InlineCode>args.command</InlineCode> with <InlineCode>jq</InlineCode>, and
        prints a block decision when the command matches:
      </DocsP>
      <CodeBlock
        language="bash"
        label="~/.bharatcode/hooks/guard-bash.sh"
        code={GUARD_BASH}
      />
      <DocsP>
        Now whenever the agent tries to shell out to a recursive force delete,
        the call is vetoed and the agent is told why — without you having to be at
        the keyboard to deny it.
      </DocsP>
      <DocsCallout tone="tip" title="Hooks complement permissions">
        Hooks sit on top of, not instead of, the{' '}
        <DocsLinkInline href="/docs/permissions">permission system</DocsLinkInline>
        . Approval modes decide <em>whether you are asked</em>; a{' '}
        <InlineCode>PreToolUse</InlineCode> hook lets you encode policy that
        always applies — even in <InlineCode>auto</InlineCode> or{' '}
        <InlineCode>full</InlineCode> mode — in your own code.
      </DocsCallout>

      <DocsH2 id="example-gofmt">Example: run gofmt after every edit</DocsH2>
      <DocsP>
        A <InlineCode>PostToolUse</InlineCode> hook is the natural place for
        follow-up work. Here is one that formats a Go file the moment the agent
        writes to it, so the working tree never drifts from{' '}
        <InlineCode>gofmt</InlineCode>:
      </DocsP>
      <CodeBlock
        language="json"
        label="./.bharatcode.json"
        code={GOFMT_CONFIG}
      />
      <DocsP>
        The <InlineCode>match</InlineCode> regex catches all three file-writing
        tools — <InlineCode>edit</InlineCode>,{' '}
        <InlineCode>multiedit</InlineCode>, and <InlineCode>write</InlineCode> — so
        the formatter runs no matter how the change was made. The script reads the
        file path from the payload and only acts on{' '}
        <InlineCode>.go</InlineCode> files:
      </DocsP>
      <CodeBlock
        language="bash"
        label="~/.bharatcode/hooks/gofmt-on-write.sh"
        code={GOFMT_HOOK}
      />

      <DocsH3 id="example-session">Example: a session-start hook</DocsH3>
      <DocsP>
        <InlineCode>OnSession</InlineCode> fires once when a session begins, which
        makes it handy for recording context. Because no specific tool is
        involved, match on <InlineCode>*</InlineCode> and run any command you
        like:
      </DocsP>
      <CodeBlock
        language="json"
        label="config.json"
        code={SESSION_HOOK_CONFIG}
      />

      <DocsH2 id="writing-hooks">Tips for writing hooks</DocsH2>
      <DocsList>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Parse stdin, don&apos;t guess.
          </strong>{' '}
          Read the JSON payload (with <InlineCode>jq</InlineCode> or your
          language of choice) rather than re-deriving values — it is the source of
          truth for the <InlineCode>event</InlineCode>,{' '}
          <InlineCode>tool</InlineCode>, and <InlineCode>args</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Keep them fast.</strong>{' '}
          Pre-tool hooks run on the critical path of every matching call, so a
          slow script slows the agent. Do the minimum needed to make a decision.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Fail open, on purpose.
          </strong>{' '}
          A <InlineCode>PreToolUse</InlineCode> hook only blocks when it
          explicitly prints <InlineCode>block: true</InlineCode>. Make sure your
          guardrails print that decision on the paths you care about — and let
          everything else fall through to allow.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Make them portable.</strong>{' '}
          Point <InlineCode>command</InlineCode> at a checked-in script so the
          same hook works for everyone on the team when it ships in a project&apos;s{' '}
          <InlineCode>./.bharatcode.json</InlineCode>.
        </DocsListItem>
      </DocsList>

      <DocsP>
        Between guardrails on <InlineCode>PreToolUse</InlineCode>, cleanup on{' '}
        <InlineCode>PostToolUse</InlineCode>, and logging on{' '}
        <InlineCode>OnError</InlineCode> and <InlineCode>OnSession</InlineCode>,
        hooks give you a precise, scriptable seam to make BharatCode behave the
        way your codebase needs — entirely in your own shell.
      </DocsP>
    </DocsPage>
  );
}
