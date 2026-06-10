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
  title: 'Built-in Tools',
  description:
    'The 13 built-in tools BharatCode can call — file ops (view, edit, multiedit, write), search (ls, glob, grep), shell (bash with background jobs), web (web_fetch, web_search), LSP diagnostics, and a todo list. Edits and shell runs are permission-gated.',
};

/**
 * Built-in tools reference. Each tool is one row: the call name the model
 * emits, the category it belongs to, a one-line description, and whether the
 * call passes through the permission gate before it runs. Grouped by category
 * (file ops / search / shell / web / context) so readers can scan to the kind
 * of capability they care about.
 */
type ToolRow = {
  /** The tool name the agent calls. */
  name: string;
  /** Human-readable category label. */
  category: string;
  /** One-line summary of what the tool does. */
  summary: string;
  /** When the agent reaches for it. */
  usedWhen: string;
  /** Runs through the ask/allow/deny permission gate before executing. */
  gated: boolean;
};

const TOOLS: ToolRow[] = [
  {
    name: 'view',
    category: 'File ops',
    summary: 'Read a file (optionally a line range) into context.',
    usedWhen: 'before editing, to ground changes in the real file contents.',
    gated: false,
  },
  {
    name: 'edit',
    category: 'File ops',
    summary: 'Replace an exact string in a file with new text.',
    usedWhen: 'making a single, surgical change to a file.',
    gated: true,
  },
  {
    name: 'multiedit',
    category: 'File ops',
    summary: 'Apply several string replacements to one file in one call.',
    usedWhen: 'several related edits land in the same file at once.',
    gated: true,
  },
  {
    name: 'write',
    category: 'File ops',
    summary: 'Create a new file or overwrite an existing one.',
    usedWhen: 'scaffolding a new file or fully rewriting one.',
    gated: true,
  },
  {
    name: 'ls',
    category: 'Search',
    summary: 'List the entries in a directory.',
    usedWhen: 'orienting in an unfamiliar tree or confirming a path exists.',
    gated: false,
  },
  {
    name: 'glob',
    category: 'Search',
    summary: 'Find files by glob pattern (e.g. **/*.go).',
    usedWhen: 'locating files by name or extension across the project.',
    gated: false,
  },
  {
    name: 'grep',
    category: 'Search',
    summary: 'Search file contents with a regular expression.',
    usedWhen: 'finding where a symbol, string, or pattern is used.',
    gated: false,
  },
  {
    name: 'bash',
    category: 'Shell',
    summary: 'Run a shell command; can launch background jobs.',
    usedWhen: 'building, testing, running, or any task the file tools cannot do.',
    gated: true,
  },
  {
    name: 'web_fetch',
    category: 'Web',
    summary: 'Fetch a URL and read its content.',
    usedWhen: 'a specific page (docs, an issue, an API spec) is needed.',
    gated: false,
  },
  {
    name: 'web_search',
    category: 'Web',
    summary: 'Search the web for current information.',
    usedWhen: 'recent or external facts are needed before acting.',
    gated: false,
  },
  {
    name: 'diagnostics',
    category: 'Context',
    summary: 'Read LSP diagnostics (errors, warnings) for the workspace.',
    usedWhen: 'checking whether an edit introduced or cleared problems.',
    gated: false,
  },
  {
    name: 'todo',
    category: 'Context',
    summary: 'Maintain a structured task list for the current work.',
    usedWhen: 'planning and tracking progress on a multi-step task.',
    gated: false,
  },
];

