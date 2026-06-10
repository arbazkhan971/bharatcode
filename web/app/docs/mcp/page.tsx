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
  title: 'MCP Integration',
  description:
    'BharatCode is a Model Context Protocol (MCP) client supporting stdio, HTTP, and SSE transports. Configure MCP servers in config, see how bridged tools appear to the agent with server-prefixed names, and set per-server permissions.',
};

const STDIO_EXAMPLE = `{
  "mcp": {
    "servers": {
      "filesystem": {
        "type": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"]
      }
    }
  }
}`;

const REMOTE_EXAMPLE = `{
  "mcp": {
    "servers": {
      "docs": {
        "type": "http",
        "url": "https://mcp.example.com/mcp"
      },
      "events": {
        "type": "sse",
        "url": "https://mcp.example.com/sse"
      }
    }
  }
}`;

const ANNOTATED_EXAMPLE = `{
  "mcp": {
    "servers": {
      // A local (stdio) server: BharatCode launches the process and
      // talks to it over stdin/stdout. Use for tools that run on your box.
      "filesystem": {
        "type": "stdio",                 // stdio | http | sse
        "command": "npx",                // the executable to launch
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"],
        "env": {                         // extra env vars for the child process
          "LOG_LEVEL": "info"
        },
        "permission": "ask"              // ask | allow | deny — applies to this server's tools
      },

      // A remote server reached over HTTP. No process is spawned;
      // BharatCode connects to the URL. Use "sse" for Server-Sent Events.
      "company-tools": {
        "type": "http",
        "url": "https://mcp.example.com/mcp"
      }
    }
  }
}`;

const GITHUB_EXAMPLE = `{
  "mcp": {
    "servers": {
      "github": {
        "type": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-github"],
        "permission": "ask"
      }
    }
  }
}`;

