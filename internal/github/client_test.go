package github

import (
	"slices"
	"testing"
)

func TestPrViewArgs(t *testing.T) {
	cases := []struct {
		target Target
		want   []string
	}{
		{Target{}, []string{"pr", "view", "--json", "id,number,url"}},
		{Target{PR: "123"}, []string{"pr", "view", "123", "--json", "id,number,url"}},
		{Target{PR: "feature", Repo: "o/r"}, []string{"pr", "view", "feature", "--repo", "o/r", "--json", "id,number,url"}},
	}
	for _, tc := range cases {
		if got := prViewArgs(tc.target); !slices.Equal(got, tc.want) {
			t.Errorf("prViewArgs(%+v) = %q, want %q", tc.target, got, tc.want)
		}
	}
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/o/r/pull/1":         "github.com",
		"https://ghe.example.com/o/r/pull/7":    "ghe.example.com",
		"https://ghe.example.com:8443/o/r/pull": "ghe.example.com:8443",
	}
	for in, want := range cases {
		got, err := hostFromURL(in)
		if err != nil || got != want {
			t.Errorf("hostFromURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "not a url", "/o/r/pull/1"} {
		if _, err := hostFromURL(bad); err == nil {
			t.Errorf("hostFromURL(%q) succeeded", bad)
		}
	}
}
