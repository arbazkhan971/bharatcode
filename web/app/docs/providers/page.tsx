import type { Metadata } from 'next';

import { Badge } from '@/app/components/ui/Badge';
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
  title: 'Providers & Models',
  description:
    'How BharatCode providers work — config-driven entries with a type, base URL, API key env var, and model list. Use open-weight, India-hosted, or fully local models so your data stays in India.',
};

/**
 * Supported providers table. Each row mirrors the config entry you would write:
 * a `type` (anthropic | openai | openai_compatible | ollama | lmstudio), an
 * `api_key_env` (the env var BharatCode reads the key from), and tags for
 * open-weight and local/on-device posture. Local + India-hosted providers are
 * what let your source code stay in the country.
 */
type ProviderRow = {
  name: string;
  type: string;
  /** Env var BharatCode reads the API key from — em dash for local runtimes. */
  apiKeyEnv: string;
  /** Serves open-weight model families. */
  openWeight: boolean;
  /** Runs fully on your own machine — no data leaves the device. */
  local: boolean;
};

const PROVIDERS: ProviderRow[] = [
  {
    name: 'DeepSeek',
    type: 'openai_compatible',
    apiKeyEnv: 'DEEPSEEK_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'Moonshot / Kimi',
    type: 'openai_compatible',
    apiKeyEnv: 'MOONSHOT_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'Groq',
    type: 'openai_compatible',
    apiKeyEnv: 'GROQ_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'Together',
    type: 'openai_compatible',
    apiKeyEnv: 'TOGETHER_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'Fireworks',
    type: 'openai_compatible',
    apiKeyEnv: 'FIREWORKS_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'OpenRouter',
    type: 'openai_compatible',
    apiKeyEnv: 'OPENROUTER_API_KEY',
    openWeight: true,
    local: false,
  },
  {
    name: 'Google Gemini',
    type: 'openai_compatible',
    apiKeyEnv: 'GEMINI_API_KEY',
    openWeight: false,
    local: false,
  },
  {
    name: 'OpenAI',
    type: 'openai',
    apiKeyEnv: 'OPENAI_API_KEY',
    openWeight: false,
    local: false,
  },
  {
    name: 'Anthropic',
    type: 'anthropic',
    apiKeyEnv: 'ANTHROPIC_API_KEY',
    openWeight: false,
    local: false,
  },
  {
    name: 'Ollama',
    type: 'ollama',
    apiKeyEnv: '—',
    openWeight: true,
    local: true,
  },
  {
    name: 'LM Studio',
    type: 'lmstudio',
    apiKeyEnv: '—',
    openWeight: true,
    local: true,
  },
];

