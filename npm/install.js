#!/usr/bin/env node
'use strict';

/*
 * BharatCode npm postinstall script.
 *
 * This runs automatically after `npm install -g bharatcode` (or `npx bharatcode`).
 * It downloads the prebuilt binary matching the user's OS/arch from the
 * corresponding GitHub Release and extracts it into ./bin/ next to the launcher
 * shim (bin/bharatcode.js).
 *
 * Design constraints:
 *   - No external dependencies. Only Node built-ins (https, fs, os, path,
 *     child_process) plus the system `tar` (unix) / PowerShell (windows) for
 *     archive extraction. This keeps the dependency tree at zero, which is the
 *     same approach esbuild/Biome-class tools use.
 *   - Fail loudly with an actionable message, and tell the user how to install
 *     manually if the automated download cannot work (e.g. air-gapped CI).
 *
 * Asset naming (from .goreleaser.yaml):
 *   bharatcode_{title Os}_{x86_64|arm64}.{tar.gz|zip}
 *   e.g. bharatcode_Darwin_arm64.tar.gz, bharatcode_Windows_x86_64.zip
 */

const fs = require('fs');
const os = require('os');
const path = require('path');
const https = require('https');
const { execFileSync } = require('child_process');

// ---------------------------------------------------------------------------
// Configuration
// ---------------------------------------------------------------------------

const REPO = 'arbazkhan971/bharatcode';
const pkg = require('./package.json');

// The release tag is derived from the package version. GoReleaser tags releases
// as `v<version>` (e.g. v0.1.0), so the published npm version MUST match a real
// GitHub release tag for the download to succeed.
const VERSION = pkg.version;
const TAG = 'v' + VERSION;

const BIN_DIR = path.join(__dirname, 'bin');
// On Windows the real executable carries a .exe suffix; elsewhere it is bare.
const BIN_NAME = process.platform === 'win32' ? 'bharatcode.exe' : 'bharatcode';
const BIN_PATH = path.join(BIN_DIR, BIN_NAME);

// ---------------------------------------------------------------------------
// Platform -> GitHub asset mapping
// ---------------------------------------------------------------------------

// Maps Node's process.platform to the title-cased GOOS string GoReleaser uses.
const PLATFORM_TO_OS = {
  darwin: 'Darwin',
  linux: 'Linux',
  win32: 'Windows',
};

// Maps Node's process.arch to GoReleaser's arch token. GoReleaser rewrites
// amd64 -> x86_64 and leaves arm64 as-is.
const ARCH_TO_TOKEN = {
  x64: 'x86_64',
  arm64: 'arm64',
};

/**
 * Build the GitHub asset filename for the current platform.
 * Returns { asset, isZip } or throws if the platform is unsupported.
 */
function resolveAsset() {
  const osName = PLATFORM_TO_OS[process.platform];
  const archToken = ARCH_TO_TOKEN[process.arch];

  if (!osName || !archToken) {
    throw new Error(
      `Unsupported platform: ${process.platform}/${process.arch}.\n` +
        `BharatCode ships prebuilt binaries for: ` +
        `darwin (x64, arm64), linux (x64, arm64), win32 (x64, arm64).\n` +
        `If you need this platform, build from source: ` +
        `https://github.com/${REPO}`
    );
  }

  // Windows archives are .zip; everything else is .tar.gz (see .goreleaser.yaml).
  const isZip = process.platform === 'win32';
  const ext = isZip ? 'zip' : 'tar.gz';
  const asset = `bharatcode_${osName}_${archToken}.${ext}`;
  return { asset, isZip };
}

// ---------------------------------------------------------------------------
// Download (follows redirects — GitHub release assets 302 to a CDN)
// ---------------------------------------------------------------------------

/**
 * Download a URL to a local file, following up to `maxRedirects` redirects.
 * Resolves when the file is fully written. Rejects on HTTP errors.
 */
