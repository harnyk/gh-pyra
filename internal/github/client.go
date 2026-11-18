// Package github implements hide.Client on top of the GitHub CLI.
package github

import (
	"context"
	"encoding/json"
	"fmt"
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

// Client talks to GitHub using the credentials of the authenticated gh CLI.
type Client struct {
	gql *api.GraphQLClient
}

// New creates a Client using gh's default host and authentication.
func New() (*Client, error) {
	gql, err := api.DefaultGraphQLClient()
	if err != nil {
		return nil, err
	}
	return &Client{gql: gql}, nil
}

// CurrentPullRequest resolves the pull request of the current branch via `gh pr view`.
func (c *Client) CurrentPullRequest(ctx context.Context) (hide.PullRequest, error) {
	stdout, stderr, err := gh.ExecContext(ctx, "pr", "view", "--json", "id,number")
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return hide.PullRequest{}, fmt.Errorf("%w: %s", err, msg)
		}
		return hide.PullRequest{}, err
	}
	var pr struct {
		ID     string `json:"id"`
		Number int    `json:"number"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &pr); err != nil {
		return hide.PullRequest{}, fmt.Errorf("parse gh pr view output: %w", err)
	}
	return hide.PullRequest{ID: pr.ID, Number: pr.Number}, nil
}

// ListFiles returns all files of the pull request, following pagination.
func (c *Client) ListFiles(ctx context.Context, pullRequestID string) ([]hide.File, error) {
	var files []hide.File
	var cursor *string
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
	mutation := unmarkMutation
	if viewed {
		mutation = markMutation
	}
	vars := map[string]any{"pullRequestId": pullRequestID, "path": path}
	return c.gql.DoWithContext(ctx, mutation, vars, nil)
}
