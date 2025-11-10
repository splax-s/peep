# peep npm shim

The npm package delivers a thin wrapper around the native peep CLI. During installation the package downloads the latest release binary that matches your operating system and CPU architecture.

## Usage

```bash
npm install -g peep
peep version
```

## How it works

1. `scripts/update-version.js` updates `package.json` to match the release tag when GoReleaser runs.
2. The `postinstall` script downloads the tarball hosted on the GitHub release and extracts the `peep` binary into `dist/`.
3. `bin/peep.js` proxies command-line arguments to the extracted binary.

If the download fails (for example because GitHub is unreachable), the installation exits with a non-zero status. Re-run `npm install -g peep` once the issue is resolved.

## Development

Install dependencies locally:

```bash
npm install
```

Verify the installer script without publishing:

```bash
npm pack
npm install -g ./peep-*.tgz
peep version
```

Remove the global install afterwards with `npm uninstall -g peep`.
