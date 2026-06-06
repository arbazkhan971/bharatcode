<!--
  This file is staged here to avoid a merge conflict with the Homebrew packaging
  work that also edits docs/install.md. Merge the section below into
  docs/install.md (under an "## npm / npx" heading) when integrating.
-->

## npm / npx

If you already have Node.js (>= 18), you can install BharatCode from npm. The npm
package downloads the correct prebuilt binary for your platform from GitHub
Releases — nothing is compiled locally.

Global install (adds `bharatcode` to your PATH):

```bash
npm install -g bharatcode
```

Run once without installing:

```bash
npx bharatcode
```

Supported platforms: macOS (arm64/x64), Linux (arm64/x64), Windows (arm64/x64).

> The npm package version maps to a GitHub release tag (`v<version>`), so the
> binary it downloads always matches the published release.

For air-gapped environments, set `BHARATCODE_SKIP_DOWNLOAD=1` during install and
place the binary in the package's `bin/` directory yourself, or download the
archive directly from the
[Releases page](https://github.com/arbazkhan971/bharatcode/releases).
