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
  title: 'Project Instructions (AGENTS.md)',
  description:
    'How BharatCode discovers AGENTS.md and CLAUDE.md files, concatenates them root-first so nested files win, byte-caps the result, and injects it into the system prompt.',
};

const EXAMPLE_AGENTS_MD = `# Project conventions

This is a Go monorepo. Follow these house rules.

## Build & test
- Build with: go build ./...
- Run the full test suite with: go test ./...
- Never commit if go vet ./... reports anything.

## Style
- Use the standard library before reaching for a dependency.
- Keep functions small; return errors, don't panic.
- Run gofmt on every file you touch.

## Boundaries
- Do not edit anything under vendor/ or generated/.
- Keep all secrets in environment variables, never in code.`;

const NESTED_AGENTS_MD = `# packages/api conventions

This service is the only place we use a database.

## Style
- Use sqlc for queries; do NOT hand-write SQL strings.
- Every handler returns a typed error, mapped to an HTTP status.

## Testing
- Integration tests need Postgres; run them with: make test-api`;

const PROJECT_TREE = `myrepo/
├── AGENTS.md            # repo-root: applies everywhere
├── go.mod
├── cmd/
│   └── server/
└── packages/
    ├── web/
    └── api/
        ├── AGENTS.md    # applies only inside packages/api/
        └── handler.go`;

const PROMPT_LAYERS = `[ global instructions          ]   loaded first  (broadest)
[ myrepo/AGENTS.md             ]   repo root
[ myrepo/packages/api/AGENTS.md]   loaded last   (most specific — wins)`;

