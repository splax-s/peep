'use strict';

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const https = require('node:https');
const tar = require('tar');

const pkg = require('../package.json');

function mapPlatform(platform) {
  switch (platform) {
    case 'darwin':
      return 'darwin';
    case 'linux':
      return 'linux';
    case 'win32':
      return 'windows';
    default:
      return null;
  }
}

function mapArch(arch) {
  switch (arch) {
    case 'x64':
      return 'amd64';
    case 'arm64':
      return 'arm64';
    default:
      return null;
  }
}

async function download(url, destination) {
  await new Promise((resolve, reject) => {
    const requestUrl = new URL(url);
    const options = {
      hostname: requestUrl.hostname,
      path: `${requestUrl.pathname}${requestUrl.search}`,
      protocol: requestUrl.protocol,
      port: requestUrl.port,
      headers: {
        'User-Agent': 'peep-cli-installer'
      }
    };

    const req = https.get(options, (res) => {
      if (res.statusCode && res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
        download(res.headers.location, destination).then(resolve).catch(reject);
        res.resume();
        return;
      }
      if (res.statusCode !== 200) {
        reject(new Error(`unexpected status code ${res.statusCode} while downloading ${url}`));
        res.resume();
        return;
      }
      const file = fs.createWriteStream(destination);
      res.pipe(file);
      file.on('finish', () => file.close(resolve));
    });

    req.on('error', (err) => {
      reject(err);
    });
  });
}

async function extract(archivePath, targetDir) {
  await tar.x({
    file: archivePath,
    cwd: targetDir,
    strip: 0
  });
}

async function install() {
  const platform = mapPlatform(process.platform);
  if (!platform) {
    console.error(`Unsupported platform: ${process.platform}`);
    process.exit(1);
  }

  const arch = mapArch(process.arch);
  if (!arch) {
    console.error(`Unsupported architecture: ${process.arch}`);
    process.exit(1);
  }

  const version = (pkg.version || '').trim();
  if (!version || version === '0.0.0') {
    console.error('Package version is not set – this build cannot download the CLI binary.');
    process.exit(1);
  }

  const releaseTag = version.startsWith('v') ? version : `v${version}`;
  const archiveName = `peep_${version}_${platform}_${arch}.tar.gz`;
  const downloadUrl = `https://github.com/splax-s/peep/releases/download/${releaseTag}/${archiveName}`;

  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'peep-'));
  const archivePath = path.join(tmpDir, archiveName);
  const distDir = path.join(__dirname, '..', 'dist');

  fs.rmSync(distDir, { recursive: true, force: true });
  fs.mkdirSync(distDir, { recursive: true });

  try {
    console.log(`Downloading peep CLI from ${downloadUrl}`);
    await download(downloadUrl, archivePath);

    await extract(archivePath, distDir);

    const binaryName = platform === 'windows' ? 'peep.exe' : 'peep';
    const binaryPath = path.join(distDir, binaryName);
    if (!fs.existsSync(binaryPath)) {
      throw new Error(`Expected binary ${binaryName} missing after extraction`);
    }
    if (platform !== 'windows') {
      fs.chmodSync(binaryPath, 0o755);
    }
    console.log('peep CLI installed successfully.');
  } catch (err) {
    console.error(`Failed to install peep CLI: ${err.message}`);
    process.exit(1);
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

install();
