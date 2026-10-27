package github

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const fakeToken = "fake-secret-token"

// newClient returns a client that sends its requests to handler.
func newClient(test *testing.T, handler http.Handler) *Client {
	test.Helper()
	server := httptest.NewServer(handler)
	test.Cleanup(server.Close)
	return &Client{HTTPClient: server.Client(), BaseURL: server.URL, Token: fakeToken}
}

// TestForks checks that Forks follows pagination, keeps only forks and sends
// the token.
func TestForks(test *testing.T) {
	var baseURL string
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+fakeToken {
			test.Errorf("Authorization header is missing or wrong")
		}
		switch request.URL.Query().Get("page") {
		case "":
			writer.Header().Set("Link", fmt.Sprintf(`<%s/user/repos?page=2>; rel="next", <%s/user/repos?page=2>; rel="last"`, baseURL, baseURL))
			_, _ = writer.Write([]byte(`[
				{"full_name": "octocat/fork", "default_branch": "main", "fork": true},
				{"full_name": "octocat/own", "default_branch": "main", "fork": false}
			]`))
		case "2":
			_, _ = writer.Write([]byte(`[{"full_name": "octocat/old", "default_branch": "master", "fork": true, "archived": true}]`))
		}
	})
	client := newClient(test, handler)
	baseURL = client.BaseURL

	forks, err := client.Forks(test.Context())
	if err != nil {
		test.Fatalf("Forks: %v", err)
	}
	want := []Fork{
		{FullName: "octocat/fork", DefaultBranch: "main"},
		{FullName: "octocat/old", DefaultBranch: "master", Archived: true},
	}
	if fmt.Sprint(forks) != fmt.Sprint(want) {
		test.Fatalf("Forks = %v, want %v", forks, want)
	}
}

// TestOrganizationForks checks that OrganizationForks asks for the
// organization's forks and keeps only forks.
func TestOrganizationForks(test *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/orgs/octo-org/repos" || request.URL.Query().Get("type") != "forks" {
			test.Errorf("request = %s, want /orgs/octo-org/repos?type=forks", request.URL)
		}
		_, _ = writer.Write([]byte(`[
			{"full_name": "octo-org/fork", "default_branch": "main", "fork": true},
			{"full_name": "octo-org/own", "default_branch": "main", "fork": false}
		]`))
	})
	forks, err := newClient(test, handler).OrganizationForks(test.Context(), "octo-org")
	if err != nil {
		test.Fatalf("OrganizationForks: %v", err)
	}
	if len(forks) != 1 || forks[0].FullName != "octo-org/fork" {
		test.Fatalf("OrganizationForks = %v, want only octo-org/fork", forks)
	}
}

// TestOrganizationForksUnknown checks that an unknown organization names the
// organization in the error, so the user knows which config entry is wrong.
func TestOrganizationForksUnknown(test *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNotFound)
		_, _ = writer.Write([]byte(`{"message": "Not Found"}`))
	})
	_, err := newClient(test, handler).OrganizationForks(test.Context(), "octo-typo")
	if err == nil || !strings.Contains(err.Error(), "octo-typo") || !strings.Contains(err.Error(), "404") {
		test.Fatalf("OrganizationForks error = %v, want a 404 naming octo-typo", err)
	}
}

// TestForksRejectsForeignNextPage checks that a next-page link to another
// host is not followed, because the token would be sent there.
func TestForksRejectsForeignNextPage(test *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Link", `<https://attacker.example/user/repos?page=2>; rel="next"`)
		_, _ = writer.Write([]byte(`[]`))
	})
	_, err := newClient(test, handler).Forks(test.Context())
	if err == nil || !strings.Contains(err.Error(), "attacker.example") {
		test.Fatalf("Forks error = %v, want the foreign link rejected", err)
	}
}

// TestForksBadToken checks that a rejected token is reported as such.
func TestForksBadToken(test *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusUnauthorized)
		_, _ = writer.Write([]byte(`{"message": "Bad credentials"}`))
	})
	_, err := newClient(test, handler).Forks(test.Context())
	if err == nil || !strings.Contains(err.Error(), "rejected the token") || !strings.Contains(err.Error(), "Bad credentials") {
		test.Fatalf("Forks error = %v, want a rejected token", err)
	}
	if strings.Contains(err.Error(), fakeToken) {
		test.Fatalf("error contains the token: %v", err)
	}
}

// TestSync checks the request Sync sends and how it reports GitHub's answers.
func TestSync(test *testing.T) {
	testCases := []struct {
		name      string
		status    int
		body      string
		mergeType string
		failure   int
	}{
		{name: "fast-forward", status: http.StatusOK, body: `{"merge_type": "fast-forward", "message": "Successfully fetched and fast-forwarded"}`, mergeType: MergeFastForward},
		{name: "up to date", status: http.StatusOK, body: `{"merge_type": "none", "message": "This branch is not behind the upstream"}`, mergeType: MergeNone},
		{name: "conflict", status: http.StatusConflict, body: `{"message": "There are merge conflicts"}`, failure: http.StatusConflict},
		{name: "cannot merge", status: http.StatusUnprocessableEntity, body: `{"message": "Branch could not be synced"}`, failure: http.StatusUnprocessableEntity},
	}
	for _, testCase := range testCases {
		test.Run(testCase.name, func(test *testing.T) {
			handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/repos/octocat/fork/merge-upstream" {
					test.Errorf("request = %s %s, want POST /repos/octocat/fork/merge-upstream", request.Method, request.URL.Path)
				}
				var body struct {
					Branch string `json:"branch"`
				}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Branch != "main" {
					test.Errorf("body branch = %q (%v), want main", body.Branch, err)
				}
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			})
			result, err := newClient(test, handler).Sync(test.Context(), Fork{FullName: "octocat/fork", DefaultBranch: "main"})
			if testCase.failure != 0 {
				var statusError *StatusError
				if !errors.As(err, &statusError) || statusError.Status != testCase.failure || statusError.Message == "" {
					test.Fatalf("Sync error = %v, want status %d with GitHub's message", err, testCase.failure)
				}
				return
			}
			if err != nil {
				test.Fatalf("Sync: %v", err)
			}
			if result.MergeType != testCase.mergeType {
				test.Fatalf("MergeType = %q, want %q", result.MergeType, testCase.mergeType)
			}
		})
	}
}
