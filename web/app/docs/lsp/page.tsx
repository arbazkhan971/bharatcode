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
  title: 'LSP Integration',
  description:
    'BharatCode runs language servers over the Language Server Protocol to give the agent in-context diagnostics through the diagnostics tool. Configure a server per language in config; the client handles server-initiated requests, so real servers like gopls and rust-analyzer work.',
};

/** Per-language LSP config, shown alongside the rest of the merged config. */
const LSP_CONFIG = `{
  // Language servers BharatCode launches, keyed by language. Each entry is a
  // subprocess the client talks to over the Language Server Protocol (stdio).
  "lsp": {
    "go": {
      "command": "gopls"               // resolved on your PATH
    },
    "rust": {
      "command": "rust-analyzer"
    }
  }
}`;

/** A server that needs arguments — same shape, with an optional args array. */
const LSP_CONFIG_ARGS = `{
  "lsp": {
    "go": {
      "command": "gopls",
      "args": ["serve"]                // extra arguments passed to the server
    }
  }
}`;

export default function LspPage() {
  return (
    <DocsPage
      eyebrow="Integrations"
      title="LSP Integration"
      lede={
        <>
          BharatCode runs real language servers over the{' '}
          <strong className="font-semibold text-fg">
            Language Server Protocol
          </strong>{' '}
          (LSP) and surfaces what they report to the agent as in-context
          diagnostics — the same errors and warnings your editor would show.
          That is how the agent knows whether an edit actually compiles, instead
          of guessing.
        </>
      }
      prev={{ href: '/docs/mcp', label: 'MCP' }}
      next={{ href: '/docs/hooks', label: 'Hooks' }}
    >
      <DocsH2 id="what-it-is">What LSP gives the agent</DocsH2>
      <DocsP>
        The Language Server Protocol is the standard your editor uses to talk to
        language tooling — <InlineCode>gopls</InlineCode> for Go,{' '}
        <InlineCode>rust-analyzer</InlineCode> for Rust, and a server for most
        other languages. A language server analyzes your project and reports{' '}
        <em>diagnostics</em>: compile errors, type mismatches, unused imports,
        lint warnings, and so on.
      </DocsP>
      <DocsP>
        BharatCode starts the configured language server for your project and
        keeps it running in the background. As the agent edits files, the server
        re-analyzes them and BharatCode collects the results. The agent reads
        those results through the{' '}
        <DocsLinkInline href="/docs/tools">
          <InlineCode>diagnostics</InlineCode> tool
        </DocsLinkInline>{' '}
        — so after a change it can see exactly what broke (or that everything is
        clean) without running a full build.
      </DocsP>
      <DocsCallout tone="tip" title="Why this matters">
        Diagnostics close the loop. The agent makes an edit, calls{' '}
        <InlineCode>diagnostics</InlineCode>, sees a type error it just
        introduced, and fixes it — all in the same turn, before handing the work
        back to you. It is the difference between an edit that looks right and
        one that compiles.
      </DocsCallout>

      <DocsH2 id="configure">Configure a server per language</DocsH2>
      <DocsP>
        Language servers are configured under an <InlineCode>lsp</InlineCode> key
        in your config, keyed by language. Each entry names the{' '}
        <InlineCode>command</InlineCode> BharatCode runs to launch the server;
        BharatCode then speaks LSP to it over its standard input and output. The{' '}
        <InlineCode>lsp</InlineCode> block lives in the same{' '}
        <DocsLinkInline href="/docs/configuration">config files</DocsLinkInline>{' '}
        as everything else — your global{' '}
        <InlineCode>~/.config/bharatcode/config.json</InlineCode> or a
        per-project <InlineCode>.bharatcode.json</InlineCode>, merged the same
        way.
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="config.json"
        code={LSP_CONFIG}
        hideCopy={false}
      />
      <DocsP>
        The <InlineCode>command</InlineCode> is resolved on your{' '}
        <InlineCode>PATH</InlineCode>, so install the server the usual way and
        BharatCode will find it. The keys (<InlineCode>go</InlineCode>,{' '}
        <InlineCode>rust</InlineCode>) name the language the server handles; add
        an entry per language you want analyzed.
      </DocsP>

      <DocsH3 id="server-args">Passing arguments</DocsH3>
      <DocsP>
        If a server needs extra arguments to launch, add an{' '}
        <InlineCode>args</InlineCode> array alongside{' '}
        <InlineCode>command</InlineCode>. The arguments are passed straight
        through when the subprocess starts:
      </DocsP>
      <CodeBlock language="jsonc" label=".bharatcode.json" code={LSP_CONFIG_ARGS} />
      <DocsCallout tone="note" title="Install the server yourself">
        BharatCode does not bundle or install language servers — it launches the{' '}
        <InlineCode>command</InlineCode> you point it at. Make sure the server is
        installed and on your <InlineCode>PATH</InlineCode> first, e.g.{' '}
        <InlineCode>go install golang.org/x/tools/gopls@latest</InlineCode> for
        Go, or your toolchain&apos;s <InlineCode>rust-analyzer</InlineCode> for
        Rust.
      </DocsCallout>

      <DocsH2 id="diagnostics-tool">Reading diagnostics</DocsH2>
      <DocsP>
        Once a server is configured, diagnostics are available to the agent
        automatically — there is nothing extra to invoke. The agent calls the{' '}
        <InlineCode>diagnostics</InlineCode> tool whenever it needs to confirm
        the workspace is healthy, typically right after an edit:
      </DocsP>
      <CodeBlock
        language="text"
        label="TUI"
        code={`> add a retry to the HTTP client and make sure it still builds

  • edit         client.go   (awaiting approval)
  • diagnostics  client.go
      ✗ client.go:42  undefined: backoff  (gopls)
  • edit         client.go   (awaiting approval)
  • diagnostics  client.go
      ✓ no problems`}
      />
      <DocsP>
        Because <InlineCode>diagnostics</InlineCode> is a read-only tool it runs
        without prompting you — see the{' '}
        <DocsLinkInline href="/docs/tools">Built-in Tools</DocsLinkInline> page
        for where it sits among the rest. If no language server is configured
        for the project&apos;s language, the agent simply has no diagnostics to
        read and falls back to building or testing through{' '}
        <InlineCode>bash</InlineCode>.
      </DocsP>

      <DocsH2 id="real-servers">Works with real language servers</DocsH2>
      <DocsP>
        A correct LSP client cannot only send requests and read responses.
        Mature servers like <InlineCode>gopls</InlineCode> and{' '}
        <InlineCode>rust-analyzer</InlineCode> are themselves active over the
        connection: they send{' '}
        <strong className="font-semibold text-fg">
          server-initiated requests
        </strong>{' '}
        back to the client — asking for configuration, registering capabilities,
        reporting work-in-progress, and more. A client that ignores those
        messages leaves the server waiting and the session stalls.
      </DocsP>
      <DocsP>
        BharatCode&apos;s LSP client handles these server-initiated requests
        correctly, replying to them as the protocol expects. That is what lets
        it drive the real <InlineCode>gopls</InlineCode> and{' '}
        <InlineCode>rust-analyzer</InlineCode> binaries — the same servers your
        editor uses — rather than a cut-down stand-in. You get the diagnostics
        those servers actually produce.
      </DocsP>
      <DocsCallout tone="tip" title="Pairs with hooks and AGENTS.md">
        LSP diagnostics tell the agent <em>what</em> is wrong; your project
        conventions tell it <em>how</em> you want it fixed. Combine LSP with{' '}
        <DocsLinkInline href="/docs/agents-md">AGENTS.md</DocsLinkInline>{' '}
        instructions and{' '}
        <DocsLinkInline href="/docs/hooks">lifecycle hooks</DocsLinkInline> to
        keep the agent inside your standards as it works.
      </DocsCallout>
    </DocsPage>
  );
}
