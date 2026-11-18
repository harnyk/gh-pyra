package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/harnyk/gh-pyra/internal/config"
	"github.com/harnyk/gh-pyra/internal/filter"
)

func spec(t *testing.T, wildcard bool, args ...string) filter.Spec {
	t.Helper()
	s, err := filter.Parse(args, nil, wildcard)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type call struct {
	path   string
	viewed bool
}

type fakeClient struct {
	files   []File
	config  []byte
	failOn  string
	delay   time.Duration
	mu      sync.Mutex
	calls   []call
	active  atomic.Int32
	maxSeen atomic.Int32
}

func (f *fakeClient) ResolvePullRequest(context.Context) (PullRequest, error) {
	return PullRequest{ID: "PR_node_42", Number: 42}, nil
}

func (f *fakeClient) ListFiles(_ context.Context, id string) ([]File, error) {
	if id != "PR_node_42" {
		return nil, fmt.Errorf("unexpected PR id %q", id)
	}
	return f.files, nil
}

func (f *fakeClient) RepoConfig(context.Context) ([]byte, error) {
	return f.config, nil
}

func (f *fakeClient) SetViewed(_ context.Context, id, path string, viewed bool) error {
	n := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		m := f.maxSeen.Load()
		if n <= m || f.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(f.delay)
	if id != "PR_node_42" {
		return fmt.Errorf("unexpected PR id %q", id)
	}
	if path == f.failOn {
		return errors.New("boom")
	}
	f.mu.Lock()
	f.calls = append(f.calls, call{path, viewed})
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.path)
	}
	slices.Sort(out)
	return out
}

func sampleFiles() []File {
	return []File{
		{Path: "src/widget.spec.ts", State: Viewed},
		{Path: "src/xspecy.ts"},
		{Path: "test name.spec.ts"},
		{Path: "line\nbreak.spec.ts"},
		{Path: "src/widget.ts"},
	}
}

func run(t *testing.T, c *fakeClient, opts Options) (string, string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	r := &Runner{Client: c, Out: &out, Err: &errOut}
	err := r.Run(context.Background(), opts)
	return out.String(), errOut.String(), err
}

func assertContains(t *testing.T, s, want string) {
	t.Helper()
	if !strings.Contains(s, want) {
		t.Errorf("expected output to contain %q, got:\n%s", want, s)
	}
}

func assertNotContains(t *testing.T, s, unwanted string) {
	t.Helper()
	if strings.Contains(s, unwanted) {
		t.Errorf("did not expect output to contain %q, got:\n%s", unwanted, s)
	}
}

func TestHideByFilter(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, errOut, err := run(t, c, Options{Filter: spec(t, false, ".spec.")})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, errOut, "Found PR #42.")
	assertContains(t, errOut, "Marking test name.spec.ts as viewed...")
	assertContains(t, errOut, "Skipping src/widget.spec.ts (already viewed).")
	assertNotContains(t, errOut, "Marking src/widget.spec.ts")
	assertContains(t, out, "Marked 2 file(s) as viewed.")
	assertContains(t, out, "Skipped 1 already viewed file(s).")

	want := []string{"line\nbreak.spec.ts", "test name.spec.ts"}
	if got := c.paths(); !slices.Equal(got, want) {
		t.Errorf("marked %q, want %q", got, want)
	}
	for _, cl := range c.calls {
		if !cl.viewed {
			t.Errorf("expected %s to be marked viewed", cl.path)
		}
	}
}

func TestNoMatch(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, _, err := run(t, c, Options{Filter: spec(t, false, "no-match")})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 0 file(s) as viewed.")
	if len(c.calls) != 0 {
		t.Errorf("unexpected calls: %v", c.calls)
	}
}

