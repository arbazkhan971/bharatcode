# bharatcode

> **OpenCode for India** — a Go-native, MIT-licensed, open-weight-first CLI coding agent. Your code stays in India.

This is the npm distribution of [BharatCode](https://github.com/arbazkhan971/bharatcode). Installing it downloads the prebuilt native binary for your platform from GitHub Releases — there is no compilation step.

## Install

Global install (adds `bharatcode` to your PATH):

```bash
npm install -g @arbazkhan971/bharatcode
```

Or run it once without installing:

```bash
npx @arbazkhan971/bharatcode
```

Then:

```bash
bharatcode --help
bharatcode version
```

## How it works

On install, a `postinstall` script (`install.js`) detects your operating system and CPU architecture, downloads the matching release archive from `https://github.com/arbazkhan971/bharatcode/releases`, and extracts the `bharatcode` binary. The `bharatcode` command is a thin Node shim that execs that binary, passing through all arguments and exit codes.

Supported platforms:

| OS      | Architectures |
| ------- | ------------- |
| macOS   | arm64, x64    |
| Linux   | arm64, x64    |
| Windows | arm64, x64    |

## Environment variables

- `BHARATCODE_SKIP_DOWNLOAD=1` — skip the binary download during `npm install` (useful for air-gapped CI; you must place the binary in `bin/` yourself).

## Troubleshooting

If the postinstall download fails (e.g. no network during install), you can re-run it:

```bash
node node_modules/bharatcode/install.js
```

Or download the archive for your platform manually from the [Releases page](https://github.com/arbazkhan971/bharatcode/releases) and extract the `bharatcode` binary onto your PATH.

## License

MIT — see the [main repository](https://github.com/arbazkhan971/bharatcode).