function download(url, dest, maxRedirects = 10) {
  return new Promise((resolve, reject) => {
    const request = https.get(
      url,
      {
        headers: {
          // Some networks/proxies reject requests without a User-Agent.
          'User-Agent': `bharatcode-npm/${VERSION} (node ${process.version})`,
          Accept: 'application/octet-stream',
        },
      },
      (res) => {
        const { statusCode, headers } = res;

        // Follow redirects (GitHub serves assets from a redirected CDN URL).
        if (statusCode >= 300 && statusCode < 400 && headers.location) {
          res.resume(); // discard body so the socket can be reused
          if (maxRedirects <= 0) {
            reject(new Error(`Too many redirects while downloading ${url}`));
            return;
          }
          const next = new URL(headers.location, url).toString();
          resolve(download(next, dest, maxRedirects - 1));
          return;
        }

        if (statusCode !== 200) {
          res.resume();
          reject(
            new Error(
              `Download failed: HTTP ${statusCode} for ${url}\n` +
                `Make sure release ${TAG} exists and publishes that asset:\n` +
                `  https://github.com/${REPO}/releases/tag/${TAG}`
            )
          );
          return;
        }

        const file = fs.createWriteStream(dest);
        res.pipe(file);
        file.on('finish', () => file.close(() => resolve()));
        file.on('error', (err) => {
          fs.rm(dest, { force: true }, () => reject(err));
        });
      }
    );

    request.on('error', reject);
    // Guard against a hung connection.
    request.setTimeout(120000, () => {
      request.destroy(new Error(`Timed out downloading ${url}`));
    });
  });
}

// ---------------------------------------------------------------------------
// Extraction (shells out to system tar / PowerShell — no JS tar dependency)
// ---------------------------------------------------------------------------

/**
 * Extract the bharatcode binary from `archivePath` into BIN_DIR.
 * Uses `tar` on unix and PowerShell's Expand-Archive on Windows.
 */
function extract(archivePath, isZip) {
  if (isZip) {
    // Expand-Archive unpacks the whole zip into BIN_DIR; the binary lands as
    // bharatcode.exe at the archive root (the archive contains no nested dir).
    execFileSync(
      'powershell',
      [
        '-NoProfile',
        '-NonInteractive',
        '-Command',
        // -Force overwrites a stale copy from a previous install.
        `Expand-Archive -LiteralPath '${archivePath}' -DestinationPath '${BIN_DIR}' -Force`,
      ],
      { stdio: 'inherit' }
    );
  } else {
    // GNU/BSD tar both accept -xzf; extract directly into BIN_DIR.
    execFileSync('tar', ['-xzf', archivePath, '-C', BIN_DIR], {
      stdio: 'inherit',
    });
  }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

async function main() {
  // Allow CI/offline installs to skip the network step entirely.
  if (process.env.BHARATCODE_SKIP_DOWNLOAD === '1') {
    console.log('[bharatcode] BHARATCODE_SKIP_DOWNLOAD=1 set; skipping binary download.');
    return;
  }

  const { asset, isZip } = resolveAsset();
  const url = `https://github.com/${REPO}/releases/download/${TAG}/${asset}`;

  fs.mkdirSync(BIN_DIR, { recursive: true });

  // If a usable binary is already present (e.g. reinstall), skip the download.
  if (fs.existsSync(BIN_PATH)) {
    console.log(`[bharatcode] Binary already present at ${BIN_PATH}; skipping download.`);
    return;
  }

  const tmpArchive = path.join(
    os.tmpdir(),
    `bharatcode-${VERSION}-${process.pid}-${asset}`
  );

  console.log(`[bharatcode] Downloading ${asset} (${TAG}) for ${process.platform}/${process.arch}...`);

  try {
    await download(url, tmpArchive);
    console.log('[bharatcode] Extracting binary...');
    extract(tmpArchive, isZip);

    if (!fs.existsSync(BIN_PATH)) {
      throw new Error(
        `Extraction completed but ${BIN_NAME} was not found in ${BIN_DIR}.\n` +
          `The release archive layout may have changed.`
      );
    }

    // Ensure the binary is executable on unix (tar usually preserves the bit,
    // but make it explicit so the shim can spawn it).
    if (process.platform !== 'win32') {
      fs.chmodSync(BIN_PATH, 0o755);
    }

    console.log(`[bharatcode] Installed ${BIN_NAME} -> ${BIN_PATH}`);
  } catch (err) {
    console.error('\n[bharatcode] Installation failed.');
    console.error(err.message || err);
    console.error(
      '\nYou can install manually instead:\n' +
        `  1. Download the right archive from https://github.com/${REPO}/releases/tag/${TAG}\n` +
        `  2. Extract the 'bharatcode' binary onto your PATH.\n` +
        'Or build from source with Go: ' +
        `https://github.com/${REPO}\n`
    );
    process.exit(1);
  } finally {
    fs.rm(tmpArchive, { force: true }, () => {});
  }
}

main();
