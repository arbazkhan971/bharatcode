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
  title: 'Configuration',
  description:
    'How BharatCode is configured: a global config.json merged with a per-project .bharatcode.json, covering providers, models, the INR cost ledger, and permissions.',
};

const ANNOTATED_CONFIG = `{
  // Providers BharatCode can talk to. Each entry is independent — mix
  // hosted open-weight endpoints, frontier APIs, and local runtimes.
  "providers": [
    {
      "name": "deepseek",                  // your label for this provider
      "type": "openai_compatible",         // anthropic | openai | openai_compatible | ollama | lmstudio
      "base_url": "https://api.deepseek.com",
      "api_key_env": "DEEPSEEK_API_KEY",   // key is read from this env var, never stored here
      "models": ["..."]                    // model IDs this provider serves
    },
    {
      "name": "moonshot",
      "type": "openai_compatible",
      "base_url": "https://api.moonshot.ai/v1",
      "api_key_env": "MOONSHOT_API_KEY",
      "models": ["..."]
    },
    {
      "name": "groq",
      "type": "openai_compatible",
      "base_url": "https://api.groq.com/openai/v1",
      "api_key_env": "GROQ_API_KEY",
      "models": ["..."]
    },
    {
      "name": "ollama",                    // fully local — nothing leaves the box
      "type": "ollama",
      "base_url": "http://localhost:11434",
      "models": ["..."]
    }
  ],

  // INR-aware cost ledger with a monthly budget gate. When the
  // monthly spend reaches the limit, the budget gate stops further calls.
  "budget": {
    "monthly_limit_inr": 2000
  },

  // Permission defaults. Verbs: ask | allow | deny. Scopes that an
  // approval can be remembered for: once | session | project | forever.
  "permissions": {
    "mode": "auto",                        // read-only | auto | full
    "bash": "ask",
    "edit": "allow"
  }
}`;

const PROJECT_OVERRIDE = `{
  "providers": [
    {
      "name": "ollama",
      "type": "ollama",
      "base_url": "http://localhost:11434",
      "models": ["..."]
    }
  ],
  "permissions": {
    "mode": "read-only"
  }
}`;

