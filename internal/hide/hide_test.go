package hide

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
)

type call struct {
	path   string
	viewed bool
}

type fakeClient struct {
	files   []File
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
		{Path: "src/widget.spec.ts", Viewed: true},
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
	out, errOut, err := run(t, c, Options{Filters: []string{".spec."}})
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
	out, _, err := run(t, c, Options{Filters: []string{"no-match"}})
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
	out, errOut, err := run(t, c, Options{Filters: []string{".spec."}, DryRun: true})
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
	out, errOut, err := run(t, c, Options{Filters: []string{".spec."}, Unhide: true})
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
	out, errOut, err := run(t, c, Options{Filters: []string{".spec."}, Unhide: true, DryRun: true})
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
	out, _, err := run(t, c, Options{All: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Marked 4 file(s) as viewed.")
	assertContains(t, out, "Skipped 1 already viewed file(s).")

	out, _, err = run(t, &fakeClient{files: sampleFiles()}, Options{All: true, Unhide: true})
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, out, "Unmarked 1 file(s) as viewed.")
	assertContains(t, out, "Skipped 4 not viewed file(s).")
}

func TestEmptyFilterRejected(t *testing.T) {
	c := &fakeClient{files: sampleFiles()}
	if _, _, err := run(t, c, Options{}); err == nil {
		t.Fatal("expected error for empty filter")
	}
}

func TestConcurrencyLimit(t *testing.T) {
	var files []File
	for i := range 40 {
		files = append(files, File{Path: fmt.Sprintf("file%02d", i)})
	}
	c := &fakeClient{files: files, delay: 20 * time.Millisecond}
	out, _, err := run(t, c, Options{All: true, Concurrency: DefaultConcurrency})
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
	out, _, err := run(t, c, Options{All: true, Concurrency: 1})
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
	out, _, err := run(t, c, Options{Filters: []string{"test name", "widget.ts"}})
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
	out, _, err := run(t, c, Options{Filters: []string{"src/*.ts"}, Wildcard: true})
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
