package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const fakeToken = "fake-secret-token"

// fakeGitHub serves the two endpoints forklift uses and records which forks
// were synced, so the whole command can be tested without the real GitHub.
type fakeGitHub struct {
	mutex  sync.Mutex
	synced []string
}

// ServeHTTP implements http.Handler.
func (fake *fakeGitHub) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch {
	case request.Method == http.MethodGet && request.URL.Path == "/user/repos":
		_, _ = writer.Write([]byte(`[
			{"full_name": "octocat/behind", "default_branch": "main", "fork": true},
			{"full_name": "octocat/current", "default_branch": "main", "fork": true},
			{"full_name": "octocat/conflict", "default_branch": "main", "fork": true},
			{"full_name": "octocat/patched", "default_branch": "main", "fork": true},
			{"full_name": "octocat/old", "default_branch": "main", "fork": true, "archived": true},
			{"full_name": "octocat/own", "default_branch": "main", "fork": false}
		]`))
	case request.Method == http.MethodGet && request.URL.Path == "/orgs/octo-org/repos":
		_, _ = writer.Write([]byte(`[{"full_name": "octo-org/behind", "default_branch": "main", "fork": true}]`))
	case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/merge-upstream"):
		name := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/repos/"), "/merge-upstream")
		fake.mutex.Lock()
		fake.synced = append(fake.synced, name)
		fake.mutex.Unlock()
		switch name {
		case "octocat/behind", "octo-org/behind":
			_, _ = writer.Write([]byte(`{"merge_type": "fast-forward"}`))
		case "octocat/current":
			_, _ = writer.Write([]byte(`{"merge_type": "none"}`))
		default:
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"message": "There are merge conflicts"}`))
		}
	default:
		writer.WriteHeader(http.StatusNotFound)
	}
}

// invocation is the outcome of one run of the command.
type invocation struct {
	code   int
	stdout string
	stderr string
}

// invoke runs the command against fake with only the given environment
// variables, so tests don't depend on the developer's real environment.
func invoke(test *testing.T, fake *fakeGitHub, environment map[string]string, arguments ...string) invocation {
	test.Helper()
	server := httptest.NewServer(fake)
	test.Cleanup(server.Close)
	var stdout, stderr bytes.Buffer
	command := &cli{
		stdout: &stdout,
		stderr: &stderr,
		lookupEnvironment: func(name string) (string, bool) {
			value, found := environment[name]
			return value, found
		},
		apiURL:     server.URL,
		httpClient: server.Client(),
	}
	code := command.run(test.Context(), arguments)
	return invocation{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

// writeConfig writes a config file and returns its path.
func writeConfig(test *testing.T, content string) string {
	test.Helper()
	filePath := filepath.Join(test.TempDir(), "forklift.json")
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		test.Fatal(err)
	}
	return filePath
}

// withToken is an environment with a token set.
var withToken = map[string]string{tokenVariable: fakeToken}

// TestSync checks a full run: excluded and archived forks are left alone,
// every other fork is synced, and a failure makes the run exit 1.
func TestSync(test *testing.T) {
	fake := &fakeGitHub{}
	configPath := writeConfig(test, `{"exclude": ["octocat/patched"]}`)
	result := invoke(test, fake, withToken, "sync", "-c", configPath)

	if result.code != exitFailure {
		test.Errorf("exit code = %d, want %d because one fork has conflicts", result.code, exitFailure)
	}
	wantLines := []string{
		"synced      octocat/behind  main (fast-forward)",
		"up-to-date  octocat/current  main",
		"failed      octocat/conflict  409 Conflict: There are merge conflicts",
		`excluded    octocat/patched  matches exclude pattern "octocat/patched"`,
		"archived    octocat/old",
	}
	if got := strings.Split(strings.TrimSpace(result.stdout), "\n"); strings.Join(got, "\n") != strings.Join(wantLines, "\n") {
		test.Errorf("stdout =\n%s\nwant\n%s", result.stdout, strings.Join(wantLines, "\n"))
	}
	if got := strings.Join(fake.synced, ","); got != "octocat/behind,octocat/current,octocat/conflict" {
		test.Errorf("synced forks = %s, want octocat/behind,octocat/current,octocat/conflict", got)
	}
	if strings.Contains(result.stdout+result.stderr, fakeToken) {
		test.Error("output contains the token")
	}
}

// TestSyncOrganizations checks that the forks of a configured organization
// are synced after the user's own forks and that exclude patterns apply to
// them too.
func TestSyncOrganizations(test *testing.T) {
	fake := &fakeGitHub{}
	configPath := writeConfig(test, `{"organizations": ["octo-org"], "exclude": ["octocat/*"]}`)
	result := invoke(test, fake, withToken, "sync", "-c", configPath)

	if result.code != exitSuccess {
		test.Errorf("exit code = %d, want %d; stderr: %s", result.code, exitSuccess, result.stderr)
	}
	if !strings.HasSuffix(result.stdout, "synced      octo-org/behind  main (fast-forward)\n") {
		test.Errorf("stdout = %q, want octo-org/behind synced last", result.stdout)
	}
	if got := strings.Join(fake.synced, ","); got != "octo-org/behind" {
		test.Errorf("synced forks = %s, want octo-org/behind", got)
	}
}

// TestSyncInclude checks that with an include list only the matching forks
// are synced.
func TestSyncInclude(test *testing.T) {
	fake := &fakeGitHub{}
	configPath := writeConfig(test, `{"include": ["octocat/behind", "octocat/current"]}`)
	result := invoke(test, fake, withToken, "sync", "-c", configPath)

	if result.code != exitSuccess {
		test.Errorf("exit code = %d, want %d; stderr: %s", result.code, exitSuccess, result.stderr)
	}
	if got := strings.Join(fake.synced, ","); got != "octocat/behind,octocat/current" {
		test.Errorf("synced forks = %s, want octocat/behind,octocat/current", got)
	}
	if !strings.Contains(result.stdout, "excluded    octocat/conflict  matches no include pattern") {
		test.Errorf("stdout = %q, want octocat/conflict excluded", result.stdout)
	}
}

// TestSyncUnknownOrganization checks that an organization that can't be
// listed stops the run before any fork, even the user's own, is synced.
func TestSyncUnknownOrganization(test *testing.T) {
	fake := &fakeGitHub{}
	configPath := writeConfig(test, `{"organizations": ["octo-typo"]}`)
	result := invoke(test, fake, withToken, "sync", "-c", configPath)

	if result.code != exitFailure || !strings.Contains(result.stderr, "octo-typo") {
		test.Errorf("exit code %d, stderr %q, want a failure naming octo-typo", result.code, result.stderr)
	}
	if len(fake.synced) != 0 {
		test.Errorf("synced %v, want nothing", fake.synced)
	}
}

// TestSyncDryRun checks that a dry run changes nothing.
func TestSyncDryRun(test *testing.T) {
	fake := &fakeGitHub{}
	result := invoke(test, fake, withToken, "sync", "-dry-run")
	if result.code != exitSuccess {
		test.Errorf("exit code = %d, want %d; stderr: %s", result.code, exitSuccess, result.stderr)
	}
	if len(fake.synced) != 0 {
		test.Errorf("dry run synced %v", fake.synced)
	}
	if !strings.Contains(result.stdout, "would-sync  octocat/behind  main") {
		test.Errorf("stdout = %q, want a would-sync line", result.stdout)
	}
}

// TestSyncErrors checks the runs that must stop before syncing anything.
func TestSyncErrors(test *testing.T) {
	testCases := []struct {
		name        string
		environment map[string]string
		arguments   []string
		code        int
		stderr      string
	}{
		{name: "missing token", arguments: []string{"sync"}, code: exitFailure, stderr: tokenVariable},
		{name: "missing config", environment: withToken, arguments: []string{"sync", "-c", "does-not-exist.json"}, code: exitFailure, stderr: "does-not-exist.json"},
		{name: "positional argument", environment: withToken, arguments: []string{"sync", "octocat/behind"}, code: exitUsage, stderr: "takes no arguments"},
		{name: "unknown flag", environment: withToken, arguments: []string{"sync", "-force"}, code: exitUsage, stderr: "-force"},
		{name: "unknown command", arguments: []string{"update"}, code: exitUsage, stderr: `unknown command "update"`},
		{name: "no command", arguments: nil, code: exitUsage, stderr: "Usage:"},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			fake := &fakeGitHub{}
			result := invoke(test, fake, testCase.environment, testCase.arguments...)
			if result.code != testCase.code {
				test.Errorf("exit code = %d, want %d", result.code, testCase.code)
			}
			if !strings.Contains(result.stderr, testCase.stderr) {
				test.Errorf("stderr = %q, want it to contain %q", result.stderr, testCase.stderr)
			}
			if len(fake.synced) != 0 {
				test.Errorf("synced %v, want nothing", fake.synced)
			}
		})
	}
}

// TestSyncBadConfigBeforeToken checks that a broken config is reported even
// when the token is missing, so both can be fixed in one go.
func TestSyncBadConfigBeforeToken(test *testing.T) {
	configPath := writeConfig(test, `{"exclude": ["patched"]}`)
	result := invoke(test, &fakeGitHub{}, nil, "sync", "-c", configPath)
	if result.code != exitFailure || !strings.Contains(result.stderr, `"patched" must have the form`) {
		test.Errorf("exit code %d, stderr %q, want the config problem", result.code, result.stderr)
	}
}

// TestVersion checks that the version command prints something.
func TestVersion(test *testing.T) {
	result := invoke(test, &fakeGitHub{}, nil, "version")
	if result.code != exitSuccess || !strings.HasPrefix(result.stdout, "forklift ") {
		test.Errorf("version: exit code %d, stdout %q", result.code, result.stdout)
	}
}
