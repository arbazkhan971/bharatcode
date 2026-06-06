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
npm install -g bharatcode      # global install
# or run without installing:
npx bharatcode
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

## Verify

```sh
bharatcode version
bharatcode doctor   # checks runtime, config, and provider API keys
```
