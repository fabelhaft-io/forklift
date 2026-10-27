# CLAUDE.md

Guidance for Claude Code (and human contributors) working in this repository.

## Project

forklift brings every GitHub fork a user owns up to date with its upstream repository,
plus the forks of the organizations named in a config file. Include and exclude patterns in
the config file limit which forks are synced. It is a public, community-driven Go project,
so code should be easy for an outsider to read.

The project is in early development. Check what actually exists before you rely on it, and
update this file when the design changes.

## Simplicity first

forklift is a small tool and should stay one. Keeping the code as simple as possible matters
more than anything else in this repository; a contributor should be able to read all of it
in one sitting.

- Build what is needed now, not what might be needed later. No options, flags or config
  fields nobody uses yet; add them together with their first user.
- One path through the code. A dry run goes through the same loop as a real run and only
  stops short of the sync request.
- Prefer removing code to adding it. When a check catches almost nothing but costs code and
  edge cases, drop it.

## Basics

- Module: `github.com/fabelhaft-io/forklift`
- License: Apache 2.0
- Go: use the version in `go.mod`

## Commands

```sh
go build ./...
go test ./...
go test -race ./...              # needs cgo; not available on this Windows machine, CI runs it
go vet ./...
go mod tidy -diff                # must print nothing
golangci-lint run                # config in .golangci.yml, includes gofmt/goimports
go run ./cmd/forklift sync -dry-run
```

golangci-lint isn't installed locally; run it with
`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run`, the version CI
pins in `.github/workflows/ci.yml`.

## Layout

```
cmd/forklift/          CLI: main.go plus one command_<name>.go per command
internal/config/       the JSON config file: organizations, include and exclude patterns
internal/github/       minimal GitHub REST client: list user and organization forks, merge-upstream
forklift.example.json  example config, referenced from the README
```

There is no `pkg/`: forklift is a command, not a library, so everything else stays in
`internal/`.

## Design rules

- **Sync through GitHub, never through git.** forklift calls the merge-upstream endpoint
  (the "Sync fork" button). It never clones, fetches or pushes, so it needs no disk space,
  no `git` and no SSH keys.
- **Only the default branch.** That is what the "Sync fork" button does, and other branches
  of a fork are usually someone's own work.
- **Organization forks are opt-in.** The user's own forks are always synced; an
  organization's forks only when the organization is listed in `organizations`. They are
  often maintained by other people on purpose, and a sync can't be undone.
- **List everything before syncing anything.** All forks, the user's and every
  organization's, are listed first, so a misspelled organization stops the run before any
  fork has changed.
- **Excluding is safe, syncing is not.** A sync can't be undone by forklift, so anything
  that could protect the wrong fork is an error before the run starts: unknown config
  fields, patterns without an owner, malformed patterns. All config problems are reported at
  once. For the same reason `exclude` always wins over `include`.
- **Skip what can never succeed.** Archived forks are read-only, so they are reported as
  `archived` instead of failing every run. Anything else GitHub refuses is reported as
  `failed`, and the user decides whether to exclude it.
- **One fork's failure doesn't stop the run.** Print the failure and continue; the exit
  code becomes 1 at the end. Only errors that affect every fork (bad token, listing fails,
  cancelled context) stop the run.
- **The exit code tells the truth.** `0` only when every fork was synced, up to date or
  deliberately skipped; `1` for any failure; `2` for a wrong command line. Scheduled runs
  only see the exit code.
- **Output is one line per fork, printed as soon as it is done**, starting with a fixed
  status word (`synced`, `up-to-date`, `would-sync`, `excluded`, `archived`, `failed`) so
  scripts can grep it. Results go to stdout, errors to stderr.
- **The token comes from `FORKLIFT_TOKEN` only**, never from the config file, a flag or
  `GITHUB_TOKEN` (which GitHub Actions sets to a token that can't sync forks).
- **The CLI is testable without a binary.** `cli` holds the output writers, the environment
  lookup, the API URL and the HTTP client; tests build one pointing at an `httptest` server
  and call `run` directly.

## Conventions

- Spell names out; abbreviations should be rare. Write `configuration`, `repository`,
  `request`, `response` and `testCase`, not `cfg`, `repo`, `req`, `resp` and `tt`. Short
  forms every Go developer expects are fine: standard library package names, `ctx`, `err`,
  `ok`, well-known acronyms (`URL`, `HTTP`, `JSON`, `API`, `ID`) and the usual layout
  directories (`cmd/`, `internal/`).
- No single-letter names, not even where Go code often uses them:
  - Test parameters are `test *testing.T`, not `t`.
  - Receivers are full words: `func (client *Client)`, `func (command *cli)`.
  - Loop variables are named after what they hold: `fork`, `pattern`, `testCase`.
- Document the why, not only the what. Contributors arrive without context, so:
  - Every package doc says why the package exists and what problem it solves.
  - Every type and function has a doc comment, including unexported ones and test helpers.
  - Every decision a reader might question gets a comment at that spot: why this default,
    why this check, why it's rejected rather than repaired. Don't comment what the code
    already says.
- Use the standard library. forklift has no dependencies, and a new one needs a very good
  reason; the GitHub API surface is three endpoints and doesn't justify an SDK.
- Pass `context.Context` as the first argument to anything that does I/O.
- Wrap errors with context: `fmt.Errorf("list repositories: %w", err)`. Don't log and also
  return the same error. Error messages start lower case.
- **Never print the token**: not in output, error messages or test fixtures. Tests check
  that the fake token doesn't appear in the output.
- Only follow pagination links that point at the configured API URL; the token goes with
  every request.
- When writing to stdout/stderr, discard the error explicitly (`_, _ = fmt.Fprintln(...)`).
- Every `//nolint` names the linter and explains why (`//nolint:gosec // reason`).

## Testing

- Tests never call the real GitHub API. Use `httptest` servers that return the responses
  GitHub documents; keep fixtures free of real tokens, emails and personal data.
- Test failures and edge cases, not only the happy path: conflicts, rejected tokens,
  pagination, foreign pagination links, bad config.
- Write table-driven tests where they fit.
