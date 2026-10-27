// Package github talks to the GitHub REST API: it lists the forks of the
// authenticated user and asks GitHub to merge upstream changes into them.
//
// forklift needs only two endpoints, so this is a small hand-written client
// instead of an SDK dependency. Syncing goes through GitHub's merge-upstream
// endpoint, the same one behind the "Sync fork" button, so forklift never
// clones a repository and doesn't need git on the machine.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DefaultURL is the API of github.com.
const DefaultURL = "https://api.github.com"

// Client sends requests to the GitHub REST API.
type Client struct {
	// HTTPClient sends the requests. It should have a timeout, because a
	// hanging request would otherwise block a scheduled run forever.
	HTTPClient *http.Client
	// BaseURL is the API root without a trailing slash, DefaultURL in
	// production and a test server in tests.
	BaseURL string
	// Token is sent with every request. It never appears in errors.
	Token string
}

// Fork is a fork owned by the authenticated user, with the fields forklift
// uses.
type Fork struct {
	// FullName is "owner/name".
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Archived      bool   `json:"archived"`
}

// repository is an item of the repository list. It is only used to tell forks
// from other repositories before they become a Fork.
type repository struct {
	Fork
	IsFork bool `json:"fork"`
}

// Merge types GitHub reports for a sync.
const (
	MergeFastForward = "fast-forward"
	MergeMerge       = "merge"
	// MergeNone means the branch already had every upstream commit.
	MergeNone = "none"
)

// SyncResult is GitHub's answer to a successful sync.
type SyncResult struct {
	// MergeType is one of MergeFastForward, MergeMerge or MergeNone.
	MergeType string `json:"merge_type"`
	// Message is GitHub's description, such as "Successfully fetched and
	// fast-forwarded from upstream octocat:main."
	Message string `json:"message"`
}

// StatusError is a response with an unexpected status. It keeps GitHub's own
// message, which usually says exactly what is wrong, such as "There are merge
// conflicts".
type StatusError struct {
	Status  int
	Message string
}

// Error implements error.
func (failure *StatusError) Error() string {
	if failure.Message == "" {
		return fmt.Sprintf("%d %s", failure.Status, http.StatusText(failure.Status))
	}
	return fmt.Sprintf("%d %s: %s", failure.Status, http.StatusText(failure.Status), failure.Message)
}

// Forks returns every fork the authenticated user owns, archived ones
// included, so the caller can report them instead of silently leaving them
// out. Forks owned by organizations are not included; see OrganizationForks.
func (client *Client) Forks(ctx context.Context) ([]Fork, error) {
	// affiliation=owner leaves out repositories the user only collaborates on
	// or reaches through an organization. Organization forks are often
	// maintained by other people on purpose, so they are only synced when the
	// user names the organization.
	forks, err := client.listForks(ctx, client.BaseURL+"/user/repos?affiliation=owner")
	if err != nil {
		return nil, fmt.Errorf("list your repositories: %w", err)
	}
	return forks, nil
}

// OrganizationForks returns every fork the organization owns that the token
// can see, archived ones included.
func (client *Client) OrganizationForks(ctx context.Context, organization string) ([]Fork, error) {
	// config.Parse only accepts names made of letters, digits and hyphens,
	// so the name needs no escaping.
	forks, err := client.listForks(ctx, client.BaseURL+"/orgs/"+organization+"/repos?type=forks")
	if err != nil {
		return nil, fmt.Errorf("list repositories of organization %s: %w", organization, err)
	}
	return forks, nil
}

// listForks fetches every page of a repository list and keeps the forks.
// The /user/repos endpoint has no filter for forks, so the filter is applied
// here for both endpoints.
func (client *Client) listForks(ctx context.Context, address string) ([]Fork, error) {
	// 100 is the largest page GitHub allows.
	address += "&per_page=100"
	var forks []Fork
	for address != "" {
		response, err := client.request(ctx, http.MethodGet, address, nil)
		if err != nil {
			return nil, err
		}
		var page []repository
		err = json.NewDecoder(response.Body).Decode(&page)
		_ = response.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		for _, item := range page {
			if item.IsFork {
				forks = append(forks, item.Fork)
			}
		}
		if address, err = client.nextPage(response.Header); err != nil {
			return nil, err
		}
	}
	return forks, nil
}

// Sync merges the upstream changes of the fork's default branch into it.
// Only the default branch is synced: it is the branch the "Sync fork" button
// updates, and other branches of a fork are usually the user's own work.
func (client *Client) Sync(ctx context.Context, fork Fork) (SyncResult, error) {
	body, err := json.Marshal(map[string]string{"branch": fork.DefaultBranch})
	if err != nil {
		return SyncResult{}, err
	}
	// FullName comes from GitHub and only contains characters that are safe
	// in a URL path, so it needs no escaping.
	address := client.BaseURL + "/repos/" + fork.FullName + "/merge-upstream"
	response, err := client.request(ctx, http.MethodPost, address, body)
	if err != nil {
		return SyncResult{}, err
	}
	defer func() { _ = response.Body.Close() }()
	var result SyncResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return SyncResult{}, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// request sends a request and returns the response if it is 200 OK.
func (client *Client) request(ctx context.Context, method, address string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("Authorization", "Bearer "+client.Token)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.HTTPClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode == http.StatusOK {
		return response, nil
	}
	defer func() { _ = response.Body.Close() }()
	var failure struct {
		Message string `json:"message"`
	}
	// Error bodies are small; reading at most 64 KiB keeps a broken response
	// from filling memory.
	_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&failure)
	statusError := &StatusError{Status: response.StatusCode, Message: failure.Message}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("GitHub rejected the token: %w", statusError)
	}
	return nil, statusError
}

// nextPage returns the URL of the next page from a Link header, or "" on the
// last page. The URL must point at the API itself: the token goes with every
// request, so following a link to another host would send it there.
func (client *Client) nextPage(header http.Header) (string, error) {
	for link := range strings.SplitSeq(header.Get("Link"), ",") {
		address, relation, found := strings.Cut(link, ";")
		if !found || strings.TrimSpace(relation) != `rel="next"` {
			continue
		}
		address = strings.Trim(strings.TrimSpace(address), "<>")
		// The trailing slash keeps a host such as api.github.com.example.org
		// from passing as api.github.com.
		if !strings.HasPrefix(address, client.BaseURL+"/") {
			return "", fmt.Errorf("next page %q is not on %s", address, client.BaseURL)
		}
		return address, nil
	}
	return "", nil
}
