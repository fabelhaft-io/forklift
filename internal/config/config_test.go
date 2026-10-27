package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestParse checks which config files are accepted and which are rejected.
func TestParse(test *testing.T) {
	testCases := []struct {
		name    string
		data    string
		failure string
	}{
		{name: "empty object", data: `{}`},
		{name: "exclude list", data: `{"exclude": ["octocat/hello-world", "octocat/*"]}`},
		{name: "unknown field", data: `{"exclude_forks": []}`, failure: `unknown field "exclude_forks"`},
		{name: "trailing data", data: `{} {}`, failure: "unexpected data"},
		{name: "missing owner", data: `{"exclude": ["hello-world"]}`, failure: `"hello-world" must have the form`},
		{name: "too many slashes", data: `{"exclude": ["octocat/hello/world"]}`, failure: "must have the form"},
		{name: "bad pattern", data: `{"exclude": ["octocat/[hello"]}`, failure: "syntax error in pattern"},
		{name: "include list", data: `{"include": ["octo-org/*"]}`},
		{name: "include without owner", data: `{"include": ["tool"]}`, failure: `include pattern "tool" must have the form`},
		{name: "organizations", data: `{"organizations": ["octo-org", "Other0rg"]}`},
		{name: "organization URL", data: `{"organizations": ["https://github.com/octo-org"]}`, failure: "must be a plain name"},
		{name: "organization with repository", data: `{"organizations": ["octo-org/fork"]}`, failure: "must be a plain name"},
		{name: "empty organization", data: `{"organizations": [""]}`, failure: "must be a plain name"},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			_, err := Parse([]byte(testCase.data))
			switch {
			case testCase.failure == "" && err != nil:
				test.Fatalf("Parse: %v", err)
			case testCase.failure != "" && (err == nil || !strings.Contains(err.Error(), testCase.failure)):
				test.Fatalf("Parse error = %v, want it to contain %q", err, testCase.failure)
			}
		})
	}
}

// TestParseReportsEveryProblem checks that all bad patterns are reported at
// once, so fixing a config doesn't take one run per mistake.
func TestParseReportsEveryProblem(test *testing.T) {
	_, err := Parse([]byte(`{"exclude": ["first", "second"]}`))
	if err == nil || !strings.Contains(err.Error(), `"first"`) || !strings.Contains(err.Error(), `"second"`) {
		test.Fatalf("Parse error = %v, want both patterns reported", err)
	}
}

// TestExcluded checks the include and exclude lists, including that matching
// ignores case, that "*" doesn't cross the "/" between owner and name, and
// that exclude wins over include.
func TestExcluded(test *testing.T) {
	testCases := []struct {
		name          string
		configuration Config
		fullName      string
		reason        string
	}{
		{name: "no lists", fullName: "octocat/hello-world"},
		{name: "exclude match", configuration: Config{Exclude: []string{"octocat/Hello-World"}}, fullName: "octocat/hello-world", reason: `matches exclude pattern "octocat/Hello-World"`},
		{name: "exclude wildcard", configuration: Config{Exclude: []string{"octocat/linux-*"}}, fullName: "OctoCat/linux-patched", reason: `matches exclude pattern "octocat/linux-*"`},
		{name: "exclude no match", configuration: Config{Exclude: []string{"octocat/linux-*"}}, fullName: "octocat/linux"},
		{name: "wildcard stays in owner", configuration: Config{Exclude: []string{"*"}}, fullName: "octocat/hello-world"},
		{name: "include match", configuration: Config{Include: []string{"octo-org/*"}}, fullName: "octo-org/tool"},
		{name: "include no match", configuration: Config{Include: []string{"octo-org/*"}}, fullName: "octocat/tool", reason: "matches no include pattern"},
		{name: "exclude wins", configuration: Config{Include: []string{"octo-org/*"}, Exclude: []string{"octo-org/legacy-*"}}, fullName: "octo-org/legacy-tool", reason: `matches exclude pattern "octo-org/legacy-*"`},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			reason, excluded := testCase.configuration.Excluded(testCase.fullName)
			if reason != testCase.reason || excluded != (testCase.reason != "") {
				test.Errorf("Excluded(%q) = %q, %v, want %q", testCase.fullName, reason, excluded, testCase.reason)
			}
		})
	}
}

// TestLoad checks that Load reads a file from disk.
func TestLoad(test *testing.T) {
	filePath := filepath.Join(test.TempDir(), "forklift.json")
	if err := os.WriteFile(filePath, []byte(`{"exclude": ["octocat/*"]}`), 0o600); err != nil {
		test.Fatal(err)
	}
	configuration, err := Load(filePath)
	if err != nil {
		test.Fatalf("Load: %v", err)
	}
	if len(configuration.Exclude) != 1 || configuration.Exclude[0] != "octocat/*" {
		test.Fatalf("Exclude = %q, want [octocat/*]", configuration.Exclude)
	}
}
