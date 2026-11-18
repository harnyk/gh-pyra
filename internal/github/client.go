// Package github implements hide.Client on top of the GitHub CLI.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	gh "github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/harnyk/gh-pyra/internal/hide"
)

const listFilesQuery = `query ListPullRequestFiles($pullRequestId: ID!, $endCursor: String) {
  node(id: $pullRequestId) {
    ... on PullRequest {
      files(first: 100, after: $endCursor) {
        nodes { path viewerViewedState }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`

const markMutation = `mutation MarkFileAsViewed($pullRequestId: ID!, $path: String!) {
  markFileAsViewed(input: {pullRequestId: $pullRequestId, path: $path}) {
    clientMutationId
  }
}`

const unmarkMutation = `mutation UnmarkFileAsViewed($pullRequestId: ID!, $path: String!) {
  unmarkFileAsViewed(input: {pullRequestId: $pullRequestId, path: $path}) {
    clientMutationId
  }
}`

// Target selects the pull request to operate on.
type Target struct {
	// PR is a pull request number, URL or branch name. Empty means the PR of
	// the current branch.
	PR string
	// Repo is [HOST/]OWNER/REPO. Empty means the current repository.
	Repo string
}

// Client talks to GitHub using the credentials of the authenticated gh CLI.
type Client struct {
	target Target
	gql    *api.GraphQLClient
}

// New creates a Client for the given target. The API host is determined when
// the pull request is resolved.
func New(target Target) *Client {
	return &Client{target: target}
}

// prViewArgs builds the `gh pr view` arguments for the target.
func prViewArgs(t Target) []string {
	args := []string{"pr", "view"}
	if t.PR != "" {
		args = append(args, t.PR)
	}
	if t.Repo != "" {
		args = append(args, "--repo", t.Repo)
	}
	return append(args, "--json", "id,number,url")
}

// hostFromURL extracts the GitHub host from a pull request URL.
func hostFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("unexpected pull request URL %q", rawURL)
	}
	return u.Host, nil
}

// ResolvePullRequest resolves the target pull request via `gh pr view` and
// prepares a GraphQL client for its host.
func (c *Client) ResolvePullRequest(ctx context.Context) (hide.PullRequest, error) {
	stdout, stderr, err := gh.ExecContext(ctx, prViewArgs(c.target)...)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return hide.PullRequest{}, fmt.Errorf("%w: %s", err, msg)
		}
		return hide.PullRequest{}, err
	}
	var pr struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pr); err != nil {
		return hide.PullRequest{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	host, err := hostFromURL(pr.URL)
	if err != nil {
		return hide.PullRequest{}, err
	}
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: host})
	if err != nil {
		return hide.PullRequest{}, err
	}
	c.gql = gql
	return hide.PullRequest{ID: pr.ID, Number: pr.Number, URL: pr.URL}, nil
}

var errNotResolved = errors.New("pull request is not resolved yet")

// ListFiles returns all files of the pull request, following pagination.
func (c *Client) ListFiles(ctx context.Context, pullRequestID string) ([]hide.File, error) {
	var files []hide.File
	var cursor *string
	if c.gql == nil {
		return nil, errNotResolved
	}
	for {
		var resp struct {
			Node struct {
				Files struct {
					Nodes []struct {
						Path              string `json:"path"`
						ViewerViewedState string `json:"viewerViewedState"`
					} `json:"nodes"`
					PageInfo struct {
						HasNextPage bool   `json:"hasNextPage"`
						EndCursor   string `json:"endCursor"`
					} `json:"pageInfo"`
				} `json:"files"`
			} `json:"node"`
		}
		vars := map[string]any{"pullRequestId": pullRequestID, "endCursor": cursor}
		if err := c.gql.DoWithContext(ctx, listFilesQuery, vars, &resp); err != nil {
			return nil, err
		}
		for _, n := range resp.Node.Files.Nodes {
			files = append(files, hide.File{Path: n.Path, Viewed: n.ViewerViewedState == "VIEWED"})
		}
		if !resp.Node.Files.PageInfo.HasNextPage {
			return files, nil
		}
		next := resp.Node.Files.PageInfo.EndCursor
		cursor = &next
	}
}

// SetViewed marks or unmarks a single file as viewed.
func (c *Client) SetViewed(ctx context.Context, pullRequestID, path string, viewed bool) error {
	if c.gql == nil {
		return errNotResolved
	}
	mutation := unmarkMutation
	if viewed {
		mutation = markMutation
	}
	vars := map[string]any{"pullRequestId": pullRequestID, "path": path}
	return c.gql.DoWithContext(ctx, mutation, vars, nil)
}