export default function ProvidersPage() {
  return (
    <DocsPage
      eyebrow="Configuration"
      title="Providers & Models"
      lede={
        <>
          A provider tells BharatCode where a model lives and how to reach it.
          Every provider is a plain config entry — a name, a type, a base URL, an
          API-key environment variable, and a list of models. Point BharatCode at
          open-weight endpoints, India-hosted gateways, or fully local runtimes
          so your code only ever travels where you allow it.
        </>
      }
      prev={{ href: '/docs/configuration', label: 'Config files' }}
      next={{ href: '/docs/profiles', label: 'Profiles' }}
    >
      <DocsH2 id="how-providers-work">How providers work</DocsH2>
      <DocsP>
        BharatCode has no hardcoded list of vendors baked into the binary.
        Instead, every model source is described by a <em>provider entry</em> in
        your{' '}
        <DocsLinkInline href="/docs/configuration">config file</DocsLinkInline>.
        Each entry has five fields:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>name</InlineCode> — a label you choose. It is how you refer
          to the provider in <InlineCode>/model</InlineCode> and in{' '}
          <InlineCode>bharatcode models</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>type</InlineCode> — the wire protocol BharatCode uses to
          talk to the endpoint. One of <InlineCode>anthropic</InlineCode>,{' '}
          <InlineCode>openai</InlineCode>,{' '}
          <InlineCode>openai_compatible</InlineCode>,{' '}
          <InlineCode>ollama</InlineCode>, or <InlineCode>lmstudio</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>base_url</InlineCode> — the HTTP endpoint. This is what
          lets you pin a provider to an India-hosted gateway or a machine on your
          own network.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>api_key_env</InlineCode> — the name of the environment
          variable BharatCode reads the API key from. The key itself never lives
          in the config file. Local runtimes need no key.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>models</InlineCode> — the list of model IDs this provider
          serves. These are the entries you pick from when you switch models.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Because the shape is the same for every vendor, adding a new provider —
        or repointing an existing one at a different region — is just editing
        JSON. Nothing about the protocol leaks into the rest of the agent.
      </DocsP>

      <DocsH2 id="provider-types">Provider types</DocsH2>
      <DocsP>
        The <InlineCode>type</InlineCode> field selects how BharatCode formats
        requests and parses responses. Pick the one that matches the API your
        endpoint speaks:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>anthropic</InlineCode> — the native Anthropic Messages API
          (Claude models).
        </DocsListItem>
        <DocsListItem>
          <InlineCode>openai</InlineCode> — the native OpenAI API (GPT models).
        </DocsListItem>
        <DocsListItem>
          <InlineCode>openai_compatible</InlineCode> — any endpoint that speaks
          the OpenAI Chat Completions protocol. This is the workhorse type: most
          open-weight gateways (DeepSeek, Moonshot/Kimi, Groq, Together,
          Fireworks, OpenRouter) and many self-hosted servers expose an
          OpenAI-compatible API, so you can reach them by setting{' '}
          <InlineCode>base_url</InlineCode> and{' '}
          <InlineCode>api_key_env</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>ollama</InlineCode> — a local{' '}
          <a
            href="https://ollama.com"
            className="font-medium text-blue underline decoration-blue/30 underline-offset-2 transition-colors hover:decoration-blue"
            target="_blank"
            rel="noreferrer"
          >
            Ollama
          </a>{' '}
          server. Runs models on your own machine; no API key.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>lmstudio</InlineCode> — a local{' '}
          <a
            href="https://lmstudio.ai"
            className="font-medium text-blue underline decoration-blue/30 underline-offset-2 transition-colors hover:decoration-blue"
            target="_blank"
            rel="noreferrer"
          >
            LM Studio
          </a>{' '}
          server. Also fully on-device; no API key.
        </DocsListItem>
      </DocsList>

      <DocsH2 id="supported-providers">Supported providers</DocsH2>
      <DocsP>
        These are the providers BharatCode knows how to talk to out of the box.
        Open-weight models come first by design — they are typically far cheaper
        than frontier closed models, and you can reach many of them through
        India-hosted gateways or run them entirely on your own hardware.
      </DocsP>

      {/* Legend for the table tags. */}
      <DocsList>
        <DocsListItem>
          <span className="inline-flex items-center gap-1.5 align-middle">
            <Dot className="bg-saffron" />
            <span className="font-medium text-fg">Open-weight</span>
          </span>{' '}
          — serves open-weight model families. No vendor lock-in.
        </DocsListItem>
        <DocsListItem>
          <span className="inline-flex items-center gap-1.5 align-middle">
            <Dot className="bg-green" />
            <span className="font-medium text-fg">Local</span>
          </span>{' '}
          — runs fully on your machine. Source code never leaves the device.
        </DocsListItem>
      </DocsList>

      <ProviderTable />

      <DocsCallout tone="note" title="On model IDs">
        BharatCode does not ship a fixed catalogue of model names. The exact
        model IDs you list under a provider depend on what that endpoint serves
        at the time — check the provider&apos;s own documentation, or run{' '}
        <InlineCode>bharatcode models</InlineCode> after configuring it to see
        what is available.
      </DocsCallout>

      <DocsH2 id="configuring-a-provider">Configuring a provider</DocsH2>
      <DocsP>
        Providers live under a <InlineCode>providers</InlineCode> array in your
        config. The global file is{' '}
        <InlineCode>~/.config/bharatcode/config.json</InlineCode>; a project file
        at <InlineCode>./.bharatcode.json</InlineCode> is merged on top for
        repo-specific overrides. Here is a hosted open-weight provider —
        DeepSeek, reached over its OpenAI-compatible endpoint:
      </DocsP>
      <CodeBlock
        language="json"
        label="~/.config/bharatcode/config.json"
        code={`{
  "providers": [
    {
      "name": "deepseek",
      "type": "openai_compatible",
      "base_url": "https://api.deepseek.com/v1",
      "api_key_env": "DEEPSEEK_API_KEY",
      "models": ["deepseek-chat", "deepseek-reasoner"]
    }
  ]
}`}
      />
      <DocsP>
        The key never goes in the file — BharatCode reads it from the environment
        variable named in <InlineCode>api_key_env</InlineCode>. Export it in your
        shell (or your secrets manager):
      </DocsP>
      <CodeBlock
        language="bash"
        label="shell"
        prompt
        code={'export DEEPSEEK_API_KEY="sk-..."'}
      />

      <DocsH3 id="local-providers">Local providers (data stays on device)</DocsH3>
      <DocsP>
        Local runtimes are the strongest privacy posture: the model runs on your
        own machine, so your source code never leaves it. A local provider needs
        no API key — only a <InlineCode>base_url</InlineCode> pointing at the
        local server. Ollama and LM Studio both work this way:
      </DocsP>
      <CodeBlock
        language="json"
        label=".bharatcode.json"
        code={`{
  "providers": [
    {
      "name": "ollama-local",
      "type": "ollama",
      "base_url": "http://localhost:11434",
      "models": ["qwen2.5-coder", "deepseek-coder-v2"]
    },
    {
      "name": "lmstudio-local",
      "type": "lmstudio",
      "base_url": "http://localhost:1234/v1",
      "models": ["your-loaded-model"]
    }
  ]
}`}
      />

      <DocsH2 id="custom-endpoint">Adding a custom OpenAI-compatible endpoint</DocsH2>
      <DocsP>
        Because <InlineCode>openai_compatible</InlineCode> is just &ldquo;speaks
        the OpenAI Chat Completions protocol,&rdquo; you can point BharatCode at
        any gateway that follows it — including an India-hosted inference
        endpoint, a regional proxy, or a self-hosted vLLM / TGI server. Set the{' '}
        <InlineCode>base_url</InlineCode> to that endpoint and name an env var for
        its key:
      </DocsP>
      <CodeBlock
        language="json"
        label=".bharatcode.json"
        code={`{
  "providers": [
    {
      "name": "india-gateway",
      "type": "openai_compatible",
      "base_url": "https://llm.example.in/v1",
      "api_key_env": "INDIA_GATEWAY_API_KEY",
      "models": ["kimi-k2", "qwen2.5-coder-32b"]
    }
  ]
}`}
      />
      <CodeBlock
        language="bash"
        label="shell"
        prompt
        code={'export INDIA_GATEWAY_API_KEY="..."'}
      />
      <DocsCallout tone="tip" title="Region pinning">
        Setting <InlineCode>base_url</InlineCode> to an in-country endpoint is how
        you keep inference traffic inside India while still using a hosted model.
        Combine it with a{' '}
        <DocsLinkInline href="/docs/profiles">profile</DocsLinkInline> to switch
        between a local provider for sensitive repos and a hosted one elsewhere.
      </DocsCallout>

      <DocsH2 id="listing-and-updating">Listing &amp; updating providers</DocsH2>
      <DocsP>
        Once a provider is configured, list every model BharatCode can currently
        reach — grouped by provider — with:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode models'}
      />
      <DocsP>
        Inside the TUI, <InlineCode>/model</InlineCode> opens the same picker so
        you can switch the active model mid-session. To refresh provider
        definitions, run:
      </DocsP>
      <CodeBlock
        language="bash"
        label="terminal"
        prompt
        code={'bharatcode update-providers'}
      />
      <DocsP>
        For providers that use a sign-in flow rather than a raw key, manage
        credentials with <InlineCode>bharatcode login</InlineCode> and{' '}
        <InlineCode>bharatcode logout</InlineCode>. See the full{' '}
        <DocsLinkInline href="/docs/cli">CLI reference</DocsLinkInline> for every
        subcommand.
      </DocsP>

      <DocsH2 id="data-stays-in-india">Data stays in India</DocsH2>
      <DocsP>
        Providers are the mechanism behind BharatCode&apos;s core promise:{' '}
        <strong className="font-semibold text-fg">
          your code only goes where you send it.
        </strong>{' '}
        BharatCode never phones home — it connects solely to the endpoints in
        your config. That gives you two ways to keep data in the country:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <strong className="font-semibold text-fg">Run fully local.</strong>{' '}
          With an <InlineCode>ollama</InlineCode> or{' '}
          <InlineCode>lmstudio</InlineCode> provider, inference happens on your
          own machine and your source never touches the network. Ideal for
          air-gapped or highly sensitive work.
        </DocsListItem>
        <DocsListItem>
          <strong className="font-semibold text-fg">
            Pin to an India-hosted endpoint.
          </strong>{' '}
          Point an <InlineCode>openai_compatible</InlineCode> provider&apos;s{' '}
          <InlineCode>base_url</InlineCode> at an in-country gateway so hosted
          inference stays within India&apos;s borders.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Open-weight models make both routes practical: the same model families
        are available locally, through regional gateways, and from global hosts,
        so you can choose the posture each project needs without changing how you
        work.
      </DocsP>
    </DocsPage>
  );
}

