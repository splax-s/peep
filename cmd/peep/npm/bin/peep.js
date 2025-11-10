#!/usr/bin/env node

const { spawn } = require('node:child_process');
const path = require('node:path');
const fs = require('node:fs');

const binaryDir = path.join(__dirname, '..', 'dist');
const binaryName = process.platform === 'win32' ? 'peep.exe' : 'peep';
const binaryPath = path.join(binaryDir, binaryName);

if (!fs.existsSync(binaryPath)) {
  console.error('peep binary not found – reinstall the package or open an issue at https://github.com/splax-s/peep/issues');
  process.exit(1);
}

const child = spawn(binaryPath, process.argv.slice(2), {
  stdio: 'inherit'
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code);
});

child.on('error', (err) => {
  console.error('failed to start peep CLI:', err);
  process.exit(1);
});