export default function ToolsPage() {
  return (
    <DocsPage
      eyebrow="Usage"
      title="Built-in Tools"
      lede={
        <>
          BharatCode ships with 13 built-in tools the agent calls to do real
          work — reading and editing files, searching the codebase, running
          shell commands, reaching the web, and reading LSP diagnostics. The
          tools that change your machine — <InlineCode>write</InlineCode>,{' '}
          <InlineCode>edit</InlineCode>, <InlineCode>multiedit</InlineCode>, and{' '}
          <InlineCode>bash</InlineCode> — pass through the permission gate before
          they run.
        </>
      }
      prev={{ href: '/docs/commands', label: 'TUI & Slash Commands' }}
      next={{ href: '/docs/goal', label: '/goal Autonomous Mode' }}
    >
      <DocsH2 id="overview">Overview</DocsH2>
      <DocsP>
        A <em>tool</em> is a capability the model can invoke during a turn.
        Instead of only producing text, the agent emits a tool call —{' '}
        <InlineCode>view</InlineCode> a file, <InlineCode>grep</InlineCode> for a
        symbol, run <InlineCode>bash</InlineCode> — BharatCode executes it, and
        the result is fed back into the conversation. That loop is how the agent
        actually reads and changes your project rather than just describing what
        you should do.
      </DocsP>
      <DocsP>
        There are 13 built-in tools in five categories. Most are read-only and
        run without interruption. The ones that modify files or your system are{' '}
        <strong className="font-semibold text-fg">permission-gated</strong>: by
        default the agent asks before they execute. See the{' '}
        <DocsLinkInline href="/docs/permissions">Permissions</DocsLinkInline>{' '}
        page for how the ask / allow / deny gate and approval modes work.
      </DocsP>

      {/* Legend for the table tag. */}
      <DocsList>
        <DocsListItem>
          <span className="inline-flex items-center gap-1.5 align-middle">
            <Dot className="bg-saffron" />
            <span className="font-medium text-fg">Permission-gated</span>
          </span>{' '}
          — the call goes through the approval gate before it runs. The rest are
          read-only and execute directly.
        </DocsListItem>
      </DocsList>

      <ToolTable />

      <DocsCallout tone="note" title="Plus two background-job controls">
        Alongside the 13 tools above, two helpers control background processes
        started by <InlineCode>bash</InlineCode>:{' '}
        <InlineCode>job_output</InlineCode> reads new output from a running job,
        and <InlineCode>job_kill</InlineCode> stops one. They only ever act on
        jobs <InlineCode>bash</InlineCode> launched, so they inherit its
        permission posture.
      </DocsCallout>

      <DocsH2 id="file-ops">File ops — view, edit, multiedit, write</DocsH2>
      <DocsP>
        These four tools are how the agent reads and changes source files.{' '}
        <InlineCode>view</InlineCode> is read-only and unrestricted; the other
        three modify files and are{' '}
        <strong className="font-semibold text-fg">permission-gated</strong>.
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>view</InlineCode> reads a file into context, optionally a
          specific line range. The agent uses it before editing so changes are
          grounded in the file&apos;s actual contents rather than a guess.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>edit</InlineCode> replaces one exact string in a file with
          new text. It is the go-to for a single, surgical change — rename a
          symbol, fix a line, tweak a value.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>multiedit</InlineCode> applies several string replacements
          to the <em>same</em> file in one call. The agent reaches for it when a
          set of related edits all land in one file, so they apply atomically.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>write</InlineCode> creates a new file or overwrites an
          existing one wholesale. Used for scaffolding a new file, or when a
          rewrite is cleaner than a pile of edits.
        </DocsListItem>
      </DocsList>
      <DocsP>
        A typical edit flow is read, then change: <InlineCode>view</InlineCode>{' '}
        the file, then <InlineCode>edit</InlineCode> it. The edit surfaces as a
        diff you approve (unless you are in an auto-approving{' '}
        <DocsLinkInline href="/docs/permissions">mode</DocsLinkInline>):
      </DocsP>
      <CodeBlock
        language="text"
        label="TUI"
        code={`> rename the Timeout field to RequestTimeout in config.go

  • view  config.go
  • edit  config.go   (awaiting approval)

  - Timeout    time.Duration
  + RequestTimeout time.Duration`}
      />
      <DocsP>
        You can inspect the most recent changes at any time with the{' '}
        <InlineCode>/diff</InlineCode> slash command.
      </DocsP>

      <DocsH2 id="search">Search — ls, glob, grep</DocsH2>
      <DocsP>
        Before the agent can change code it has to find it. These three
        read-only tools are how it explores a repository:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>ls</InlineCode> lists the entries in a directory — used to
          orient in an unfamiliar tree or confirm a path exists.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>glob</InlineCode> finds files by pattern, such as{' '}
          <InlineCode>**/*.go</InlineCode> or{' '}
          <InlineCode>cmd/**/main.go</InlineCode> — used to locate files by name
          or extension across the project.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>grep</InlineCode> searches file <em>contents</em> with a
          regular expression — used to find every place a symbol, string, or
          pattern appears. It is the agent&apos;s primary way to trace how code
          is wired together.
        </DocsListItem>
      </DocsList>
      <DocsP>
        These three are read-only and never gated, so the agent can explore your
        codebase freely without prompting you for approval.
      </DocsP>

      <DocsH2 id="shell">Shell — bash (with background jobs)</DocsH2>
      <DocsP>
        <InlineCode>bash</InlineCode> runs a shell command and returns its
        output. It is the agent&apos;s catch-all for anything the file and
        search tools cannot do — building, running tests, installing
        dependencies, git operations, code generators, and more.
      </DocsP>
      <DocsCallout tone="warn" title="bash is permission-gated">
        Because a shell command can do anything your user can,{' '}
        <InlineCode>bash</InlineCode> always passes through the{' '}
        <DocsLinkInline href="/docs/permissions">permission gate</DocsLinkInline>
        . In the default read-only mode the agent asks before each command; you
        can grant approval for once, the session, the project, or forever, or
        switch to an auto-approving mode when you trust the work.
      </DocsCallout>
      <DocsH3 id="background-jobs">Background jobs</DocsH3>
      <DocsP>
        Long-running commands — a dev server, a file watcher, a test runner in
        watch mode — would block the turn if run in the foreground. Instead,{' '}
        <InlineCode>bash</InlineCode> can launch them as{' '}
        <strong className="font-semibold text-fg">background jobs</strong> that
        keep running while the agent continues. Two helper tools manage them:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>job_output</InlineCode> reads the new output a background
          job has produced since it was last checked — used to watch a server&apos;s
          logs or wait for a build to finish.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>job_kill</InlineCode> stops a running background job —
          used to shut a server down once it is no longer needed.
        </DocsListItem>
      </DocsList>
      <DocsP>
        For example, the agent might start a dev server in the background, poll
        its output until it is ready, run a check against it, then kill it:
      </DocsP>
      <CodeBlock
        language="text"
        label="TUI"
        code={`> start the dev server, hit /health once it's up, then stop it

  • bash        npm run dev   (background job #1)
  • job_output  job #1        "listening on :3000"
  • bash        curl -s localhost:3000/health   → {"ok":true}
  • job_kill    job #1        stopped`}
      />

      <DocsH2 id="web">Web — web_fetch, web_search</DocsH2>
      <DocsP>
        Two tools let the agent reach beyond your machine when the answer is not
        in the codebase:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>web_fetch</InlineCode> retrieves a specific URL and reads
          its content — used when the agent already knows the page it needs, such
          as a library&apos;s docs, a GitHub issue, or an API specification.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>web_search</InlineCode> runs a web search and returns
          results — used to find current or external information before acting,
          when the exact source is not known up front.
        </DocsListItem>
      </DocsList>
      <DocsCallout tone="note" title="These reach the network">
        Web tools make outbound requests to the URLs and search engines they
        target. They do not send your source code anywhere on their own — but if
        you need a strict no-network posture, govern them through the{' '}
        <DocsLinkInline href="/docs/permissions">
          permission system
        </DocsLinkInline>{' '}
        like any other tool.
      </DocsCallout>

      <DocsH2 id="context">Context — diagnostics, todo</DocsH2>
      <DocsP>
        The last two tools keep the agent grounded in the real state of the
        project and its own plan:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>diagnostics</InlineCode> reads the errors and warnings
          your language server (LSP) reports for the workspace. The agent uses
          it to confirm an edit compiles cleanly — or to see exactly what broke —
          without having to run a full build. See{' '}
          <DocsLinkInline href="/docs/lsp">LSP integration</DocsLinkInline> for
          how diagnostics are wired in.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>todo</InlineCode> maintains a structured task list for the
          work in progress. On a multi-step task the agent writes out the steps,
          then checks them off as it goes, so both you and it can see what is
          done and what remains.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="permissions-and-tools">Tools and permissions</DocsH2>
      <DocsP>
        Tools are the surface the permission system governs. The read-only tools
        — <InlineCode>view</InlineCode>, <InlineCode>ls</InlineCode>,{' '}
        <InlineCode>glob</InlineCode>, <InlineCode>grep</InlineCode>,{' '}
        <InlineCode>web_fetch</InlineCode>, <InlineCode>web_search</InlineCode>,{' '}
        <InlineCode>diagnostics</InlineCode>, and <InlineCode>todo</InlineCode> —
        do not change your machine, so they run directly. The mutating tools —{' '}
        <InlineCode>write</InlineCode>, <InlineCode>edit</InlineCode>,{' '}
        <InlineCode>multiedit</InlineCode>, and <InlineCode>bash</InlineCode> —
        go through the gate.
      </DocsP>
      <DocsP>
        Which gated tools actually prompt you depends on the active approval
        mode: <InlineCode>read-only</InlineCode> asks for every mutating call,{' '}
        <InlineCode>auto</InlineCode> auto-approves a safe subset, and{' '}
        <InlineCode>full</InlineCode> approves everything. The{' '}
        <InlineCode>/permissions</InlineCode> slash command switches modes, and{' '}
        <InlineCode>--yolo</InlineCode> (or <InlineCode>/yolo</InlineCode> in the
        TUI) bypasses the gate entirely for a trusted run.
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode run --yolo "format the repo and run the test suite"'}
      />
      <DocsCallout tone="tip" title="Tighten further with hooks">
        Beyond approval modes, you can intercept any tool call with{' '}
        <DocsLinkInline href="/docs/hooks">lifecycle hooks</DocsLinkInline> —{' '}
        <InlineCode>PreToolUse</InlineCode> can inspect a call&apos;s JSON payload
        and block it by glob or regex match, giving you policy control on top of
        the permission gate.
      </DocsCallout>
    </DocsPage>
  );
}

