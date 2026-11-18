package github

import (
	"slices"
	"testing"

	"github.com/harnyk/gh-pyra/internal/review"
)

func TestPrViewArgs(t *testing.T) {
	cases := []struct {
		target Target
		want   []string
	}{
		{Target{}, []string{"pr", "view", "--json", "id,number,url,baseRefName"}},
		{Target{PR: "123"}, []string{"pr", "view", "123", "--json", "id,number,url,baseRefName"}},
		{Target{PR: "feature", Repo: "o/r"}, []string{"pr", "view", "feature", "--repo", "o/r", "--json", "id,number,url,baseRefName"}},
	}
	for _, tc := range cases {
		if got := prViewArgs(tc.target); !slices.Equal(got, tc.want) {
			t.Errorf("prViewArgs(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestParsePullRequestURL(t *testing.T) {
	cases := map[string][3]string{
		"https://github.com/o/r/pull/1":            {"github.com", "o", "r"},
		"https://ghe.example.com/org/repo/pull/7":  {"ghe.example.com", "org", "repo"},
		"https://ghe.example.com:8443/o/r/pull/9/": {"ghe.example.com:8443", "o", "r"},
	}
	for in, want := range cases {
		h, o, n, err := parsePullRequestURL(in)
		if err != nil || [3]string{h, o, n} != want {
			t.Errorf("parsePullRequestURL(%q) = %q %q %q, %v; want %q", in, h, o, n, err, want)
		}
	}
	for _, bad := range []string{"", "not a url", "/o/r/pull/1", "https://github.com/o/r", "https://github.com/o/r/issues/1"} {
		if _, _, _, err := parsePullRequestURL(bad); err == nil {
			t.Errorf("parsePullRequestURL(%q) succeeded", bad)
		}
	}
}

func TestParseViewedState(t *testing.T) {
	cases := map[string]review.State{"VIEWED": review.Viewed, "DISMISSED": review.Dismissed, "UNVIEWED": review.Unviewed, "": review.Unviewed}
	for in, want := range cases {
		if got := parseViewedState(in); got != want {
			t.Errorf("parseViewedState(%q) = %v, want %v", in, got, want)
		}
	}
}