export default function ConfigurationPage() {
  return (
    <DocsPage
      eyebrow="Configuration"
      title="Configuration"
      lede={
        <>
          BharatCode reads its settings from JSON config files. A global file
          holds your defaults; an optional per-project file layers on top of it.
          Everything — providers, the INR cost ledger, and permissions — is
          config-driven, so you can keep machine-wide defaults and still tune
          behavior repo by repo.
        </>
      }
      prev={{ href: '/docs/quick-start', label: 'Quick Start' }}
      next={{ href: '/docs/providers', label: 'Providers & Models' }}
    >
      <DocsH2 id="config-files">Config files</DocsH2>
      <DocsP>
        BharatCode looks in two places and merges them. The global file lives in
        your XDG config directory; the project file sits in your repository
        root:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>~/.config/bharatcode/config.json</InlineCode> — global
          defaults, applied to every project on the machine.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>./.bharatcode.json</InlineCode> — project-local overrides,
          committed (or git-ignored) alongside your code.
        </DocsListItem>
      </DocsList>
      <DocsP>
        The two are <strong className="font-semibold text-fg">merged</strong>,
        and the project file wins. Keep your accounts, API key environment
        variables, and budget in the global file once; use the project file only
        for the few things that differ for a given repository — for example,
        forcing a local-only provider or a stricter permission mode.
      </DocsP>

      <DocsCallout tone="note" title="Merge order">
        Global first, project on top. When the same key appears in both files,
        the value from <InlineCode>./.bharatcode.json</InlineCode> overrides the
        global one. A <DocsLinkInline href="/docs/profiles">profile</DocsLinkInline>{' '}
        (<InlineCode>--profile &lt;name&gt;</InlineCode>) overlays a third,
        named layer on top of both.
      </DocsCallout>

      <DocsH2 id="annotated-example">An annotated config.json</DocsH2>
      <DocsP>
        Here is a complete global config with every major section: the{' '}
        <InlineCode>providers</InlineCode> array, the cost{' '}
        <InlineCode>budget</InlineCode>, and <InlineCode>permissions</InlineCode>
        . The comments below are explanatory — strict JSON does not allow
        comments, so strip the <InlineCode>//</InlineCode> notes before saving
        the file.
      </DocsP>

      <CodeBlock
        language="jsonc"
        label="~/.config/bharatcode/config.json"
        code={ANNOTATED_CONFIG}
      />

      <DocsH3 id="providers">Providers</DocsH3>
      <DocsP>
        Each entry in the <InlineCode>providers</InlineCode> array is one
        endpoint BharatCode can route to. The fields are:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>name</InlineCode> — your label for the provider, used when
          you switch models with <InlineCode>/model</InlineCode> or the{' '}
          <InlineCode>models</InlineCode> command.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>type</InlineCode> — one of{' '}
          <InlineCode>anthropic</InlineCode>, <InlineCode>openai</InlineCode>,{' '}
          <InlineCode>openai_compatible</InlineCode>,{' '}
          <InlineCode>ollama</InlineCode>, or <InlineCode>lmstudio</InlineCode>.
          Most hosted open-weight endpoints speak the OpenAI wire format, so
          they use <InlineCode>openai_compatible</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>base_url</InlineCode> — the API endpoint to call. For local
          runtimes this points at your machine (for example{' '}
          <InlineCode>http://localhost:11434</InlineCode> for Ollama).
        </DocsListItem>
        <DocsListItem>
          <InlineCode>api_key_env</InlineCode> — the name of the environment
          variable that holds the key (e.g.{' '}
          <InlineCode>DEEPSEEK_API_KEY</InlineCode>,{' '}
          <InlineCode>MOONSHOT_API_KEY</InlineCode>,{' '}
          <InlineCode>GROQ_API_KEY</InlineCode>). The key itself is read from the
          environment at runtime — it is never written into the config file.
          Local providers like Ollama and LM Studio need no key, so they omit
          this field.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>models</InlineCode> — the model IDs this provider serves.
          Fill these with the identifiers your chosen provider publishes.
        </DocsListItem>
      </DocsList>

      <DocsCallout tone="tip" title="Keys live in your environment">
        BharatCode resolves <InlineCode>api_key_env</InlineCode> to an
        environment variable rather than embedding secrets in JSON, so you can
        commit a config file without leaking credentials. Export the variable in
        your shell profile, or pass it inline for a single run.
      </DocsCallout>

      <DocsP>
        For the full list of supported providers, how open-weight endpoints are
        treated as first-class, and details on running fully local with Ollama
        and LM Studio, see{' '}
        <DocsLinkInline href="/docs/providers">
          Providers &amp; Models
        </DocsLinkInline>
        .
      </DocsP>

      <DocsH3 id="budget">Budget &amp; the cost ledger</DocsH3>
      <DocsP>
        BharatCode keeps an INR-aware cost ledger and gates spend with a monthly
        budget. The <InlineCode>budget</InlineCode> block sets the monthly limit;
        once you reach it, the budget gate steps in. You can inspect current
        spend and the remaining allowance from the TUI with{' '}
        <InlineCode>/budget</InlineCode>, or from the command line:
      </DocsP>
      <CodeBlock
        language="bash"
        label="check spend and budget"
        prompt
        code={'bharatcode stats\nbharatcode budget'}
      />

      <DocsH3 id="permissions">Permissions</DocsH3>
      <DocsP>
        The <InlineCode>permissions</InlineCode> block sets how BharatCode asks
        before it touches your machine. The overall{' '}
        <InlineCode>mode</InlineCode> is one of{' '}
        <InlineCode>read-only</InlineCode>, <InlineCode>auto</InlineCode>, or{' '}
        <InlineCode>full</InlineCode>. Individual actions resolve to a verb —{' '}
        <InlineCode>ask</InlineCode>, <InlineCode>allow</InlineCode>, or{' '}
        <InlineCode>deny</InlineCode> — and when BharatCode asks, you can remember
        your answer for a chosen scope:{' '}
        <InlineCode>once</InlineCode>, <InlineCode>session</InlineCode>,{' '}
        <InlineCode>project</InlineCode>, or <InlineCode>forever</InlineCode>.
      </DocsP>
      <DocsP>
        You can change the mode mid-session with{' '}
        <InlineCode>/permissions</InlineCode>, and the{' '}
        <InlineCode>--yolo</InlineCode> flag (or the <InlineCode>/yolo</InlineCode>{' '}
        slash command) bypasses prompts entirely. The full model is covered on{' '}
        the{' '}
        <DocsLinkInline href="/docs/permissions">
          Permissions
        </DocsLinkInline>{' '}
        page.
      </DocsP>

      <DocsH2 id="project-overrides">Project overrides</DocsH2>
      <DocsP>
        A project file only needs the keys it changes — it does not have to
        repeat your whole global config. A common pattern is a repo that must
        stay fully local and read-only:
      </DocsP>
      <CodeBlock
        language="jsonc"
        label="./.bharatcode.json"
        code={PROJECT_OVERRIDE}
      />
      <DocsP>
        Dropped into a repository root, this forces a local Ollama provider and a{' '}
        <InlineCode>read-only</InlineCode> permission mode for that project,
        while every other setting still comes from your global config.
      </DocsP>

      <DocsH2 id="bharatcode-config">The <InlineCode>bharatcode config</InlineCode> command</DocsH2>
      <DocsP>
        Use the <InlineCode>config</InlineCode> subcommand to inspect the
        configuration BharatCode has resolved — the effective result after the
        global file, the project file, and any active profile have been merged.
        It is the quickest way to confirm which providers are loaded and which
        settings are actually in effect before you start a session.
      </DocsP>
      <CodeBlock
        language="bash"
        label="inspect resolved configuration"
        prompt
        code={'bharatcode config'}
      />
      <DocsP>
        Related commands round out provider setup:{' '}
        <InlineCode>bharatcode models</InlineCode> lists the models available
        from your configured providers,{' '}
        <InlineCode>bharatcode update-providers</InlineCode> refreshes provider
        data, and <InlineCode>bharatcode doctor</InlineCode> checks your
        environment for common problems. See the{' '}
        <DocsLinkInline href="/docs/cli">CLI Reference</DocsLinkInline> for the
        full list.
      </DocsP>

      <DocsH2 id="where-data-lives">Where the data lives</DocsH2>
      <DocsP>
        BharatCode uses two directories under your home folder, each for a
        distinct purpose:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>~/.config/bharatcode/</InlineCode> — your global
          configuration, including{' '}
          <InlineCode>config.json</InlineCode>.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>~/.bharatcode/prompts/</InlineCode> — the custom prompt
          registry. Drop Markdown files here (<InlineCode>*.md</InlineCode>) to
          add your own <InlineCode>/&lt;name&gt;</InlineCode> slash commands, with{' '}
          <InlineCode>{'{{input}}'}</InlineCode> and{' '}
          <InlineCode>{'{{var}}'}</InlineCode> interpolation.
        </DocsListItem>
      </DocsList>
      <DocsP>
        Project-level instructions are picked up separately:{' '}
        <DocsLinkInline href="/docs/agents-md">
          AGENTS.md / CLAUDE.md
        </DocsLinkInline>{' '}
        files in your repository are ingested into the system prompt, and the{' '}
        per-project <InlineCode>./.bharatcode.json</InlineCode> sits in the repo
        root described above.
      </DocsP>

      <DocsCallout tone="tip" title="Next up">
        Now that your config is in place, head to{' '}
        <DocsLinkInline href="/docs/providers">
          Providers &amp; Models
        </DocsLinkInline>{' '}
        to wire up a model, or to{' '}
        <DocsLinkInline href="/docs/profiles">Profiles</DocsLinkInline> to layer
        named configs with <InlineCode>--profile</InlineCode>.
      </DocsCallout>
    </DocsPage>
  );
}