/** Small colored dot used in the legend and the table tag. */
function Dot({ className = '' }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${className}`}
    />
  );
}

/** Pill tag marking a permission-gated tool. */
function GatedTag() {
  return (
    <span className="inline-flex items-center rounded-md border border-saffron/30 bg-saffron/10 px-1.5 py-0.5 font-mono text-[0.6875rem] font-medium uppercase tracking-wider text-saffron">
      Gated
    </span>
  );
}

/**
 * Built-in tools table. Scrolls horizontally on narrow screens so the columns
 * stay readable without wrapping awkwardly on mobile.
 */
function ToolTable() {
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-bg-elevated">
      <table className="w-full min-w-[44rem] border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-border text-xs uppercase tracking-wider text-faint">
            <th scope="col" className="px-4 py-3 font-medium">
              Tool
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              Category
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              What it does
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              Agent uses it when
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              Gate
            </th>
          </tr>
        </thead>
        <tbody>
          {TOOLS.map((t) => (
            <tr
              key={t.name}
              className="border-b border-border/60 transition-colors last:border-0 hover:bg-surface/40"
            >
              <th
                scope="row"
                className="whitespace-nowrap px-4 py-3 align-top"
              >
                <code className="font-mono text-[0.8125rem] font-medium text-fg">
                  {t.name}
                </code>
              </th>
              <td className="whitespace-nowrap px-4 py-3 align-top text-muted">
                {t.category}
              </td>
              <td className="px-4 py-3 align-top text-muted">{t.summary}</td>
              <td className="px-4 py-3 align-top text-muted">{t.usedWhen}</td>
              <td className="px-4 py-3 align-top">
                {t.gated ? (
                  <GatedTag />
                ) : (
                  <span className="text-faint">—</span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
