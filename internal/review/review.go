// Package review implements pull request review commands: marking files as
// viewed, listing and counting them.
package review

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"text/tabwriter"

	"golang.org/x/sync/errgroup"

	"github.com/harnyk/gh-pyra/internal/config"
	"github.com/harnyk/gh-pyra/internal/filter"
)

// DefaultConcurrency is the default number of files marked in parallel.
const DefaultConcurrency = 8

// PullRequest identifies the pull request being reviewed.
type PullRequest struct {
	ID     string
	Number int
	URL    string
}

// State is the viewer's viewed state of a file.
type State int

const (
	// Unviewed files have not been marked as viewed.
	Unviewed State = iota
	// Viewed files are marked as viewed.
	Viewed
	// Dismissed files were marked as viewed but changed since then.
	Dismissed
)

// States lists all states in display order.
var States = []State{Unviewed, Viewed, Dismissed}

func (s State) String() string {
	switch s {
	case Viewed:
		return "viewed"
	case Dismissed:
		return "changed"
	default:
		return "unviewed"
	}
}

// ParseState parses a state name as printed by String.
func ParseState(name string) (State, error) {
	for _, s := range States {
		if s.String() == name {
			return s, nil
		}
	}
	return 0, fmt.Errorf("unknown state %q (want unviewed, viewed or changed)", name)
}

// File is a pull request file together with its viewed state.
type File struct {
	Path      string
	State     State
	Additions int
	Deletions int
}

// Client is the subset of the GitHub API used by this package.
type Client interface {
	ResolvePullRequest(ctx context.Context) (PullRequest, error)
	ListFiles(ctx context.Context, pullRequestID string) ([]File, error)
	SetViewed(ctx context.Context, pullRequestID, path string, viewed bool) error
	// RepoConfig returns the repository configuration file from the pull
	// request's base branch, or nil if there is none. It must be called after
	// ResolvePullRequest.
	RepoConfig(ctx context.Context) ([]byte, error)
}

// Options controls a Run invocation.
type Options struct {
	// Filter selects files. An empty filter selects every file.
	Filter filter.Spec
	// Unhide unmarks files as viewed instead of marking them.
	Unhide bool
	// DryRun only reports what would be changed.
	DryRun bool
	// Concurrency is the maximum number of parallel API mutations.
	Concurrency int
}

// ListOptions controls a List invocation.
type ListOptions struct {
	// Filter selects files. An empty filter selects every file.
	Filter filter.Spec
	// States limits output to files in these states; empty means all.
	States []State
}

// Runner executes commands against a Client and writes human-readable output.
type Runner struct {
	Client Client
	// UserPresets loads the user's presets; nil means none.
	UserPresets func() (config.Set, error)
	// Out receives results.
	Out io.Writer
	// Err receives progress messages.
	Err io.Writer

	mu sync.Mutex
}

func (r *Runner) logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.Err, format, args...)
}

func (r *Runner) resolve(ctx context.Context, announce bool) (PullRequest, error) {
	pr, err := r.Client.ResolvePullRequest(ctx)
	if err != nil {
		return PullRequest{}, fmt.Errorf("resolve pull request: %w", err)
	}
	if announce {
		if pr.URL != "" {
			r.logf("Found PR #%d (%s).\n", pr.Number, pr.URL)
		} else {
			r.logf("Found PR #%d.\n", pr.Number)
		}
	}
	return pr, nil
}

// presets merges built-in, repository and user presets (later ones win).
func (r *Runner) presets(ctx context.Context) (config.Set, error) {
	repo := config.Set{}
	data, err := r.Client.RepoConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", config.RepoPath, err)
	}
	if data != nil {
		if repo, err = config.Parse(data, config.RepoPath); err != nil {
			return nil, err
		}
	}
	user := config.Set{}
	if r.UserPresets != nil {
		if user, err = r.UserPresets(); err != nil {
			return nil, err
		}
	}
	return config.Merge(config.Builtins(), repo, user), nil
}

// selector resolves the filter, loading presets only when it references any.
func (r *Runner) selector(ctx context.Context, spec filter.Spec) (filter.Selector, error) {
	var patterns map[string][]filter.Pattern
	if len(spec.Presets) > 0 {
		set, err := r.presets(ctx)
		if err != nil {
			return filter.Selector{}, err
		}
		patterns = set.Patterns()
	}
	return spec.Resolve(patterns)
}

