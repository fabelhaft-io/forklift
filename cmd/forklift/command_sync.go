package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/fabelhaft-io/forklift/internal/config"
	"github.com/fabelhaft-io/forklift/internal/github"
)

const syncSummary = `Usage:
  forklift sync [-c <config.json>] [-dry-run]

Bring the default branch of every fork you own, and of every fork of the
organizations in the config file, up to date with its upstream repository.
The include and exclude patterns in the config file limit which forks are
synced.

`

// Statuses printed for each fork. They are fixed words, so scripts can grep
// for them.
const (
	statusSynced    = "synced"
	statusUpToDate  = "up-to-date"
	statusWouldSync = "would-sync"
	statusExcluded  = "excluded"
	statusArchived  = "archived"
	statusFailed    = "failed"
)

// sync runs `forklift sync`.
func (command *cli) sync(ctx context.Context, arguments []string) int {
	flags := command.newFlagSet("sync", syncSummary)
	var configPath string
	var dryRun bool
	flags.StringVar(&configPath, "config", "", "read the organizations and the forks to include and exclude from this `file`")
	flags.StringVar(&configPath, "c", "", "shorthand for -config")
	flags.BoolVar(&dryRun, "dry-run", false, "list what would be synced without changing anything")
	positional, code, proceed := parse(flags, arguments)
	if !proceed {
		return code
	}
	if len(positional) > 0 {
		return command.usageError(flags, "sync takes no arguments")
	}

	configuration := &config.Config{}
	if configPath != "" {
		var err error
		if configuration, err = config.Load(configPath); err != nil {
			return command.fail(err)
		}
	}
	// Read the config before the token, so a broken config is reported even
	// when the token is missing too.
	token, _ := command.lookupEnvironment(tokenVariable)
	if token == "" {
		return command.fail(fmt.Errorf("set %s to a GitHub token that can write to your forks", tokenVariable))
	}

	client := &github.Client{HTTPClient: command.httpClient, BaseURL: command.apiURL, Token: token}
	// Every list is fetched before the first sync, so a misspelled
	// organization stops the run before anything has changed instead of
	// halfway through it.
	forks, err := client.Forks(ctx)
	if err != nil {
		return command.fail(err)
	}
	for _, organization := range configuration.Organizations {
		organizationForks, err := client.OrganizationForks(ctx, organization)
		if err != nil {
			return command.fail(err)
		}
		forks = append(forks, organizationForks...)
	}

	code = exitSuccess
	for _, fork := range forks {
		// Stop between forks on Ctrl-C instead of printing a failure for
		// every remaining one.
		if err := ctx.Err(); err != nil {
			return command.fail(err)
		}
		status, detail := syncFork(ctx, client, configuration, fork, dryRun)
		if status == statusFailed {
			code = exitFailure
		}
		// Lines are printed as each fork finishes rather than as an aligned
		// table at the end, so a long run shows progress.
		_, _ = fmt.Fprintln(command.stdout, strings.TrimSpace(fmt.Sprintf("%-10s  %s  %s", status, fork.FullName, detail)))
	}
	return code
}

// syncFork syncs one fork, unless it is excluded or archived, and returns its
// status and a detail for the output.
func syncFork(ctx context.Context, client *github.Client, configuration *config.Config, fork github.Fork, dryRun bool) (string, string) {
	if reason, excluded := configuration.Excluded(fork.FullName); excluded {
		return statusExcluded, reason
	}
	// Archived repositories are read-only, so syncing them always fails.
	// Skipping them keeps one archived fork from failing every run.
	if fork.Archived {
		return statusArchived, ""
	}
	if dryRun {
		return statusWouldSync, fork.DefaultBranch
	}
	result, err := client.Sync(ctx, fork)
	if err != nil {
		return statusFailed, err.Error()
	}
	if result.MergeType == github.MergeNone {
		return statusUpToDate, fork.DefaultBranch
	}
	return statusSynced, fmt.Sprintf("%s (%s)", fork.DefaultBranch, result.MergeType)
}
