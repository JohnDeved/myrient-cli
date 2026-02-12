# myrient

Fast CLI + TUI client for browsing, searching, indexing, and downloading from Myrient.

## Install

### Option 1: `go install` (recommended)

```bash
go install github.com/johannberger/myrient/cmd/myrient@latest
```

This installs `myrient` into your Go bin directory (`$(go env GOPATH)/bin` by default).

### Option 2: Build locally

```bash
git clone https://github.com/johannberger/myrient.git
cd myrient
make install
```

## Quick start

```bash
myrient ls "/No-Intro" --limit 10
myrient find "Chrono Trigger" --prefer-region eu --prefer-language de,en
myrient download "Chrono Trigger" --prefer-region eu --prefer-language de,en
```

For interactive browsing, run:

```bash
myrient
```

## Useful commands

- `myrient ls <path> [--json] [--name-only] [--limit N]`
- `myrient browse <path> [--plain|--json] [--name-only] [--limit N]`
- `myrient find <query> [--search-path <path>] [--prefer-region eu] [--prefer-language de,en]`
- `myrient download <url-or-query> [--search-path <path>] [--prefer-region eu] [--prefer-language de,en]`
- `myrient index [--force] [--workers N]`
- `myrient search <query> [--collection <name>] [--limit N] [--json]`
- `myrient stats [--json]`

## Development

```bash
go test ./...
```
