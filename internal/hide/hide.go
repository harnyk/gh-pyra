// Package hide implements marking pull request files as viewed or not viewed.
package hide

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// DefaultConcurrency is the default number of files marked in parallel.
const DefaultConcurrency = 8

// PullRequest identifies the pull request of the current branch.
type PullRequest struct {
	ID     string
	Number int
	URL    string
}

// File is a pull request file together with its viewed state.
type File struct {
	Path   string
	Viewed bool
}

// Client is the subset of the GitHub API used by this package.
type Client interface {
	ResolvePullRequest(ctx context.Context) (PullRequest, error)
	ListFiles(ctx context.Context, pullRequestID string) ([]File, error)
	SetViewed(ctx context.Context, pullRequestID, path string, viewed bool) error
}

// Options controls a Run invocation.
type Options struct {
	// Filters select files; a file is selected when it matches any of them.
	// Ignored when All is set.
	Filters []string
	// Wildcard treats Filters as glob patterns instead of literal substrings.
	Wildcard bool
	// All processes every file in the pull request.
	All bool
	// Unhide unmarks files as viewed instead of marking them.
	Unhide bool
	// DryRun only reports what would be changed.
	DryRun bool
	// Concurrency is the maximum number of parallel API mutations.
	Concurrency int
}

// Runner executes commands against a Client and writes human-readable output.
type Runner struct {
	Client Client
	// Out receives the final summary.
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

// Run marks (or unmarks) matching files as viewed.
func (r *Runner) Run(ctx context.Context, opts Options) error {
	match := func(string) bool { return true }
	if !opts.All {
		m, err := NewMatcher(opts.Filters, opts.Wildcard)
		if err != nil {
			return err
		}
		match = m
	}
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = DefaultConcurrency
	}

	pr, err := r.Client.ResolvePullRequest(ctx)
	if err != nil {
		return fmt.Errorf("resolve pull request: %w", err)
	}
	if pr.URL != "" {
		r.logf("Found PR #%d (%s).\n", pr.Number, pr.URL)
	} else {
		r.logf("Found PR #%d.\n", pr.Number)
	}

	files, err := r.Client.ListFiles(ctx, pr.ID)
	if err != nil {
		return fmt.Errorf("list pull request files: %w", err)
	}

	var targets []string
	skipped := 0
	for _, f := range files {
		if !match(f.Path) {
			continue
		}
		switch {
		case opts.Unhide && !f.Viewed:
			skipped++
			r.logf("Skipping %s (not viewed).\n", f.Path)
			continue
		case !opts.Unhide && f.Viewed:
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

// Count prints how many files are viewed and not viewed.
func (r *Runner) Count(ctx context.Context) error {
	pr, err := r.Client.ResolvePullRequest(ctx)
	if err != nil {
		return fmt.Errorf("resolve pull request: %w", err)
	}
	files, err := r.Client.ListFiles(ctx, pr.ID)
	if err != nil {
		return fmt.Errorf("list pull request files: %w", err)
	}
	hidden := 0
	for _, f := range files {
		if f.Viewed {
			hidden++
		}
	}
	fmt.Fprintf(r.Out, "visible: %d\n", len(files)-hidden)
	fmt.Fprintf(r.Out, "hidden : %d\n", hidden)
	fmt.Fprintf(r.Out, "total  : %d\n", len(files))
	return nil
}