export default function AgentsMdPage() {
  return (
    <DocsPage
      eyebrow="Configuration"
      title="Project Instructions (AGENTS.md)"
      lede={
        <>
          Drop an <InlineCode>AGENTS.md</InlineCode> (or{' '}
          <InlineCode>CLAUDE.md</InlineCode>) into your repository and BharatCode
          will read it and follow your house conventions automatically — build
          commands, code style, files that are off-limits — without you
          re-stating them on every prompt.
        </>
      }
      prev={{ href: '/docs/profiles', label: 'Profiles' }}
      next={{ href: '/docs/commands', label: 'TUI & Slash Commands' }}
    >
      <DocsP>
        Project instruction files are how you teach the agent the rules of{' '}
        <em>your</em> codebase. When BharatCode starts a session it looks for{' '}
        <InlineCode>AGENTS.md</InlineCode> and <InlineCode>CLAUDE.md</InlineCode>{' '}
        files, reads them, and injects their contents into the system prompt.
        From that point on the agent treats them as standing orders: it builds
        with the command you specified, matches your style, and stays out of the
        directories you marked off-limits.
      </DocsP>

      <DocsP>
        Both filenames are recognized, so a file written for another
        compatible agent works here unchanged — there is nothing
        BharatCode-specific you have to add.
      </DocsP>

      <DocsH2 id="discovery">How files are discovered</DocsH2>
      <DocsP>
        BharatCode collects instruction files along a chain that runs from the
        broadest scope down to the narrowest:
      </DocsP>
      <DocsList ordered>
        <DocsListItem>
          <strong className="font-semibold text-fg">Global</strong> — your
          personal, machine-wide instructions that apply to every project.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">Repository root</strong> —
          the <InlineCode>AGENTS.md</InlineCode> /{' '}
          <InlineCode>CLAUDE.md</InlineCode> at the top of the project.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Nested directories
          </strong>{' '}
          — any instruction files in subdirectories on the path down to your
          current working directory.
        </DocsListItem>
      </DocsList>
      <DocsP>
        The chain is walked root-first: global, then the repository root, then
        each nested directory in turn, ending at your{' '}
        <InlineCode>cwd</InlineCode>. Every file found along that path is
        included — they accumulate rather than replace one another.
      </DocsP>

      <DocsH2 id="how-they-combine">How the files combine</DocsH2>
      <DocsP>
        The collected files are concatenated <strong className="font-semibold text-fg">root-first</strong>:
        the broadest instructions are placed at the top of the system prompt and
        the most-local file last. Because the deepest, most-specific file comes
        last, it overrides anything more general before it. In short:{' '}
        <strong className="font-semibold text-fg">
          on a conflict, the most-local instruction wins.
        </strong>
      </DocsP>
      <CodeBlock
        label="precedence (top = broadest, bottom = wins)"
        code={PROMPT_LAYERS}
      />
      <DocsCallout tone="note" title="Override, not deep merge">
        This is plain concatenation with recency precedence, not a structured
        key-by-key merge. A more-local file does not surgically replace one
        field of a parent — it simply appears later in the prompt, so when two
        instructions genuinely conflict, the agent follows the one nearest your
        working directory.
      </DocsCallout>

      <DocsH3 id="byte-cap">The byte cap</DocsH3>
      <DocsP>
        The combined instruction text is byte-capped before it goes into the
        system prompt, so an enormous or runaway file can&apos;t crowd out your
        actual prompt and the model&apos;s working context. Keep these files
        focused on durable conventions — the commands, boundaries, and style
        rules the agent needs every time — rather than long prose. If you have a
        lot to say, prefer a short root file plus tighter nested files over one
        giant document.
      </DocsP>

      <DocsH2 id="example">An example AGENTS.md</DocsH2>
      <DocsP>
        A good instruction file is short and concrete. State the exact build and
        test commands, the style rules that matter, and anything the agent must
        never touch. Here is a repository-root{' '}
        <InlineCode>AGENTS.md</InlineCode> for a Go project:
      </DocsP>
      <CodeBlock language="markdown" label="AGENTS.md" code={EXAMPLE_AGENTS_MD} />
      <DocsP>
        With this in place you no longer have to remind the agent how to run the
        tests or that <InlineCode>vendor/</InlineCode> is off-limits — it reads
        the rules at the start of every session and applies them on its own.
      </DocsP>

      <DocsH2 id="nesting">Nesting and override in practice</DocsH2>
      <DocsP>
        Nesting lets each part of a monorepo carry its own rules while still
        inheriting the repository-wide ones. Consider this layout, where the API
        package adds conventions of its own:
      </DocsP>
      <CodeBlock label="project layout" code={PROJECT_TREE} />
      <DocsP>
        The package-level file only needs to describe what is{' '}
        <em>different</em> about that corner of the codebase:
      </DocsP>
      <CodeBlock
        language="markdown"
        label="packages/api/AGENTS.md"
        code={NESTED_AGENTS_MD}
      />
      <DocsP>
        Now the scope of each file follows where you are working:
      </DocsP>
      <DocsList>
        <DocsListItem>
          Working in <InlineCode>cmd/server/</InlineCode>, the agent sees the
          global instructions plus the repo-root{' '}
          <InlineCode>AGENTS.md</InlineCode>. The API-specific rules are not on
          the path to your <InlineCode>cwd</InlineCode>, so they are not loaded.
        </DocsListItem>
        <DocsListItem>
          Working in <InlineCode>packages/api/</InlineCode>, the agent sees
          both files: the repo-root <InlineCode>AGENTS.md</InlineCode> first,
          then <InlineCode>packages/api/AGENTS.md</InlineCode> last. It inherits
          the repo-wide rules (build with{' '}
          <InlineCode>go build ./...</InlineCode>, never edit{' '}
          <InlineCode>vendor/</InlineCode>) and layers the API rules on top (use{' '}
          <InlineCode>sqlc</InlineCode>, run integration tests with{' '}
          <InlineCode>make test-api</InlineCode>).
        </DocsListItem>
        <DocsListItem>
          Where the two genuinely disagree, the nested file wins because it is
          read last. If the root file said &quot;keep handlers minimal&quot; and
          the API file added &quot;every handler returns a typed error,&quot;
          the agent honors the more specific, local instruction inside{' '}
          <InlineCode>packages/api/</InlineCode>.
        </DocsListItem>
      </DocsList>

      <DocsCallout tone="tip" title="Keep them lean">
        Treat instruction files as the agent&apos;s onboarding doc, not its
        knowledge base. A tight root file with the build/test commands and hard
        boundaries, plus small per-package files for local quirks, gives the
        best results and stays comfortably under the byte cap.
      </DocsCallout>

      <DocsP>
        Project instructions pair naturally with the rest of your{' '}
        <DocsLinkInline href="/docs/configuration">configuration</DocsLinkInline>
        : config files set <em>how</em> BharatCode runs, while{' '}
        <InlineCode>AGENTS.md</InlineCode> tells the agent <em>how to behave</em>{' '}
        inside your code.
      </DocsP>
    </DocsPage>
  );
}
