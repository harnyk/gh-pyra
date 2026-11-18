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
| `gh pyra count` | Count viewed and not viewed files |

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

By default a filter is a plain **substring** of the file path (not a regex). A file is processed when it matches **any** of the filters.

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

Quote patterns so the shell does not expand them. Without `--wildcard`, characters like `*` and `[` are matched literally.

### Flags of `hide` and `show`

| Flag | Description |
|------|-------------|
| `-w`, `--wildcard` | Treat filters as glob patterns |
| `-a`, `--all` | Process every file (no filters allowed) |
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