func (r *Runner) selectedFiles(ctx context.Context, spec filter.Spec, announce bool) (PullRequest, []File, error) {
	pr, err := r.resolve(ctx, announce)
	if err != nil {
		return PullRequest{}, nil, err
	}
	sel, err := r.selector(ctx, spec)
	if err != nil {
		return PullRequest{}, nil, err
	}
	files, err := r.Client.ListFiles(ctx, pr.ID)
	if err != nil {
		return PullRequest{}, nil, fmt.Errorf("list pull request files: %w", err)
	}
	var out []File
	for _, f := range files {
		if sel.Select(f.Path) {
			out = append(out, f)
		}
	}
	return pr, out, nil
}

// Run marks (or unmarks) matching files as viewed.
func (r *Runner) Run(ctx context.Context, opts Options) error {
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}

	pr, files, err := r.selectedFiles(ctx, opts.Filter, true)
	if err != nil {
		return err
	}

	var targets []string
	skipped := 0
	for _, f := range files {
		switch {
		case opts.Unhide && f.State != Viewed:
			skipped++
			r.logf("Skipping %s (not viewed).\n", f.Path)
			continue
		case !opts.Unhide && f.State == Viewed:
			skipped++
			r.logf("Skipping %s (already viewed).\n", f.Path)
			continue
		}
		targets = append(targets, f.Path)
	}

	verb, past, skippedKind := "mark", "Marked", "already viewed"
	if opts.Unhide {
		verb, past, skippedKind = "unmark", "Unmarked", "not viewed"
	}

	var done atomic.Int64
	if opts.DryRun {
		for _, path := range targets {
			r.logf("Would %s %s as viewed.\n", verb, path)
		}
		fmt.Fprintf(r.Out, "Would %s %d file(s) as viewed.\n", verb, len(targets))
	} else {
		progress := "Marking"
		if opts.Unhide {
			progress = "Unmarking"
		}
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(concurrency)
		for _, path := range targets {
			g.Go(func() error {
				r.logf("%s %s as viewed...\n", progress, path)
				if err := r.Client.SetViewed(gctx, pr.ID, path, !opts.Unhide); err != nil {
					return fmt.Errorf("%s %s as viewed: %w", verb, path, err)
				}
				done.Add(1)
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			fmt.Fprintf(r.Out, "%s %d of %d file(s) as viewed before failing.\n", past, done.Load(), len(targets))
			return err
		}
		fmt.Fprintf(r.Out, "%s %d file(s) as viewed.\n", past, done.Load())
	}
	fmt.Fprintf(r.Out, "Skipped %d %s file(s).\n", skipped, skippedKind)
	return nil
}

// List prints matching files with their state and line changes.
func (r *Runner) List(ctx context.Context, opts ListOptions) error {
	_, files, err := r.selectedFiles(ctx, opts.Filter, false)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	for _, f := range files {
		if len(opts.States) > 0 && !containsState(opts.States, f.State) {
			continue
		}
		fmt.Fprintf(tw, "%s\t+%d\t-%d\t%s\n", f.State, f.Additions, f.Deletions, f.Path)
	}
	return tw.Flush()
}

func containsState(states []State, s State) bool {
	for _, x := range states {
		if x == s {
			return true
		}
	}
	return false
}

// Count prints how many files are viewed and not viewed.
func (r *Runner) Count(ctx context.Context) error {
	_, files, err := r.selectedFiles(ctx, filter.Spec{}, false)
	if err != nil {
		return err
	}
	hidden := 0
	for _, f := range files {
		if f.State == Viewed {
			hidden++
		}
	}
	fmt.Fprintf(r.Out, "visible: %d\n", len(files)-hidden)
	fmt.Fprintf(r.Out, "hidden : %d\n", hidden)
	fmt.Fprintf(r.Out, "total  : %d\n", len(files))
	return nil
}

// Presets prints the presets available for the pull request.
func (r *Runner) Presets(ctx context.Context) error {
	if _, err := r.resolve(ctx, false); err != nil {
		return err
	}
	set, err := r.presets(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(r.Out, 0, 0, 2, ' ', 0)
	for _, p := range set.Sorted() {
		fmt.Fprintf(tw, "@%s\t%s\t%s\n", p.Name, p.Source, strings.Join(p.Entries, " "))
	}
	return tw.Flush()
}