func TestDryRun(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, errOut, err := run(t, c, Options{Filter: spec(t, false, ".spec."), DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, errOut, "Would mark test name.spec.ts as viewed.")
	assertNotContains(t, errOut, "Would mark src/widget.spec.ts")
	assertContains(t, out, "Would mark 2 file(s) as viewed.")
	assertContains(t, out, "Skipped 1 already viewed file(s).")
	if len(c.calls) != 0 {
		t.Errorf("dry run made calls: %v", c.calls)
	}
}

func TestUnhide(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, errOut, err := run(t, c, Options{Filter: spec(t, false, ".spec."), Unhide: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, errOut, "Unmarking src/widget.spec.ts as viewed...")
	assertContains(t, errOut, "Skipping test name.spec.ts (not viewed).")
	assertContains(t, out, "Unmarked 1 file(s) as viewed.")
	assertContains(t, out, "Skipped 2 not viewed file(s).")
	if len(c.calls) != 1 || c.calls[0] != (call{"src/widget.spec.ts", false}) {
		t.Errorf("unexpected calls: %v", c.calls)
	}
}

func TestUnhideDryRun(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, errOut, err := run(t, c, Options{Filter: spec(t, false, ".spec."), Unhide: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, errOut, "Would unmark src/widget.spec.ts as viewed.")
	assertContains(t, out, "Would unmark 1 file(s) as viewed.")
	if len(c.calls) != 0 {
		t.Errorf("dry run made calls: %v", c.calls)
	}
}

func TestAll(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, _, err := run(t, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 4 file(s) as viewed.")
	assertContains(t, out, "Skipped 1 already viewed file(s).")

	out, _, err = run(t, &fakeClient{files: sampleFiles()}, Options{Unhide: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Unmarked 1 file(s) as viewed.")
	assertContains(t, out, "Skipped 4 not viewed file(s).")
}

func TestConcurrencyLimit(t *testing.T) {
	var files []File
	for i := range 40 {
		files = append(files, File{Path: fmt.Sprintf("file%02d", i)})
	}
	c := &fakeClient{files: files, delay: 20 * time.Millisecond}
	out, _, err := run(t, c, Options{Concurrency: DefaultConcurrency})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 40 file(s) as viewed.")
	if got := c.maxSeen.Load(); got != DefaultConcurrency {
		t.Errorf("max parallel calls = %d, want %d", got, DefaultConcurrency)
	}
}

func TestMutationFailure(t *testing.T) {
	c := &fakeClient{files: sampleFiles(), failOn: "src/widget.ts"}
	out, _, err := run(t, c, Options{Concurrency: 1})
	if err == nil || !strings.Contains(err.Error(), "src/widget.ts") {
		t.Fatalf("expected error mentioning failed path, got %v", err)
	}
	assertContains(t, out, "before failing")
}

func TestCount(t *testing.T) {
	var out bytes.Buffer
	r := &Runner{Client: &fakeClient{files: sampleFiles()}, Out: &out, Err: &bytes.Buffer{}}
	if err := r.Count(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "visible: 4")
	assertContains(t, out.String(), "hidden : 1")
	assertContains(t, out.String(), "total  : 5")
}

func TestMultipleFilters(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, _, err := run(t, c, Options{Filter: spec(t, false, "test name", "widget.ts")})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 2 file(s) as viewed.")
	want := []string{"src/widget.ts", "test name.spec.ts"}
	if got := c.paths(); !slices.Equal(got, want) {
		t.Errorf("marked %q, want %q", got, want)
	}
}

func TestWildcardRun(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, _, err := run(t, c, Options{Filter: spec(t, true, "src/*.ts")})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 2 file(s) as viewed.")
	assertContains(t, out, "Skipped 1 already viewed file(s).")
	want := []string{"src/widget.ts", "src/xspecy.ts"}
	if got := c.paths(); !slices.Equal(got, want) {
		t.Errorf("marked %q, want %q", got, want)
	}
}

func TestExclude(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	out, _, err := run(t, c, Options{Filter: spec(t, false, "!.spec.")})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 2 file(s) as viewed.")
	want := []string{"src/widget.ts", "src/xspecy.ts"}
	if got := c.paths(); !slices.Equal(got, want) {
		t.Errorf("marked %q, want %q", got, want)
	}
}

func runner(c *fakeClient, user config.Set) (*Runner, *bytes.Buffer) {
	var out bytes.Buffer
	r := &Runner{Client: c, Out: &out, Err: &bytes.Buffer{}}
	if user != nil {
		r.UserPresets = func() (config.Set, error) { return user, nil }
	}
	return r, &out
}

func TestPresets(t *testing.T) {
	c := &fakeClient{
		files:  append(sampleFiles(), File{Path: "web/pnpm-lock.yaml"}, File{Path: "ui/a.snap"}),
		config: []byte("presets:\n  specs: ['*.spec.ts', '!test name*']\n"),
	}
	user, err := config.Parse([]byte("presets:\n  snaps: ['*.snap']\n"), "user.yml")
	if err != nil {
		t.Fatal(err)
	}
	r, out := runner(c, user)
	if err := r.Run(context.Background(), Options{Filter: spec(t, false, "@specs", "@lockfiles", "@snaps")}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "Marked 3 file(s) as viewed.")
	want := []string{"line\nbreak.spec.ts", "ui/a.snap", "web/pnpm-lock.yaml"}
	if got := c.paths(); !slices.Equal(got, want) {
		t.Errorf("marked %q, want %q", got, want)
	}

	out.Reset()
	if err := r.Presets(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"@lockfiles", "built-in", "@specs", ".github/pyra.yml", "@snaps", "user.yml", "!test name*"} {
		assertContains(t, out.String(), s)
	}
}

func TestUnknownPreset(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	_, _, err := run(t, c, Options{Filter: spec(t, false, "@nope")})
	if err == nil || !strings.Contains(err.Error(), "unknown preset @nope") {
		t.Fatalf("expected unknown preset error, got %v", err)
	}
	if len(c.calls) != 0 {
		t.Errorf("unexpected calls: %v", c.calls)
	}
}

func TestInvalidRepoConfig(t *testing.T) {
	c := &fakeClient{files: sampleFiles(), config: []byte("presets: [")}
	_, _, err := run(t, c, Options{Filter: spec(t, false, "@x")})
	if err == nil || !strings.Contains(err.Error(), ".github/pyra.yml") {
		t.Fatalf("expected config error, got %v", err)
	}
}

func TestPresetsNotLoadedWithoutReferences(t *testing.T) {
	c := &fakeClient{files: sampleFiles(), config: []byte("presets: [")}
	if _, _, err := run(t, c, Options{Filter: spec(t, false, ".spec.")}); err != nil {
		t.Fatalf("broken config must not matter without @presets: %v", err)
	}
}

func TestList(t *testing.T) {
	files := sampleFiles()
	files[1].State = Dismissed
	files[1].Additions, files[1].Deletions = 12, 3
	c := &fakeClient{files: files}

	r, out := runner(c, nil)
	if err := r.List(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 6 { // "line\nbreak" spans two lines
		t.Fatalf("unexpected output:\n%s", out.String())
	}
	assertContains(t, out.String(), "viewed    +0   -0  src/widget.spec.ts")
	assertContains(t, out.String(), "changed   +12  -3  src/xspecy.ts")

	out.Reset()
	if err := r.List(context.Background(), ListOptions{Filter: spec(t, false, "src/"), States: []State{Dismissed, Viewed}}); err != nil {
		t.Fatal(err)
	}
	assertContains(t, out.String(), "src/xspecy.ts")
	assertContains(t, out.String(), "src/widget.spec.ts")
	assertNotContains(t, out.String(), "src/widget.ts\n")
}

func TestParseState(t *testing.T) {
	for _, s := range States {
		got, err := ParseState(s.String())
		if err != nil || got != s {
			t.Errorf("ParseState(%q) = %v, %v", s, got, err)
		}
	}
	if _, err := ParseState("nope"); err == nil {
		t.Error("expected error")
	}
}

func TestDismissedIsHiddenAgain(t *testing.T) {
	c := &fakeClient{files: []File{{Path: "a", State: Dismissed}}}
	out, _, err := run(t, c, Options{})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 1 file(s) as viewed.")

	c = &fakeClient{files: []File{{Path: "a", State: Dismissed}}}
	out, _, err = run(t, c, Options{Unhide: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Unmarked 0 file(s) as viewed.")
}
