# gh-pyra

A [GitHub CLI](https://cli.github.com/) extension (`gh pyra`): a swiss army knife for pull requests.

*Pyra* is "potato" in the Poznań dialect of Polish.

Currently it helps review large pull requests by marking noisy files as viewed, so they collapse in the review UI and stay out of the way.

## Requirements

- [GitHub CLI](https://cli.github.com/) (`gh`), authenticated (`gh auth login`)
- A pull request to work on: the PR of the current branch (as `gh pr view` resolves it), or one selected with `--pr`

## Installation

```bash
gh extension install harnyk/gh-pyra
```

`gh` downloads the precompiled binary for your platform from the latest GitHub release.

Upgrade / remove:

```bash
gh extension upgrade gh-pyra
gh extension remove gh-pyra
```

### From a local clone

Requires Go (see `go.mod` for the version).

```bash
cd /path/to/gh-pyra
go build -o gh-pyra .
gh extension install .
```

Rebuild with `go build -o gh-pyra .` after pulling changes.

## Commands

| Command | Description |
|---------|-------------|
| `gh pyra hide FILTER...` | Mark matching files as viewed |
| `gh pyra show FILTER...` | Unmark matching files as viewed (alias: `unhide`) |
| `gh pyra ls [FILTER...]` | List files with their viewed state (alias: `list`) |
| `gh pyra count` | Count viewed and not viewed files |
| `gh pyra presets` | List available filter presets |

Run `gh pyra <command> --help` for details.

### Selecting a pull request

By default commands operate on the pull request of the current branch. Global flags select another one, the same way `gh pr view` does:

| Flag | Description |
|------|-------------|
| `--pr PR` | Pull request number, URL or branch name |
| `-R`, `--repo [HOST/]OWNER/REPO` | Repository of the pull request (requires `--pr`) |

```bash
# PR #123 of the current repository
gh pyra hide --pr 123 'pnpm-lock.yaml'

# PR in another repository
gh pyra count -R octo/widgets --pr 45

# by URL (also works for GitHub Enterprise hosts)
gh pyra show --all --pr https://github.com/octo/widgets/pull/45
```

### Hide files

By default a filter is a plain **substring** of the file path (not a regex). A file is processed when it matches **any** of the filters (see [Filters](#filters) for excludes, globs and presets).

```bash
# anything with .spec. in the path
gh pyra hide '.spec.'

# lockfiles and everything under generated/
gh pyra hide 'pnpm-lock.yaml' 'generated/'

# every file in the PR
gh pyra hide --all
```

### Show files again

`show` takes the same filters and flags as `hide`:

```bash
gh pyra show '.spec.'
gh pyra show --all
```

### List files

```bash
gh pyra ls                    # every file
gh pyra ls -s changed         # files changed since you viewed them
gh pyra ls -s unviewed '.go'  # filters work as in hide/show
```

```text
viewed    +12  -3   src/api/client.ts
changed   +4   -1   src/api/server.ts
unviewed  +80  -0   src/ui/widget.tsx
```

States: `unviewed`, `viewed`, and `changed` (marked as viewed, but the file changed since then). `--state`/`-s` can be repeated.

Use `ls` with the same filters to preview what `hide` or `show` would touch.

### Flags of `hide` and `show`

| Flag | Description |
|------|-------------|
| `-w`, `--wildcard` | Treat patterns as globs |
| `-x`, `--exclude PATTERN` | Exclude matching files (repeatable) |
| `-a`, `--all` | Process every file (only excludes allowed) |
| `-n`, `--dry-run` | Show what would change without calling the API |
| `-j`, `--concurrency N` | Files updated in parallel (default 8) |

### Count viewed files

```bash
gh pyra count
```

```text
visible: 12
hidden : 45
total  : 57
```

## Filters

`hide`, `show` and `ls` take the same filters:

| Filter | Meaning |
|--------|---------|
| `PATTERN` | include files matching `PATTERN` |
| `!PATTERN` | exclude files matching `PATTERN` (same as `--exclude`/`-x PATTERN`) |
| `@NAME` | include the patterns of [preset](#presets) `NAME` |

A file is selected when it matches **any include and no exclude**. With only excludes, every other file is selected. Start a filter with `\!` or `\@` to match a literal `!` or `@`.

### Excludes

```bash
# everything under src/ except src/core/
gh pyra hide 'src/' '!src/core/'

# every file except src/core/ (two equivalent forms)
gh pyra hide '!src/core/'
gh pyra hide --all -x 'src/core/'
```

Quote `!` filters: in interactive shells an unquoted `!` triggers history expansion.

### Wildcard (glob) filters

With `--wildcard` (`-w`) every filter is a glob pattern matched against the whole path:

| Pattern | Matches |
|---------|---------|
| `*` | any characters except `/` |
| `**` | any characters including `/` (whole path segments) |
| `?` | one character except `/` |
| `[abc]`, `[a-z]`, `[!abc]` | one character from (or not from) the set |
| `{a,b}` | either alternative |

A pattern without `/` is also matched against the file name alone, so it works in any directory.

```bash
# *.spec.ts files in any directory
gh pyra hide -w '*.spec.ts'

# everything under generated/ at the repository root
gh pyra hide -w 'generated/**'

# YAML anywhere under src/, plus snapshot files anywhere
gh pyra hide -w 'src/**/*.{yaml,yml}' '*.snap'
```

Quote patterns so the shell does not expand them. Without `--wildcard`, characters like `*` and `[` are matched literally. `--wildcard` applies to `!` and `--exclude` patterns too.

### Presets

A preset is a named list of glob patterns (always globs, regardless of `--wildcard`), referenced as `@NAME`:

```bash
gh pyra hide @lockfiles @snapshots
gh pyra presets   # list available presets and where they come from
```

Presets come from three sources; later ones override earlier ones with the same name:

1. **Built-in**: `@lockfiles` (lockfiles of common package managers) and `@snapshots` (`*.snap`, `**/__snapshots__/**`).
2. **Repository**: `.github/pyra.yml` on the **base branch** of the pull request, so a PR cannot change the presets it is reviewed with. Works with `--pr`/`--repo` without a local checkout.
3. **User**: `~/.config/gh-pyra/config.yml` (the OS user config directory), or the file in `$GH_PYRA_CONFIG`.

Format:

```yaml
presets:
  noise:
    - pnpm-lock.yaml
    - '**/*.snap'
    - 'generated/**'
    - '!generated/schema.graphql'   # excludes work inside presets too
```

Excludes from presets apply to the whole selection.

## Behavior

- `hide` skips files already marked viewed; `show` skips files not marked viewed.
- Progress messages go to stderr and may appear in any order because files are processed concurrently.
- If a request fails, remaining requests are cancelled and the command exits with code 1.
- Invalid arguments → exit code 2; `gh`/API errors → exit code 1.

## Migrating from gh-hide

| gh-hide | gh-pyra |
|---------|---------|
| `gh hide FILTER` | `gh pyra hide FILTER` |
| `gh hide -u FILTER` | `gh pyra show FILTER` |
| `gh hide --all` | `gh pyra hide --all` |
| `gh hide -c` | `gh pyra count` |

## Development and tests

Tests use a fake GitHub client; no live API calls:

```bash
go test -race ./...
```

## Releasing

Releases are built by [GoReleaser](https://goreleaser.com/) in GitHub Actions when a `v*` tag is pushed:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Check the configuration and build locally without publishing:

```bash
goreleaser check
goreleaser release --snapshot --clean --skip=publish
```
