// Package github implements review.Client on top of the GitHub CLI.
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

	"github.com/harnyk/gh-pyra/internal/config"
	"github.com/harnyk/gh-pyra/internal/review"
)

const listFilesQuery = `query ListPullRequestFiles($pullRequestId: ID!, $endCursor: String) {
  node(id: $pullRequestId) {
    ... on PullRequest {
      files(first: 100, after: $endCursor) {
        nodes { path additions deletions viewerViewedState }
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

	// Set by ResolvePullRequest.
	owner, name, baseRef string
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
	return append(args, "--json", "id,number,url,baseRefName")
}

// parsePullRequestURL extracts the host, owner and repository name from a
// pull request URL such as https://HOST/OWNER/REPO/pull/N.
func parsePullRequestURL(rawURL string) (host, owner, name string, err error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "", "", "", fmt.Errorf("unexpected pull request URL %q", rawURL)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return "", "", "", fmt.Errorf("unexpected pull request URL %q", rawURL)
	}
	return u.Host, parts[0], parts[1], nil
}

// ResolvePullRequest resolves the target pull request via `gh pr view` and
// prepares a GraphQL client for its host.
func (c *Client) ResolvePullRequest(ctx context.Context) (review.PullRequest, error) {
	stdout, stderr, err := gh.ExecContext(ctx, prViewArgs(c.target)...)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return review.PullRequest{}, fmt.Errorf("%w: %s", err, msg)
		}
		return review.PullRequest{}, err
	}
	var pr struct {
		ID      string `json:"id"`
		Number  int    `json:"number"`
		URL     string `json:"url"`
		BaseRef string `json:"baseRefName"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pr); err != nil {
		return review.PullRequest{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	host, owner, name, err := parsePullRequestURL(pr.URL)
	if err != nil {
		return review.PullRequest{}, err
	}
	c.owner, c.name, c.baseRef = owner, name, pr.BaseRef
	gql, err := api.NewGraphQLClient(api.ClientOptions{Host: host})
	if err != nil {
		return review.PullRequest{}, err
	}
	c.gql = gql
	return review.PullRequest{ID: pr.ID, Number: pr.Number, URL: pr.URL}, nil
}

var errNotResolved = errors.New("pull request is not resolved yet")

// ListFiles returns all files of the pull request, following pagination.
func (c *Client) ListFiles(ctx context.Context, pullRequestID string) ([]review.File, error) {
	var files []review.File
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
						Additions         int    `json:"additions"`
						Deletions         int    `json:"deletions"`
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
			files = append(files, review.File{
				Path:      n.Path,
				State:     parseViewedState(n.ViewerViewedState),
				Additions: n.Additions,
				Deletions: n.Deletions,
			})
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

func parseViewedState(s string) review.State {
	switch s {
	case "VIEWED":
		return review.Viewed
	case "DISMISSED":
		return review.Dismissed
	default:
		return review.Unviewed
	}
}

const repoFileQuery = `query RepoFile($owner: String!, $name: String!, $expression: String!) {
  repository(owner: $owner, name: $name) {
    object(expression: $expression) {
      ... on Blob { text isBinary }
    }
  }
}`

// RepoConfig reads the configuration file from the base branch of the pull
// request. Reading the base branch (not the PR head) means a pull request
// cannot change the presets it is reviewed with.
func (c *Client) RepoConfig(ctx context.Context) ([]byte, error) {
	if c.gql == nil {
		return nil, errNotResolved
	}
	var resp struct {
		Repository struct {
			Object *struct {
				Text     *string `json:"text"`
				IsBinary bool    `json:"isBinary"`
			} `json:"object"`
		} `json:"repository"`
	}
	vars := map[string]any{
		"owner":      c.owner,
		"name":       c.name,
		"expression": c.baseRef + ":" + config.RepoPath,
	}
	if err := c.gql.DoWithContext(ctx, repoFileQuery, vars, &resp); err != nil {
		return nil, err
	}
	obj := resp.Repository.Object
	if obj == nil || obj.Text == nil {
		return nil, nil
	}
	if obj.IsBinary {
		return nil, errors.New("file is binary")
	}
	return []byte(*obj.Text), nil
}
