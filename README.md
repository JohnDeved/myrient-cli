# myrient-cli

Fast CLI + TUI client for browsing, searching, indexing, and downloading from Myrient.

## Install

### Option 1: `go install` (recommended)

```bash
go install github.com/JohnDeved/myrient-cli/cmd/myrient-cli@latest
```

This installs `myrient-cli` into your Go bin directory (`$(go env GOPATH)/bin` by default).

If `myrient-cli` is not found after install, add Go bin to your PATH:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

### Option 2: Build locally

```bash
git clone https://github.com/JohnDeved/myrient-cli.git
cd myrient-cli
make install
```

## Quick start

```bash
myrient-cli ls "/No-Intro" --limit 10
myrient-cli find "Chrono Trigger" --prefer-region eu --prefer-language de,en
myrient-cli download "Chrono Trigger" --prefer-region eu --prefer-language de,en
```

For interactive browsing, run:

```bash
myrient-cli
```

## Useful commands

- `myrient-cli ls <path> [--json] [--name-only] [--limit N]`
- `myrient-cli browse <path> [--plain|--json] [--name-only] [--limit N]`
- `myrient-cli find <query> [--search-path <path>] [--prefer-region eu] [--prefer-language de,en]`
- `myrient-cli download <url-or-query> [--search-path <path>] [--prefer-region eu] [--prefer-language de,en]`
- `myrient-cli index [--force] [--workers N]`
- `myrient-cli search <query> [--collection <name>] [--limit N] [--json]`
- `myrient-cli stats [--json]`

## Development

```bash
go test ./...
```
