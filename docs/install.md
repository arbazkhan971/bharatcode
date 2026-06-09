# Installing BharatCode

Pick whichever fits your setup. All methods install the same `bharatcode`
binary; none require a Go toolchain except "From source".

## Homebrew (macOS / Linux)

```sh
brew install arbazkhan971/tap/bharatcode
```

This pulls the formula from the [`arbazkhan971/homebrew-tap`](https://github.com/arbazkhan971/homebrew-tap)
repository (Homebrew expands `arbazkhan971/tap` to `arbazkhan971/homebrew-tap`)
and installs `bharatcode` onto your `PATH`. Upgrade later with:

```sh
brew upgrade bharatcode
```

## npm / npx

```sh
npm install -g bharatcode-cli      # global install
# or run without installing:
npx bharatcode-cli
```

The npm package downloads the prebuilt binary for your platform on install.
See [`npm/README.md`](../npm/README.md) for details.

## Install script — macOS / Linux (curl)

```sh
curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh | sh
```

Installs the latest release binary to `~/.local/bin`. Override with environment
variables:

```sh
# specific version / directory
curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh \
  | BHARATCODE_VERSION=v0.2.0 BHARATCODE_INSTALL_DIR=/usr/local/bin sh
```

## Install script — Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.ps1 | iex
```

Installs `bharatcode.exe` to `%LOCALAPPDATA%\Programs\bharatcode` and adds it to
your user `PATH`. Set `$env:BHARATCODE_VERSION` first to pin a version.

## go install

If you have Go 1.24+:

```sh
go install github.com/arbazkhan971/bharatcode@latest
```

Installs to `$(go env GOPATH)/bin`. Note: this path does not stamp the build
version, so `bharatcode version` and `bharatcode update` report an unknown
commit. Use Homebrew, the install scripts, or a source build for accurate
version reporting.

## From source

```sh
git clone https://github.com/arbazkhan971/bharatcode.git
cd bharatcode
make build        # stamps version + commit into the binary
./bin/bharatcode version
```

## Upgrading

If you installed with a package manager, prefer its own upgrade path
(`brew upgrade bharatcode`, `npm install -g bharatcode-cli`, re-running the install
script, etc.). Otherwise BharatCode can update itself in place:

```sh
bharatcode update            # check only: reports whether a newer build exists
bharatcode update --apply    # download the latest release and replace this binary
```

`--apply` downloads the release archive for your platform, verifies it against
the release's published SHA-256 `checksums.txt` (a checksum mismatch or a
missing manifest is a hard failure — it never installs unverified bytes), then
atomically swaps the new binary over the running one. Restart `bharatcode`
afterwards to run the new version. Both `update` and `update --apply` are
disabled in offline mode, which forbids the network access they require.

To have BharatCode check on startup and self-update automatically, opt in via
config:

```json
{
  "options": {
    "auto_update": true
  }
}
```

With `auto_update` enabled, an interactive launch does a brief, best-effort
check and, if a newer release exists, installs it in the background and prints a
one-line notice; the update takes effect the next time you start BharatCode. The
check is skipped in offline mode and for unstamped builds (such as
`go install`), and any failure is a non-fatal warning that never blocks startup.

## Verify

```sh
bharatcode version
bharatcode doctor   # checks runtime, config, provider API keys, and ChatGPT sign-in
```

`bharatcode doctor` reports each check as `[OK]` or `[WARN]` (warnings are
non-fatal and never block startup). When a
ChatGPT provider is enabled in your config it also shows the **ChatGPT
subscription** line — `signed in as <account> on the <plan> plan`, or a warning
telling you to run `bharatcode auth chatgpt` if you are not yet signed in. See
[Signing in with ChatGPT](#signing-in-with-chatgpt-experimental) below.

## Signing in with ChatGPT (experimental)

If you have a ChatGPT subscription you can use it in place of an API key. This is
experimental, relies on undocumented endpoints, and is intended for personal
single-account use only:

```sh
bharatcode auth chatgpt            # OAuth (PKCE) sign-in; opens your browser
bharatcode auth chatgpt --status   # show the signed-in account, plan, and token state
bharatcode auth chatgpt --logout   # remove the stored credentials
```

## Headless / CI runs

`BHARATCODE_HEADLESS=1` forces the interactive TUI onto a quiet, non-rendering
path so PTY captures and CI logs do not accumulate live-redraw noise. BharatCode
also picks this path automatically when `CI` is set or the terminal is `dumb`:

```sh
BHARATCODE_HEADLESS=1 bharatcode   # quiet, non-rendering TUI for PTY/CI captures
```

The variable only gates the interactive TUI's renderer; `bharatcode run` is
already non-interactive (it never starts a renderer) and ends with a `Changed
files:` block listing every path it created, modified, or deleted during the run,
regardless of `BHARATCODE_HEADLESS`.
