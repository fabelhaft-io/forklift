// Package config reads the forklift config file, which lists the
// organizations whose forks are synced too, and which forks to sync or leave
// alone.
//
// Organization forks are opt-in. They are often maintained by other people,
// sometimes with patches on purpose, and a sync can't be undone, so forklift
// only touches the organizations the user names.
//
// Some forks are meant to drift from their upstream: a patched copy you
// maintain yourself, a fork whose upstream was deleted, a snapshot you keep on
// purpose. Syncing those would either fail on every run or, worse, merge
// changes you don't want, so they are excluded by name. When only a few forks
// should be synced, listing those is easier, so the include list does the
// opposite.
//
// Every problem in the file is reported at once and before anything is
// synced. forklift usually runs unattended, and a typo in a pattern that only
// shows up after it has merged into the fork it was meant to protect can't be
// undone by fixing the file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// organizationName is what GitHub allows in an organization name. Checking
// it catches URLs and "owner/name" pasted by mistake, and means the name is
// safe to put into an API URL as it is.
var organizationName = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Config is the content of the config file.
type Config struct {
	// Organizations lists the organizations whose forks are synced in
	// addition to the user's own forks.
	Organizations []string `json:"organizations"`
	// Include lists "owner/name" patterns of the forks to sync. Empty means
	// every fork. The patterns use path.Match syntax, so "*" matches any part
	// of a name but never the "/" between owner and name.
	Include []string `json:"include"`
	// Exclude lists patterns, like Include, of forks to leave alone. It wins
	// over Include, so a fork is never synced by accident because a broad
	// include pattern also matches it.
	Exclude []string `json:"exclude"`
}

// Load reads and checks the config file at filePath.
func Load(filePath string) (*Config, error) {
	data, err := os.ReadFile(filepath.Clean(filePath))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// Parse reads and checks a config file. Unknown fields are an error, so a
// typo such as "exclude_forks" doesn't silently exclude nothing.
func Parse(data []byte) (*Config, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var configuration Config
	if err := decoder.Decode(&configuration); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if decoder.More() {
		return nil, errors.New("parse config: unexpected data after the closing brace")
	}
	var problems []error
	for _, organization := range configuration.Organizations {
		if !organizationName.MatchString(organization) {
			problems = append(problems, fmt.Errorf("organization %q must be a plain name such as \"octo-org\"", organization))
		}
	}
	for _, pattern := range configuration.Include {
		if err := checkPattern("include", pattern); err != nil {
			problems = append(problems, err)
		}
	}
	for _, pattern := range configuration.Exclude {
		if err := checkPattern("exclude", pattern); err != nil {
			problems = append(problems, err)
		}
	}
	if err := errors.Join(problems...); err != nil {
		return nil, err
	}
	return &configuration, nil
}

// checkPattern reports a pattern of the given list ("include" or "exclude")
// that can't be what the user meant.
func checkPattern(list, pattern string) error {
	// A pattern without an owner, such as "linux", would never match,
	// because names are matched as "owner/name". Rejecting it is safer than
	// guessing an owner: in the exclude list, the fork it was meant to
	// protect would be synced.
	if strings.Count(pattern, "/") != 1 {
		return fmt.Errorf("%s pattern %q must have the form \"owner/name\"", list, pattern)
	}
	if _, err := path.Match(pattern, ""); err != nil {
		return fmt.Errorf("%s pattern %q: %w", list, pattern, err)
	}
	return nil
}

// Excluded reports whether the fork fullName ("owner/name") must be left
// alone, and why: because it matches no include pattern, or because it
// matches an exclude pattern.
func (configuration *Config) Excluded(fullName string) (string, bool) {
	if len(configuration.Include) > 0 {
		if _, matched := firstMatch(configuration.Include, fullName); !matched {
			return "matches no include pattern", true
		}
	}
	if pattern, matched := firstMatch(configuration.Exclude, fullName); matched {
		return fmt.Sprintf("matches exclude pattern %q", pattern), true
	}
	return "", false
}

// firstMatch returns the first of patterns that matches fullName. Matching
// ignores case, like GitHub does for owner and repository names.
func firstMatch(patterns []string, fullName string) (string, bool) {
	for _, pattern := range patterns {
		// Parse checked every pattern, so Match can't fail here.
		if matched, _ := path.Match(strings.ToLower(pattern), strings.ToLower(fullName)); matched {
			return pattern, true
		}
	}
	return "", false
}
