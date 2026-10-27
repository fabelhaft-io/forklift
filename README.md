# forklift

```text
       |   ________
       |  |        |             __            _     _  _  __ _
       |  |   o    |            / _| ___  _ __| | __| |(_)/ _| |_
 _____ |  |  /|\   |_______    | |_ / _ \| '__| |/ /| || | |_| __|
|     ||==|  / \   |  ___  |    |  _| (_) | |  |   < | || |  _| |_
|_____||  |________|_|___|_|    |_|  \___/|_|  |_|\_\|_||_|_|  \__|
=======|   (O)         (O)
```
I like to fork any good repository I find - to keep it safe and protected should the original vanish.
Since this leads to hundreds of repositories I needed an easy way to keep 'em up to date. 

Together with https://github.com/fabelhaft-io/flotsam good ideas will never just disappear.


**Keep your GitHub forks up to date, all of them, with one command.**

forklift is a small open-source tool, written in Go, that brings every fork you own up to
date with its upstream repository, and the forks of any organizations you name. It does
what the "Sync fork" button on GitHub does, for all those forks at once, and skips the ones
you list in a config file.

> **Status: early development.** The commands and config may change before the first
> release.

## How it works

1. forklift lists the repositories you own, and those of the organizations in your config
   file, and keeps the forks.
