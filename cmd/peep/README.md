# peep CLI

The peep CLI provides authenticated access to projects, deployments, and other platform features from the terminal.

## Installation

### Homebrew (macOS and Linux)

```bash
brew tap splax-s/peep
brew install peep
```

### npm (macOS, Linux, Windows)

```bash
npm install -g peep
```

### Manual download

Prebuilt archives for Linux, macOS, and Windows are published with each release:

1. Download the archive that matches your operating system and CPU architecture from the [GitHub Releases page](https://github.com/splax-s/peep/releases).
2. Extract the archive and place the `peep` binary somewhere on your `$PATH` (`peep.exe` on Windows).
3. Run `peep version` to confirm the binary is available.

## Quick start

```bash
peep login --email you@example.com
peep project list --team <team-id>
peep deploy trigger --project <project-id>
```

Use `peep help` to print the built-in usage summary.

## Configuration file

The CLI stores credentials in `~/.config/peep/config.json` (or the equivalent `os.UserConfigDir()` for your platform). The file includes the API base URL and the last access token issued via the `peep login` command.

## Reporting issues

File issues or feature requests in the main repository: <https://github.com/splax-s/peep/issues>.
