'use strict';

const fs = require('node:fs');
const path = require('node:path');

const versionArg = process.argv[2];

if (!versionArg) {
  console.error('Version argument is required.');
  process.exit(1);
}

const normalized = versionArg.startsWith('v') ? versionArg.slice(1) : versionArg;
if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(normalized)) {
  console.error(`Invalid semantic version: ${versionArg}`);
  process.exit(1);
}

const pkgPath = path.join(__dirname, '..', 'package.json');
const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));

pkg.version = normalized;

fs.writeFileSync(pkgPath, `${JSON.stringify(pkg, null, 2)}\n`);