2. It skips the forks your config file leaves out, and archived forks, which are read-only.
3. For every other fork, it asks GitHub to merge the upstream changes into the fork's
   default branch through the
   [merge-upstream API](https://docs.github.com/en/rest/branches/branches#sync-a-fork-branch-with-the-upstream-repository).

Everything happens on GitHub's side: forklift never clones a repository and doesn't need
`git`. If the fork has commits of its own, GitHub creates a merge commit; if the branches
conflict, nothing changes and forklift reports the fork as failed.

## Usage

```sh
go install github.com/fabelhaft-io/forklift/cmd/forklift@latest

export FORKLIFT_TOKEN=github_pat_...

forklift sync -dry-run              # show what would be synced, change nothing
forklift sync                       # sync every fork you own
forklift sync -c forklift.json      # also sync organization forks, skip excluded ones
```

Each fork gets one line as soon as it is done:

```
synced      octocat/hello-world  main (fast-forward)
up-to-date  octocat/spoon-knife  main
failed      octocat/linux  409 Conflict: There are merge conflicts
excluded    octocat/my-patched-tool  matches exclude pattern "octocat/my-patched-tool"
archived    octocat/old-experiment
```

| Status       | Meaning                                                              |
|--------------|----------------------------------------------------------------------|
| `synced`     | upstream changes were merged (`fast-forward` or `merge`)             |
| `up-to-date` | the fork already had every upstream commit                           |
| `would-sync` | `-dry-run` only: this fork would be synced                           |
| `excluded`   | the include or exclude patterns in the config file leave it out      |
| `archived`   | the fork is archived and therefore read-only                         |
| `failed`     | GitHub refused the sync; the line shows GitHub's reason              |

The exit code is `0` when every fork was synced or skipped, `1` when any fork failed or the
config or token is invalid, and `2` when the command line is wrong. A scheduled run that
leaves a fork behind therefore never looks like a success.

## Token

forklift reads a GitHub token from the `FORKLIFT_TOKEN` environment variable. It
deliberately doesn't use `GITHUB_TOKEN`: GitHub Actions sets that variable to a token that
can only access the workflow's own repository.

- **Fine-grained token** (recommended): give it access to the forks to sync, with
  **Contents: Read and write** and **Workflows: Read and write**.
- **Classic token**: the `repo` scope (or `public_repo` if all your forks are public) and the
  `workflow` scope.

The workflow permission is needed because GitHub refuses a sync that changes files under
`.github/workflows/` without it, and upstream repositories change their workflows often.

To sync organization forks, the token also needs write access to them. A fine-grained token
belongs to a single owner, either your account or one organization, so to sync your own
forks and an organization's in one run, use a classic token.

## Configuration

The config file lists the organizations whose forks forklift syncs too, and which forks to
sync or leave alone. Copy [forklift.example.json](forklift.example.json) to `forklift.json`
and pass it with `-c`:

```json
{
  "organizations": ["octo-org"],
  "exclude": [
    "octocat/my-patched-tool",
    "octocat/linux-*",
    "octo-org/vendored-*"
  ]
}
```

### organizations

Your own forks are always synced. Organization forks are only synced for the organizations
listed here, because they are often maintained by other people, sometimes with changes of
their own on purpose. Write the plain organization name, as it appears in
`https://github.com/<name>`.

All forks are listed before the first one is synced, so an organization that doesn't exist
or that the token can't see stops the run before anything has changed.

### include and exclude

Two lists of patterns decide which forks are synced, so you can work from an allowlist, a
blocklist or both:

- **`include`**: if it has patterns, only forks that match one of them are synced. Leave it
  out or empty to sync every fork.
- **`exclude`**: forks that match one of these are never synced. Exclude a fork when you
  maintain changes in it that shouldn't be mixed with upstream, when its upstream is gone,
  or when it fails on every run for a reason you accept.

`exclude` always wins, so you can include broadly and carve out exceptions:

```json
{
  "organizations": ["octo-org"],
  "include": ["octo-org/*"],
  "exclude": ["octo-org/legacy-*"]
}
```

This syncs every fork of octo-org except the `legacy-` ones, and none of your own forks.
Patterns only filter the forks forklift lists: an organization's forks still need the
organization in `organizations`.

Each pattern has the form `owner/name` and uses
[Go's `path.Match` syntax](https://pkg.go.dev/path#Match): `*` matches any part of a name,
`?` one character and `[...]` a set of characters. `*` never matches the `/`, so
`octocat/*` matches all of octocat's forks. Matching ignores case.

The file is checked before anything is synced: an unknown field, an organization that isn't
a plain name, or a pattern without an owner is an error. A pattern that silently matches
nothing could let forklift sync the fork you meant to protect.

The token never goes in the config file.

## Running on a schedule

### With cron

```cron
0 6 * * *  FORKLIFT_TOKEN=github_pat_... /usr/local/bin/forklift sync -c /etc/forklift.json
```

### With GitHub Actions

GitHub Actions can run forklift for you, with no machine of your own. The config file and
the workflow live in a small repository of their own:

```
sync-my-forks/
├── forklift.json
└── .github/
    └── workflows/
        └── sync-forks.yml
```

1. Create a **private** repository, for example `sync-my-forks`. Private keeps your list of
   forks to yourself, and GitHub disables scheduled workflows in public repositories that
   have had no activity for 60 days.
2. Add your config as `forklift.json` at the root (see [Configuration](#configuration)). If
   you just want every fork synced, `{}` is enough.
3. Create a token as described under [Token](#token), and store it in the repository under
   **Settings → Secrets and variables → Actions** as a secret named `FORKLIFT_TOKEN`.
4. Add the workflow as `.github/workflows/sync-forks.yml`:

   ```yaml
   name: Sync forks

   on:
     schedule:
       - cron: "0 6 * * *"   # every day at 06:00 UTC
     workflow_dispatch:      # adds a "Run workflow" button for manual runs

   # forklift uses FORKLIFT_TOKEN, not the workflow's own token, which only needs
   # to read this repository to check out forklift.json.
   permissions:
     contents: read

   jobs:
     sync:
       runs-on: ubuntu-latest
       steps:
         - uses: actions/checkout@v7
         - uses: actions/setup-go@v7
           with:
             go-version: stable
         - run: go run github.com/fabelhaft-io/forklift/cmd/forklift@latest sync -c forklift.json
           env:
             FORKLIFT_TOKEN: ${{ secrets.FORKLIFT_TOKEN }}
   ```

5. Open the **Actions** tab, select **Sync forks** and click **Run workflow** to try it
   once. Change the step to `sync -dry-run -c forklift.json` first if you want to see what it
   would do without changing anything.

Each run's log lists every fork with its status. If any fork fails, the run is marked as
failed, and GitHub emails you about it by default.

## Limitations

- Organization forks are only synced for the organizations in the config file.
- Only the default branch of each fork is synced, like the "Sync fork" button does.
- Only github.com is supported, not GitHub Enterprise Server.
- Forks are synced one after another, without retries. If GitHub rate-limits a run, the
  affected forks are reported as failed and synced on the next run.

## Contributing

Issues and pull requests are welcome. [CLAUDE.md](CLAUDE.md) describes the design and the
code conventions; it is written for Claude Code but applies to every contributor. Before you
push, make sure these pass:

```sh
go vet ./...
go test ./...
```

## License

[Apache 2.0](LICENSE)
