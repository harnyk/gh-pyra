package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/harnyk/gh-pyra/internal/github"
	"github.com/harnyk/gh-pyra/internal/hide"
)

type fakeClient struct {
	target   github.Target
	marked   []string
	unmarked []string
}

func (f *fakeClient) ResolvePullRequest(context.Context) (hide.PullRequest, error) {
	return hide.PullRequest{ID: "PR_1", Number: 1}, nil
}

func (f *fakeClient) ListFiles(context.Context, string) ([]hide.File, error) {
	return []hide.File{{Path: "a.spec.ts"}, {Path: "b.ts", Viewed: true}}, nil
}

func (f *fakeClient) SetViewed(_ context.Context, _, path string, viewed bool) error {
	if viewed {
		f.marked = append(f.marked, path)
	} else {
		f.unmarked = append(f.unmarked, path)
	}
	return nil
}

func execute(args ...string) (*fakeClient, string, error) {
	c := &fakeClient{}
	cmd := NewRootCmd("test", func(t github.Target) (hide.Client, error) {
		c.target = t
		return c, nil
	})
	var out bytes.Buffer
	cmd.SetArgs(args)
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	return c, out.String(), err
}

func TestSubcommands(t *testing.T) {
	c, out, err := execute("hide", ".spec.")
	if err != nil || len(c.marked) != 1 || c.marked[0] != "a.spec.ts" {
		t.Fatalf("hide: err=%v marked=%v", err, c.marked)
	}
	if !strings.Contains(out, "Marked 1 file(s) as viewed.") {
		t.Errorf("hide output: %s", out)
	}

	for _, name := range []string{"show", "unhide"} {
		c, _, err = execute(name, "--all")
		if err != nil || len(c.unmarked) != 1 || c.unmarked[0] != "b.ts" {
			t.Fatalf("%s: err=%v unmarked=%v", name, err, c.unmarked)
		}
	}

	_, out, err = execute("count")
	if err != nil || !strings.Contains(out, "total  : 2") {
		t.Fatalf("count: err=%v out=%s", err, out)
	}
}

func TestInvalidArgsAreUsageErrors(t *testing.T) {
	cases := map[string][]string{
		"unknown command":       {"hid"},
		"hide without filter":   {"hide"},
		"empty filter":          {"hide", ""},
		"empty among filters":   {"hide", "a", ""},
		"all with filter":       {"hide", "--all", ".spec."},
		"bad wildcard":          {"show", "-w", "[a"},
		"wildcard with all":     {"hide", "-w", "--all"},
		"count with args":       {"count", "x"},
		"unknown flag":          {"hide", "--nope", "a"},
		"zero concurrency":      {"hide", "-j", "0", "a"},
		"invalid concurrency":   {"show", "--concurrency", "x", "a"},
		"mark flag on count":    {"count", "--all"},
		"unknown root flag":     {"--nope"},
		"old style hide syntax": {".spec."},
		"repo without pr":       {"hide", "-R", "o/r", "a"},
		"repo without pr count": {"count", "--repo", "o/r"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			called := false
			cmd := NewRootCmd("test", func(github.Target) (hide.Client, error) {
				called = true
				return nil, errors.New("must not be called")
			})
			cmd.SetArgs(args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			_, err := cmd.ExecuteC()
			if _, ok := errors.AsType[UsageError](err); !ok {
				t.Fatalf("expected usage error, got %v", err)
			}
			if called {
				t.Fatal("client was created for invalid arguments")
			}
		})
	}
}

func TestTargetFlags(t *testing.T) {
	cases := []struct {
		args []string
		want github.Target
	}{
		{[]string{"count"}, github.Target{}},
		{[]string{"count", "--pr", "12"}, github.Target{PR: "12"}},
		{[]string{"--pr", "12", "-R", "o/r", "hide", "x"}, github.Target{PR: "12", Repo: "o/r"}},
		{[]string{"show", "--all", "--pr", "https://github.com/o/r/pull/3"}, github.Target{PR: "https://github.com/o/r/pull/3"}},
		{[]string{"hide", "-R", "ghe.example.com/o/r", "--pr", "feature", "x"}, github.Target{PR: "feature", Repo: "ghe.example.com/o/r"}},
	}
	for _, tc := range cases {
		c, _, err := execute(tc.args...)
		if err != nil {
			t.Fatalf("%v: %v", tc.args, err)
		}
		if c.target != tc.want {
			t.Errorf("%v: target = %+v, want %+v", tc.args, c.target, tc.want)
		}
	}
}
