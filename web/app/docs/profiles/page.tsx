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
  title: 'Profiles',
  description:
    'Named config overlays in BharatCode. Use --profile <name> to layer ~/.config/bharatcode/<name>.json on top of your merged config — for example a locked-down review profile or a full-access scripting profile.',
};

export default function ProfilesPage() {
  return (
    <DocsPage
      eyebrow="Configuration"
      title="Profiles"
      lede={
        <>
          A profile is a named config overlay. Pass{' '}
          <InlineCode>--profile &lt;name&gt;</InlineCode> and BharatCode layers{' '}
          <InlineCode>~/.config/bharatcode/&lt;name&gt;.json</InlineCode> on top
          of your already-merged config — the profile wins. Keep a locked-down
          review profile and a full-access scripting profile side by side, and
          switch between them with a single flag.
        </>
      }
      prev={{ href: '/docs/providers', label: 'Providers & Models' }}
      next={{ href: '/docs/agents-md', label: 'AGENTS.md' }}
    >
      <DocsP>
        BharatCode builds its effective config by merging your global config
        with any project config. A profile adds a third, optional layer on top
        of that merge. You opt into a profile per invocation with{' '}
        <InlineCode>--profile &lt;name&gt;</InlineCode>, so the same machine and
        the same project can behave very differently depending on which profile
        you select — strict and read-only for reviewing untrusted code, or wide
        open for trusted automation.
      </DocsP>

      <DocsH2 id="how-it-works">How profiles fit the config merge</DocsH2>
      <DocsP>
        Without a profile, your effective config is the global config merged
        with the project config:
      </DocsP>
      <DocsList>
        <DocsListItem>
          <InlineCode>~/.config/bharatcode/config.json</InlineCode> — your global
          base config.
        </DocsListItem>
        <DocsListItem>
          <InlineCode>./.bharatcode.json</InlineCode> — the project config,
          merged on top of the global one.
        </DocsListItem>
      </DocsList>
      <DocsP>
        When you add <InlineCode>--profile &lt;name&gt;</InlineCode>, BharatCode
        loads <InlineCode>~/.config/bharatcode/&lt;name&gt;.json</InlineCode> and
        overlays it last. The profile is the highest-priority layer, so any
        field it sets overrides the same field from the global and project
        configs. Fields the profile leaves out fall through to whatever the
        merge already produced.
      </DocsP>

      <CodeBlock
        language="text"
        label="merge order (last layer wins)"
        code={`~/.config/bharatcode/config.json      # 1. global base
./.bharatcode.json                    # 2. project overrides global
~/.config/bharatcode/<name>.json      # 3. --profile <name> overrides both`}
      />

      <DocsCallout tone="note" title="Profiles live next to your global config">
        Profile files sit alongside your global{' '}
        <InlineCode>config.json</InlineCode> in{' '}
        <InlineCode>~/.config/bharatcode/</InlineCode>. A file named{' '}
        <InlineCode>review.json</InlineCode> there is selected with{' '}
        <InlineCode>--profile review</InlineCode>. Profiles are user-level, so
        they are available in every project on your machine, not committed with a
        single repo.
      </DocsCallout>

      <DocsH2 id="use-cases">Two common profiles</DocsH2>
      <DocsP>
        The clearest way to think about profiles is by intent. Here are two
        profiles that pull in opposite directions — a cautious one for reading
        code you do not trust, and a permissive one for unattended scripting.
      </DocsP>

      <DocsH3 id="review-profile">A locked-down review profile</DocsH3>
      <DocsP>
        When you are exploring an unfamiliar repository or reviewing a pull
        request, you want the agent to look but not touch. A review profile pins
        the approval mode to <InlineCode>read-only</InlineCode> and denies the
        tools that mutate files or run shell commands, so nothing can be changed
        on disk no matter what the model decides to do.
      </DocsP>

      <CodeBlock
        language="json"
        label="~/.config/bharatcode/review.json"
        code={`{
  "approval_mode": "read-only",
  "permissions": {
    "edit": "deny",
    "multiedit": "deny",
    "write": "deny",
    "bash": "deny"
  }
}`}
      />

      <DocsP>
        Because the profile is the last layer to merge, this{' '}
        <InlineCode>read-only</InlineCode> mode wins even if your global or
        project config set a more permissive mode. The view, ls, glob, grep, and
        diagnostics tools still work, so you can read and search the codebase
        freely while edits and shell access stay blocked.
      </DocsP>

      <DocsH3 id="scripting-profile">A full-access scripting profile</DocsH3>
      <DocsP>
        At the other extreme, an automation or scripting profile assumes you
        already trust the workspace and want the agent to run without stopping
        for approvals. It sets the approval mode to <InlineCode>full</InlineCode>{' '}
        and can point at a fast, cheap model for batch work.
      </DocsP>

      <CodeBlock
        language="json"
        label="~/.config/bharatcode/scripting.json"
        code={`{
  "approval_mode": "full",
  "model": "deepseek/deepseek-chat"
}`}
      />

      <DocsCallout tone="warn" title="Full approval grants real power">
        A profile with <InlineCode>approval_mode: full</InlineCode> lets the
        agent edit files and run shell commands without asking. Use it only in
        workspaces you trust — a disposable clone, a CI runner, or a sandbox.
        See{' '}
        <DocsLinkInline href="/docs/permissions">Permissions</DocsLinkInline>{' '}
        for how approval modes and the ask / allow / deny system interact.
      </DocsCallout>

      <DocsH2 id="invoking">Selecting a profile</DocsH2>
      <DocsP>
        Pass <InlineCode>--profile &lt;name&gt;</InlineCode> to any invocation.
        It works for the interactive TUI and for headless runs alike.
      </DocsP>

      <CodeBlock
        language="bash"
        label="interactive — open the TUI with a profile"
        prompt
        code={`bharatcode --profile review`}
      />

      <CodeBlock
        language="bash"
        label="headless — run a one-shot prompt with a profile"
        prompt
        code={`bharatcode run --profile review "Summarize what changed in this PR"`}
      />

      <DocsP>
        The scripting profile pairs naturally with headless automation. Combine
        it with <InlineCode>--json</InlineCode> for an NDJSON event stream and{' '}
        <InlineCode>--output-last-message</InlineCode> to capture the final
        answer to a file:
      </DocsP>

      <CodeBlock
        language="bash"
        label="unattended automation with the scripting profile"
        prompt
        code={`bharatcode run --profile scripting --json \\
  --output-last-message ./result.txt \\
  "Run the test suite and fix any failing tests"`}
      />

      <DocsH2 id="precedence">Precedence and partial overlays</DocsH2>
      <DocsP>
        A profile does not have to be a complete config. It only needs the
        fields you want to override; everything else is inherited from the merge
        underneath it. That makes profiles small and focused — they describe a
        posture, not a whole configuration.
      </DocsP>
      <DocsP>
        Suppose your global config selects a default model and your project
        config defines providers and a monthly budget. A review profile that
        sets only the approval mode and a few permission denials changes the
        agent&apos;s posture while leaving your model, providers, and budget
        untouched:
      </DocsP>

      <CodeBlock
        language="text"
        label="effective config with --profile review"
        code={`model:        from global config        (profile does not touch it)
providers:    from project config       (profile does not touch it)
budget:       from project config       (profile does not touch it)
approval_mode: read-only                (profile wins)
permissions.edit / write / bash: deny   (profile wins)`}
      />

      <DocsCallout tone="tip" title="Keep a small library of profiles">
        Drop several small files in <InlineCode>~/.config/bharatcode/</InlineCode>{' '}
        — for example <InlineCode>review.json</InlineCode> and{' '}
        <InlineCode>scripting.json</InlineCode> — and switch postures with a
        single flag instead of editing your config by hand each time. The base
        config and project config keep your shared defaults; the profile only
        carries what changes.
      </DocsCallout>

      <DocsP>
        Profiles cover config-level overrides. For instructions that travel with
        a repository rather than your machine, see{' '}
        <DocsLinkInline href="/docs/agents-md">AGENTS.md</DocsLinkInline>, and
        for the full list of config fields you can set in a profile, see{' '}
        <DocsLinkInline href="/docs/configuration">Config files</DocsLinkInline>.
      </DocsP>
    </DocsPage>
  );
}