export default function McpPage() {
  return (
    <DocsPage
      eyebrow="Integrations"
      title="MCP Integration"
      lede={
        <>
          BharatCode is a Model Context Protocol (MCP) client. Point it at one or
          more MCP servers and their tools are bridged into the agent alongside
          the built-ins — over a local <InlineCode>stdio</InlineCode> process or a
          remote <InlineCode>http</InlineCode> / <InlineCode>sse</InlineCode>{' '}
          endpoint. Each server is just another entry in your config.
        </>
      }
      prev={{ href: '/docs/permissions', label: 'Permissions' }}
      next={{ href: '/docs/lsp', label: 'LSP' }}
    >
      <DocsH2 id="what-is-mcp">What is MCP?</DocsH2>
      <DocsP>
        The{' '}
        <a
          href="https://modelcontextprotocol.io"
          target="_blank"
          rel="noreferrer"
          className="font-medium text-blue underline decoration-blue/30 underline-offset-2 transition-colors hover:decoration-blue"
        >
          Model Context Protocol
        </a>{' '}
        (MCP) is an open standard for connecting AI agents to external tools and
        data sources. A program that exposes capabilities — reading files,
        searching a database, calling an API — implements an MCP{' '}
        <strong className="font-semibold text-fg">server</strong>. An agent that
        consumes those capabilities is an MCP{' '}
        <strong className="font-semibold text-fg">client</strong>.
      </DocsP>
      <DocsP>
        BharatCode is an MCP client. Once a server is configured, the tools it
        advertises become available to the model in the same way as the{' '}
        <DocsLinkInline href="/docs/tools">built-in tools</DocsLinkInline> — the
        agent can call them, you approve them through the same{' '}
        <DocsLinkInline href="/docs/permissions">permission system</DocsLinkInline>
        , and their results flow back into the conversation. You write no glue
        code: any compliant MCP server works out of the box.
      </DocsP>

      <DocsH2 id="transports">Transports</DocsH2>
      <DocsP>
        BharatCode supports three MCP transports. Which one you use depends on
        whether the server runs locally as a child process or remotely behind a
        URL:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>stdio</InlineCode> — BharatCode launches the server as a
          local child process and communicates over its standard input and
          output. This is the most common setup for tools that run on your own
          machine.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>http</InlineCode> — BharatCode connects to a remote server
          over HTTP at a given <InlineCode>url</InlineCode>. No process is
          spawned; the server is hosted elsewhere.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>sse</InlineCode> — a remote transport using Server-Sent
          Events, also addressed by a <InlineCode>url</InlineCode>. Use this when
          the server you are connecting to exposes an SSE endpoint.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="configuring-servers">Configuring MCP servers</DocsH2>
      <DocsP>
        MCP servers live under an <InlineCode>mcp</InlineCode> block in your
        config, inside a <InlineCode>servers</InlineCode> map. The map key is the
        name you choose for the server — keep it short and lowercase, because it
        becomes the prefix on every bridged tool (see{' '}
        <DocsLinkInline href="#bridged-tools">below</DocsLinkInline>). Like the
        rest of BharatCode, this can sit in your global{' '}
        <InlineCode>~/.config/bharatcode/config.json</InlineCode> or a per-project{' '}
        <InlineCode>./.bharatcode.json</InlineCode>; see{' '}
        <DocsLinkInline href="/docs/configuration">Configuration</DocsLinkInline>{' '}
        for how the two files merge.
      </DocsP>

      <DocsH3 id="stdio-servers">Local (stdio) servers</DocsH3>
      <DocsP>
        A <InlineCode>stdio</InlineCode> server is described by the executable to
        run. The fields are <InlineCode>command</InlineCode> (the program),{' '}
        <InlineCode>args</InlineCode> (its arguments), and an optional{' '}
        <InlineCode>env</InlineCode> map of environment variables passed to the
        child process:
      </DocsP>
      <CodeBlock
        language="json"
        label="./.bharatcode.json"
        code={STDIO_EXAMPLE}
      />
      <DocsP>
        Here BharatCode runs the official MCP filesystem server with{' '}
        <InlineCode>npx</InlineCode>, scoping it to a single directory passed as
        the last argument. The server starts when BharatCode starts and is shut
        down when your session ends.
      </DocsP>

      <DocsH3 id="remote-servers">Remote (HTTP / SSE) servers</DocsH3>
      <DocsP>
        A remote server is described by its <InlineCode>type</InlineCode> (
        <InlineCode>http</InlineCode> or <InlineCode>sse</InlineCode>) and a{' '}
        <InlineCode>url</InlineCode>. No <InlineCode>command</InlineCode> or{' '}
        <InlineCode>args</InlineCode> are needed because BharatCode does not
        launch the process — it simply connects:
      </DocsP>
      <CodeBlock
        language="json"
        label="~/.config/bharatcode/config.json"
        code={REMOTE_EXAMPLE}
      />

      <DocsH3 id="all-fields">All server fields</DocsH3>
      <DocsP>
        Putting it together, here is an annotated entry showing every field. As
        with the rest of BharatCode&apos;s config, strict JSON has no comments —
        strip the <InlineCode>//</InlineCode> notes before saving:
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="~/.config/bharatcode/config.json (annotated)"
        code={ANNOTATED_EXAMPLE}
      />
      <DocsList>
        <DocsListItem>
          <InlineCode>type</InlineCode> — the transport:{' '}
          <InlineCode>stdio</InlineCode>, <InlineCode>http</InlineCode>, or{' '}
          <InlineCode>sse</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>command</InlineCode> — for <InlineCode>stdio</InlineCode>{' '}
          servers, the executable BharatCode launches.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>args</InlineCode> — for <InlineCode>stdio</InlineCode>{' '}
          servers, the list of arguments passed to{' '}
          <InlineCode>command</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>env</InlineCode> — for <InlineCode>stdio</InlineCode>{' '}
          servers, additional environment variables to set on the child process.
          The child also inherits BharatCode&apos;s own environment, so secrets
          like API tokens are best exported in your shell rather than written
          here.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>url</InlineCode> — for <InlineCode>http</InlineCode> and{' '}
          <InlineCode>sse</InlineCode> servers, the endpoint to connect to.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>permission</InlineCode> — an optional per-server permission
          verb (<InlineCode>ask</InlineCode>, <InlineCode>allow</InlineCode>, or{' '}
          <InlineCode>deny</InlineCode>) applied to every tool the server bridges.
          See{' '}
          <DocsLinkInline href="#per-server-permission">
            per-server permission
          </DocsLinkInline>{' '}
          below.
        </DocsListItem>
      </DocsList>

      <DocsCallout tone="tip" title="Keep secrets in the environment">
        For tokens and keys, prefer your shell environment over hardcoded
        values. A <InlineCode>stdio</InlineCode> server is a child process that
        BharatCode launches, so it inherits BharatCode&apos;s environment —
        export the variable the server expects (for example a{' '}
        <InlineCode>GITHUB_PERSONAL_ACCESS_TOKEN</InlineCode> for the GitHub
        server) and it is picked up automatically. The optional{' '}
        <InlineCode>env</InlineCode> map is for setting additional, non-secret
        variables on the child. This mirrors how providers read keys from{' '}
        <InlineCode>api_key_env</InlineCode>, so you can commit a config file
        without leaking credentials.
      </DocsCallout>

      <DocsH2 id="bridged-tools">How bridged tools appear to the agent</DocsH2>
      <DocsP>
        When a server connects, BharatCode reads the list of tools it advertises
        and bridges each one into the agent&apos;s toolset. To avoid collisions
        between servers — and with the built-ins — bridged tool names are{' '}
        <strong className="font-semibold text-fg">server-prefixed</strong>: the
        server&apos;s name from your <InlineCode>servers</InlineCode> map is
        prepended to each tool.
      </DocsP>
      <DocsP>
        So a <InlineCode>github</InlineCode> server that advertises a{' '}
        <InlineCode>create_issue</InlineCode> tool surfaces to the model as{' '}
        <InlineCode>github_create_issue</InlineCode>; a{' '}
        <InlineCode>filesystem</InlineCode> server&apos;s{' '}
        <InlineCode>read_file</InlineCode> becomes{' '}
        <InlineCode>filesystem_read_file</InlineCode>. The prefix is the same
        name you chose as the map key, which is why a short, lowercase server
        name reads best. These bridged tools sit alongside built-ins like{' '}
        <InlineCode>view</InlineCode>, <InlineCode>edit</InlineCode>, and{' '}
        <InlineCode>bash</InlineCode> — the model picks whichever fits the task.
      </DocsP>
      <DocsCallout tone="note" title="Names are illustrative">
        The exact tool names depend on what each MCP server advertises; the
        examples above show the <em>shape</em> of a bridged name (
        <InlineCode>{'<server>_<tool>'}</InlineCode>), not a fixed list. Run a
        session and the model will see whatever tools your configured servers
        expose, each carrying its server prefix.
      </DocsCallout>

      <DocsH2 id="per-server-permission">Per-server permission</DocsH2>
      <DocsP>
        Bridged tools are not exempt from BharatCode&apos;s safety model. Because
        each one has a server-prefixed name, it flows through the same{' '}
        <DocsLinkInline href="/docs/permissions">permission system</DocsLinkInline>{' '}
        as everything else: the active mode (
        <InlineCode>read-only</InlineCode>, <InlineCode>auto</InlineCode>, or{' '}
        <InlineCode>full</InlineCode>) and per-action verbs (
        <InlineCode>ask</InlineCode>, <InlineCode>allow</InlineCode>,{' '}
        <InlineCode>deny</InlineCode>) all apply. When BharatCode asks, you can
        remember the answer for a scope — <InlineCode>once</InlineCode>,{' '}
        <InlineCode>session</InlineCode>, <InlineCode>project</InlineCode>, or{' '}
        <InlineCode>forever</InlineCode>.
      </DocsP>
      <DocsP>
        To set a default for an entire server, add a{' '}
        <InlineCode>permission</InlineCode> verb to its entry. This applies to
        every tool the server bridges, which is handy when a server is
        trustworthy enough to auto-allow, or sensitive enough to always confirm:
      </DocsP>
      <CodeBlock
        language="json"
        label="per-server permission"
        code={`{
  "mcp": {
    "servers": {
      "filesystem": {
        "type": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"],
        "permission": "allow"
      },
      "github": {
        "type": "stdio",
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-github"],
        "permission": "ask"
      }
    }
  }
}`}
      />
      <DocsP>
        Here the read-mostly filesystem server is auto-allowed, while the GitHub
        server — which can open issues and push changes — always asks first.
      </DocsP>

      <DocsH2 id="worked-example">Worked example: the GitHub MCP server</DocsH2>
      <DocsP>
        Let&apos;s wire up the official GitHub MCP server end to end. First,
        export your token in the shell — using the exact variable name the
        server reads — so the secret never lands in a config file. Because
        BharatCode launches the server as a child process, it inherits this
        variable automatically:
      </DocsP>
      <CodeBlock
        language="bash"
        label="export a GitHub token"
        prompt
        code={'export GITHUB_PERSONAL_ACCESS_TOKEN="ghp_your_personal_access_token"'}
      />
      <DocsP>
        Then add the server to your config. It runs over{' '}
        <InlineCode>stdio</InlineCode> via <InlineCode>npx</InlineCode>; no token
        appears here, because the server reads it from the inherited
        environment:
      </DocsP>
      <CodeBlock
        language="json"
        label="~/.config/bharatcode/config.json"
        code={GITHUB_EXAMPLE}
      />
      <DocsP>
        Start BharatCode as usual. On launch it spawns the GitHub server, reads
        the tools it advertises, and bridges them under the{' '}
        <InlineCode>github_</InlineCode> prefix. Now you can ask the agent to use
        them in plain language:
      </DocsP>
      <CodeBlock
        language="bash"
        label="use a bridged tool"
        prompt
        code={'bharatcode run "Open a GitHub issue titled \'Flaky CI on main\' summarizing the last three failed runs"'}
      />
      <DocsP>
        Because the server entry carries <InlineCode>{'"permission": "ask"'}</InlineCode>
        , BharatCode pauses for your approval before any{' '}
        <InlineCode>github_*</InlineCode> tool runs — so the model can propose the
        action, but nothing is created on your account until you say yes.
      </DocsP>

      <DocsCallout tone="tip" title="Next up">
        MCP brings external tools in; the next integration brings your editor&apos;s
        language intelligence in. See{' '}
        <DocsLinkInline href="/docs/lsp">LSP</DocsLinkInline> for in-context
        diagnostics, or{' '}
        <DocsLinkInline href="/docs/hooks">Hooks</DocsLinkInline> to run your own
        shell commands around tool calls.
      </DocsCallout>
    </DocsPage>
  );
}
