#!/usr/bin/env node
'use strict';

/*
 * Launcher shim for the BharatCode CLI.
 *
 * `npm install -g bharatcode` symlinks this file onto the user's PATH as
 * `bharatcode`. It locates the platform binary that install.js downloaded into
 * ./bharatcode (or ./bharatcode.exe on Windows), spawns it with the same argv
 * and stdio, and exits with the binary's exit code.
 */

const fs = require('fs');
const path = require('path');
const { spawnSync } = require('child_process');

// The real executable lives next to this shim, dropped here by install.js.
const binName = process.platform === 'win32' ? 'bharatcode.exe' : 'bharatcode';
const binPath = path.join(__dirname, binName);

if (!fs.existsSync(binPath)) {
  console.error(
    `[bharatcode] The native binary is missing (expected at ${binPath}).\n` +
      'The postinstall download may have failed. Try reinstalling:\n' +
      '  npm install -g bharatcode\n' +
      'or run the installer directly:\n' +
      '  node ' + path.join(__dirname, '..', 'install.js') + '\n'
  );
  process.exit(1);
}

// Forward every argument after `node <shim>` and inherit stdio so the TUI,
// prompts, and pipes all work transparently.
const result = spawnSync(binPath, process.argv.slice(2), { stdio: 'inherit' });

// Propagate signal-based termination as a conventional 128+signal code.
if (result.signal) {
  const signals = { SIGINT: 2, SIGTERM: 15, SIGHUP: 1, SIGQUIT: 3 };
  process.exit(128 + (signals[result.signal] || 0));
}

// spawnSync sets .error (e.g. ENOENT/EACCES) if the binary couldn't run.
if (result.error) {
  console.error(`[bharatcode] Failed to launch binary: ${result.error.message}`);
  process.exit(1);
}

process.exit(result.status === null ? 1 : result.status);