/** Small colored dot used in the legend and the type column. */
function Dot({ className = '' }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={`inline-block h-1.5 w-1.5 shrink-0 rounded-full ${className}`}
    />
  );
}

/**
 * Supported-providers table. Scrolls horizontally on narrow screens so the
 * five columns stay readable without wrapping awkwardly on mobile.
 */
function ProviderTable() {
  return (
    <div className="overflow-x-auto rounded-xl border border-border bg-bg-elevated">
      <table className="w-full min-w-[36rem] border-collapse text-left text-sm">
        <thead>
          <tr className="border-b border-border text-xs uppercase tracking-wider text-faint">
            <th scope="col" className="px-4 py-3 font-medium">
              Provider
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              Type
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              API key env var
            </th>
            <th scope="col" className="px-4 py-3 font-medium">
              Tags
            </th>
          </tr>
        </thead>
        <tbody>
          {PROVIDERS.map((p) => (
            <tr
              key={p.name}
              className="border-b border-border/60 last:border-0 transition-colors hover:bg-surface/40"
            >
              <th
                scope="row"
                className="whitespace-nowrap px-4 py-3 font-medium text-fg"
              >
                {p.name}
              </th>
              <td className="whitespace-nowrap px-4 py-3">
                <code className="font-mono text-[0.8125rem] text-muted">
                  {p.type}
                </code>
              </td>
              <td className="whitespace-nowrap px-4 py-3">
                {p.apiKeyEnv === '—' ? (
                  <span className="text-faint">— none (local)</span>
                ) : (
                  <code className="font-mono text-[0.8125rem] text-muted">
                    {p.apiKeyEnv}
                  </code>
                )}
              </td>
              <td className="px-4 py-3">
                <div className="flex flex-wrap items-center gap-1.5">
                  {p.openWeight ? (
                    <Badge variant="saffron" size="sm">
                      Open-weight
                    </Badge>
                  ) : null}
                  {p.local ? (
                    <Badge variant="green" size="sm">
                      Local
                    </Badge>
                  ) : null}
                </div>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
