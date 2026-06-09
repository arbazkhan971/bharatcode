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
  title: 'Installation',
  description:
    'Install BharatCode with Homebrew, npm, or a one-line install script — no Go toolchain required — or build from source. macOS, Linux, and Windows; CGO-free static binary.',
};

export default function InstallationPage() {
  return (
    <DocsPage
      eyebrow="Getting Started"
      title="Installation"
      lede={
        <>
          BharatCode is a single, self-contained binary — no daemon, no
          background service, no native dependencies. Install it with Homebrew,
          npm, or a one-line script (no Go toolchain required), or build from a
          checkout of the source.
        </>
      }
      prev={{ href: '/docs', label: 'Introduction' }}
      next={{ href: '/docs/quick-start', label: 'Quick Start' }}
    >
      <DocsH2 id="homebrew">Homebrew (macOS / Linux)</DocsH2>
      <DocsP>
        Install from the BharatCode Homebrew tap. This fetches the prebuilt
        binary for your platform and puts <InlineCode>bharatcode</InlineCode> on
        your <InlineCode>PATH</InlineCode> — no Go toolchain needed.
      </DocsP>

      <CodeBlock
        language="bash"
        label="homebrew"
        prompt
        code={'brew install arbazkhan971/tap/bharatcode'}
      />

      <DocsP>
        Upgrade later with{' '}
        <InlineCode>brew upgrade bharatcode</InlineCode>.
      </DocsP>

      <DocsH2 id="npm">npm / npx</DocsH2>
      <DocsP>
        If you already have Node.js, install BharatCode from npm. The package
        downloads the right prebuilt binary for your platform on install.
      </DocsP>

      <CodeBlock
        language="bash"
        label="npm"
        prompt
        code={[
          'npm install -g bharatcode-cli    # global install',
          '',
          '# or run without installing:',
          'npx bharatcode-cli',
        ].join('\n')}
      />

      <DocsH2 id="install-script">Install script</DocsH2>
      <DocsP>
        A one-line installer downloads the latest release binary for your
        platform. On macOS and Linux it installs to{' '}
        <InlineCode>~/.local/bin</InlineCode>; on Windows it installs to{' '}
        <InlineCode>%LOCALAPPDATA%\Programs\bharatcode</InlineCode> and updates
        your user <InlineCode>PATH</InlineCode>.
      </DocsP>

      <CodeBlock
        language="bash"
        label="macOS / linux"
        prompt
        code={
          'curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh | sh'
        }
      />

      <CodeBlock
        language="powershell"
        label="windows (powershell)"
        prompt
        code={
          'irm https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.ps1 | iex'
        }
      />

      <DocsCallout tone="tip" title="Pin a version">
        Set <InlineCode>BHARATCODE_VERSION</InlineCode> to install a specific
        release tag instead of the latest — for example{' '}
        <InlineCode>
          curl -fsSL …/install.sh | BHARATCODE_VERSION=v0.2.0 sh
        </InlineCode>
        .
      </DocsCallout>

      <DocsH2 id="install-go-install">go install</DocsH2>
      <DocsP>
        If you have Go 1.24 or newer, install directly with the Go toolchain.
        This compiles BharatCode and places the binary in your Go bin directory
        (<InlineCode>$(go env GOPATH)/bin</InlineCode>, typically{' '}
        <InlineCode>~/go/bin</InlineCode>) — make sure that is on your{' '}
        <InlineCode>PATH</InlineCode>.
      </DocsP>

      <CodeBlock
        language="bash"
        label="install"
        prompt
        code={'go install github.com/arbazkhan971/bharatcode@latest'}
      />

      <DocsCallout tone="note" title="Version reporting">
        The <InlineCode>go install</InlineCode> path does not stamp the build
        version, so <InlineCode>bharatcode version</InlineCode> and{' '}
        <InlineCode>bharatcode update</InlineCode> report an unknown commit. Use
        Homebrew, the install script, or a source build for accurate version
        reporting.
      </DocsCallout>

      <DocsH2 id="verify">Verify the installation</DocsH2>
      <DocsP>
        Once the binary is on your <InlineCode>PATH</InlineCode>, confirm it runs
        and reports a version.
      </DocsP>

      <CodeBlock
        language="bash"
        label="check version"
        prompt
        code={'bharatcode version'}
      />

      <DocsP>
        If your shell prints “command not found”, the Go bin directory is not on
        your <InlineCode>PATH</InlineCode> yet — re-open your terminal after
        editing your shell profile, or run the binary by its full path (for
        example <InlineCode>~/go/bin/bharatcode version</InlineCode>) to confirm
        it installed.
      </DocsP>

      <DocsH3 id="doctor">Run the doctor</DocsH3>
      <DocsP>
        <InlineCode>bharatcode doctor</InlineCode> runs an environment check — it
        inspects your setup and surfaces anything that might get in the way, such
        as a missing or unreadable config file or a provider whose API key
        environment variable is not set.
      </DocsP>

      <CodeBlock
        language="bash"
        label="diagnose your setup"
        prompt
        code={'bharatcode doctor'}
      />

      <DocsP>
        Run it any time something feels off — right after installing, after
        editing your{' '}
        <DocsLinkInline href="/docs/configuration">config</DocsLinkInline>, or
        when a{' '}
        <DocsLinkInline href="/docs/providers">provider</DocsLinkInline> is not
        being picked up.
      </DocsP>

      <DocsH2 id="from-source">Build from source</DocsH2>
      <DocsP>
        Prefer to build from a checkout — to track the main branch, hack on the
        code, or produce a binary in a controlled environment? Clone the
        repository and build it with the Go toolchain.
      </DocsP>

      <CodeBlock
        language="bash"
        label="clone & build"
        prompt
        code={[
          'git clone https://github.com/arbazkhan971/bharatcode.git',
          'cd bharatcode',
          'make build        # stamps version + commit into the binary',
        ].join('\n')}
      />

      <DocsP>
        That produces a <InlineCode>bharatcode</InlineCode> binary under{' '}
        <InlineCode>bin/</InlineCode>, which you can run directly or move onto
        your <InlineCode>PATH</InlineCode>:
      </DocsP>

      <CodeBlock
        language="bash"
        label="run it / install it"
        prompt
        code={[
          './bin/bharatcode version',
          '',
          '# or install into your Go bin directory',
          'make install',
        ].join('\n')}
      />

      <DocsCallout tone="note" title="CGO-free">
        Building from source needs Go 1.24+ and nothing else. The build is
        <InlineCode>CGO_ENABLED=0</InlineCode> — no C compiler, no system
        libraries — so the binary is fully static and easy to drop onto a server
        or into a CI image.
      </DocsCallout>

      <DocsH2 id="upgrading">Upgrading</DocsH2>
      <DocsP>
        Run <InlineCode>bharatcode update</InlineCode> to check whether a newer
        version is available. Then upgrade with the same method you installed
        with — <InlineCode>brew upgrade bharatcode</InlineCode>,{' '}
        <InlineCode>npm install -g bharatcode-cli@latest</InlineCode>, re-running the
        install script, or pulling and rebuilding a source checkout.
      </DocsP>

      <CodeBlock
        language="bash"
        label="check for updates"
        prompt
        code={'bharatcode update'}
      />

      <DocsP>
        Confirm the upgrade landed with{' '}
        <InlineCode>bharatcode version</InlineCode>.
      </DocsP>

      <DocsH2 id="next-steps">Next steps</DocsH2>
      <DocsP>
        With the binary installed and verified, you are ready to point it at a
        model and start working.
      </DocsP>
      <DocsList>
        <DocsListItem>
          <DocsLinkInline href="/docs/quick-start">Quick Start</DocsLinkInline> —
          launch the TUI and run your first session.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/providers">
            Providers &amp; Models
          </DocsLinkInline>{' '}
          — connect an open-weight, hosted, or fully local model.
        </DocsListItem>
        <DocsListItem>
          <DocsLinkInline href="/docs/configuration">
            Config files
          </DocsLinkInline>{' '}
          — set up your global and per-project configuration.
        </DocsListItem>
      </DocsList>
    </DocsPage>
  );
}
