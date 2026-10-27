// Command forklift brings your GitHub forks up to date with their upstream
// repositories.
//
// The command only parses the command line and prints results; talking to
// GitHub and reading the config live in packages under internal/. Each
// command is in its own command_<name>.go file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/fabelhaft-io/forklift/internal/github"
)

// Exit codes. Schedulers only see the exit code, so a run in which any fork
// failed to sync must not exit 0: otherwise a cron job would report success
// for months while a fork silently falls further behind.
const (
	exitSuccess = 0
	// exitFailure means a fork failed to sync, or the configuration or token
	// is invalid.
	exitFailure = 1
	// exitUsage means the command line was wrong.
	exitUsage = 2
)

// tokenVariable is the environment variable that holds the GitHub token. It
// has its own name instead of GITHUB_TOKEN because GitHub Actions sets
// GITHUB_TOKEN to a token scoped to the workflow's repository, which can't
// sync your forks and would only produce confusing permission errors.
const tokenVariable = "FORKLIFT_TOKEN"

const usage = `forklift brings your GitHub forks up to date with their upstream repositories.

Usage:
  forklift sync [-c <config.json>] [-dry-run]   sync every fork you own
  forklift version                              print the version

The GitHub token is read from the FORKLIFT_TOKEN environment variable.
Run "forklift <command> -h" for the options of a command.
`

// main wires the command to the real process: arguments, output, environment
// and signals.
func main() {
	// Ctrl-C and the SIGTERM that schedulers and containers send cancel the
	// context, so a run stops between forks instead of being killed mid-request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	command := &cli{
		stdout:            os.Stdout,
		stderr:            os.Stderr,
		lookupEnvironment: os.LookupEnv,
		apiURL:            github.DefaultURL,
		// Merging a large upstream can take GitHub a while, but a hanging
		// request must not block a scheduled run forever.
		httpClient: &http.Client{Timeout: 2 * time.Minute},
	}
	code := command.run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

// cli holds everything a command reads from and writes to, so tests can
// replace it with buffers, a fake environment and a test server.
type cli struct {
	stdout            io.Writer
	stderr            io.Writer
	lookupEnvironment func(name string) (string, bool)
	apiURL            string
	httpClient        *http.Client
}

// run executes one command and returns its exit code. Tests call it directly,
// which is much faster than building and starting the binary.
func (command *cli) run(ctx context.Context, arguments []string) int {
	if len(arguments) == 0 {
		_, _ = io.WriteString(command.stderr, usage)
		return exitUsage
	}
	name, rest := arguments[0], arguments[1:]
	switch name {
	case "sync":
		return command.sync(ctx, rest)
	case "version", "-version", "--version":
		_, _ = fmt.Fprintln(command.stdout, "forklift", resolveVersion())
		return exitSuccess
	case "help", "-h", "-help", "--help":
		_, _ = io.WriteString(command.stdout, usage)
		return exitSuccess
	}
	_, _ = fmt.Fprintf(command.stderr, "forklift: unknown command %q\n\n%s", name, usage)
	return exitUsage
}

// newFlagSet returns a flag set for a command that prints summary and the
// flags as its help. ContinueOnError makes the flag package return errors
// instead of calling os.Exit, so tests can check them and run decides the exit
// code.
func (command *cli) newFlagSet(name, summary string) *flag.FlagSet {
	flags := flag.NewFlagSet("forklift "+name, flag.ContinueOnError)
	flags.SetOutput(command.stderr)
	flags.Usage = func() {
		_, _ = io.WriteString(command.stderr, summary)
		flags.PrintDefaults()
	}
	return flags
}

// parse parses the flags and returns the remaining arguments, or an exit
// code if parsing should stop. -h is not an error, so it exits 0.
func parse(flags *flag.FlagSet, arguments []string) ([]string, int, bool) {
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, exitSuccess, false
		}
		return nil, exitUsage, false
	}
	return flags.Args(), 0, true
}

// fail prints every line of err and returns exitFailure. Each line gets the
// "forklift:" prefix, so joined errors (one problem per line) stay readable
// and can be found with grep in a log.
func (command *cli) fail(err error) int {
	for line := range strings.SplitSeq(err.Error(), "\n") {
		_, _ = fmt.Fprintln(command.stderr, "forklift:", line)
	}
	return exitFailure
}

// usageError prints message and the command's help, and returns exitUsage,
// which tells scripts the command line is wrong rather than a sync.
func (command *cli) usageError(flags *flag.FlagSet, message string) int {
	_, _ = fmt.Fprintln(command.stderr, "forklift:", message)
	flags.Usage()
	return exitUsage
}

// resolveVersion returns the module version Go records in the binary, or
// "dev" if there is none. `go install ...@vX.Y.Z` records the requested
// version, and since Go 1.24 `go build` in a git checkout records the tag or
// a pseudo-version, so releases need no -ldflags version variable.
func resolveVersion() string {
	buildInformation, ok := debug.ReadBuildInfo()
	if ok && buildInformation.Main.Version != "" && buildInformation.Main.Version != "(devel)" {
		return buildInformation.Main.Version
	}
	return "dev"
}
