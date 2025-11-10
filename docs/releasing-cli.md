# Releasing the peep CLI

This document describes how to publish a new version of the Go-based CLI across GitHub Releases, Homebrew, and npm. Releases are driven by [GoReleaser](https://goreleaser.com/) and a dedicated GitHub Actions workflow.

## Prerequisites

- Go 1.24 or newer
- Node.js 18 or newer
- Docker (only required for local testing inside Minikube)
- A GitHub personal access token with `repo` permissions (`GITHUB_TOKEN`)
- An npm access token with `publish` permissions (`NPM_TOKEN`)
- Access to the `splax-s/homebrew-peep` tap repository

Export the tokens before running GoReleaser locally:

```bash
export GITHUB_TOKEN=ghp_your_token
export NPM_TOKEN=your-npm-token
```

The GitHub Actions workflow reads the same environment variables from repository secrets.

## Versioning workflow

1. Choose the semantic version for the release (for example `v0.4.0`).
2. Create a Git tag on the main branch following the pattern `cli/vX.Y.Z`:

   ```bash
   git tag cli/v0.4.0
   git push origin cli/v0.4.0
   ```

   Pushing the tag triggers the `release-cli` workflow, which runs GoReleaser end-to-end.

3. If you prefer to run the release locally instead of using GitHub Actions:

   ```bash
   goreleaser release --clean --skip-validate
   ```

   The command:

   - Builds archives for macOS (amd64/arm64), Linux (amd64/arm64), and Windows (amd64/arm64)
   - Updates `cmd/peep/npm/package.json` to match the release version
   - Publishes a GitHub Release with checksums
   - Generates a Homebrew formula in the `splax-s/homebrew-peep` tap
   - Publishes the npm shim package (if `NPM_TOKEN` is set)

   After running GoReleaser locally, reset modified workspace files:

   ```bash
   git checkout -- cmd/peep/npm/package.json
   ```

## Homebrew tap

GoReleaser updates the `splax-s/homebrew-peep` tap automatically. The resulting formula installs the single `peep` binary and exposes `peep version` for verification.

To test the formula locally:

```bash
brew tap splax-s/homebrew-peep
brew install peep
peep version
```

## npm package

The npm package is a lightweight shim that downloads the appropriate CLI binary for the host operating system during `npm install`. Publishing is handled by GoReleaser, but you can verify the package manually:

```bash
cd cmd/peep/npm
npm pack
npm install -g ./peep-*.tgz
peep version
```

The package depends on the presence of release assets hosted on GitHub. If you rerun `npm publish`, ensure the corresponding release artifacts still exist.

## Dry runs and troubleshooting

- Run `goreleaser build --snapshot --clean` to validate the configuration without publishing.
- Set `SKIP_NPM=1 goreleaser release --clean` to skip npm publishing while still creating the GitHub Release and Homebrew formula.
- Inspect `.goreleaser.yaml` for additional options, including the changelog filters and archive naming patterns.

If a release fails part-way through, delete the partially created tag and rerun GoReleaser after addressing the underlying issue.
