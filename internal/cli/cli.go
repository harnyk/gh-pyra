// Package cli defines the gh-pyra command tree.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/harnyk/gh-pyra/internal/config"
	"github.com/harnyk/gh-pyra/internal/filter"
	"github.com/harnyk/gh-pyra/internal/github"
	"github.com/harnyk/gh-pyra/internal/review"
)

// ClientFactory creates the GitHub client for the selected pull request. It is
// called lazily, only after arguments are valid.
type ClientFactory func(target github.Target) (review.Client, error)

// clientFunc creates a client for the target chosen by global flags.
type clientFunc func() (review.Client, error)

// UsageError signals invalid command-line usage (exit code 2).
type UsageError struct{ error }

func usageErrorf(format string, args ...any) error {
	return UsageError{fmt.Errorf(format, args...)}
}

func noArgs(name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != 0 {
			return usageErrorf("%s takes no arguments", name)
		}
		return nil
	}
}

// NewRootCmd builds the `gh pyra` command.
func NewRootCmd(version string, newClientFor ClientFactory) *cobra.Command {
	var target github.Target
	newClient := func() (review.Client, error) { return newClientFor(target) }

	root := &cobra.Command{
		Use: "pyra",
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh pyra",
		},
		Short:         "Swiss army knife for GitHub pull requests",
		Long:          "Swiss army knife for GitHub pull requests.\n\nCommands operate on the pull request of the current branch unless --pr is given.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if target.Repo != "" && target.PR == "" {
				return usageErrorf("--repo requires --pr")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return cmd.Help()
			}
			msg := fmt.Sprintf("unknown command %q", args[0])
			if s := cmd.SuggestionsFor(args[0]); len(s) > 0 {
				msg += fmt.Sprintf(" (did you mean %s?)", strings.Join(s, ", "))
			}
			return UsageError{errors.New(msg)}
		},
	}
	pf := root.PersistentFlags()
	pf.StringVar(&target.PR, "pr", "", "`PR` number, URL or branch (default: PR of the current branch)")
	pf.StringVarP(&target.Repo, "repo", "R", "", "select another repository using the `[HOST/]OWNER/REPO` format (requires --pr)")

	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return UsageError{err} })
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(
		newMarkCmd(newClient, false),
		newMarkCmd(newClient, true),
		newListCmd(newClient),
		newCountCmd(newClient),
		newPresetsCmd(newClient),
	)
	return root
}

func newRunner(cmd *cobra.Command, newClient clientFunc) (*review.Runner, error) {
	client, err := newClient()
	if err != nil {
		return nil, err
	}
	return &review.Runner{
		Client:      client,
		UserPresets: config.LoadUser,
		Out:         cmd.OutOrStdout(),
		Err:         cmd.ErrOrStderr(),
	}, nil
}

const filterHelp = `Filters:
  PATTERN     include files matching PATTERN
  !PATTERN    exclude files matching PATTERN (also: --exclude PATTERN)
  @NAME       include the patterns of preset NAME (see "gh pyra presets")
A file is selected when it matches any include and no exclude. With only
excludes, every other file is selected. Start a filter with \! or \@ to
match a literal "!" or "@".

By default PATTERN is a literal substring of the file path (not a regex).
With --wildcard, PATTERN is a glob pattern matched against the whole path:
  *       any characters except /
  **      any characters including / (whole path segments)
  ?       one character except /
  [abc]   one character from the set ([!abc] negates)
  {a,b}   either alternative
A pattern without / is also matched against the file name, so '*.spec.ts'
selects such files in any directory. Preset patterns are always globs.
Quote filters to keep the shell from expanding them.`

type filterFlags struct {
	wildcard bool
	excludes []string
}

func (f *filterFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&f.wildcard, "wildcard", "w", false, "treat patterns as globs instead of substrings")
	cmd.Flags().StringArrayVarP(&f.excludes, "exclude", "x", nil, "exclude files matching `PATTERN` (repeatable)")
}

func (f *filterFlags) parse(args []string) (filter.Spec, error) {
	spec, err := filter.Parse(args, f.excludes, f.wildcard)
	if err != nil {
		return filter.Spec{}, UsageError{err}
	}
	return spec, nil
}

// newMarkCmd builds `hide` (mark as viewed) or `show` (unmark as viewed).
func newMarkCmd(newClient clientFunc, unhide bool) *cobra.Command {
	var (
		ff   filterFlags
		all  bool
		opts = review.Options{Unhide: unhide}
	)

	cmd := &cobra.Command{
		Use:   "hide FILTER... | --all",
		Short: "Mark matching pull request files as viewed",
		Long:  "Mark matching pull request files as viewed, collapsing them in the review UI.\n\n" + filterHelp,
		Example: `  gh pyra hide '.spec.' 'pnpm-lock.yaml'
  gh pyra hide -w '*.spec.ts' 'generated/**'
  gh pyra hide @lockfiles @snapshots
  gh pyra hide 'src/' '!src/core/'
  gh pyra hide --all -x 'src/core/' --dry-run`,
	}
	if unhide {
		cmd.Use = "show FILTER... | --all"
		cmd.Aliases = []string{"unhide"}
		cmd.Short = "Unmark matching pull request files as viewed"
		cmd.Long = "Unmark matching pull request files as viewed, expanding them again in the review UI.\n\n" + filterHelp
		cmd.Example = `  gh pyra show '.spec.'
  gh pyra show -w 'src/**'
  gh pyra show --all`
	}

	cmd.Args = func(cmd *cobra.Command, args []string) error {
		spec, err := ff.parse(args)
		if err != nil {
			return err
		}
		switch {
		case all && spec.HasIncludes():
			return usageErrorf("include filters cannot be used with --all (excludes can)")
		case !all && spec.IsEmpty():
			return usageErrorf("at least one FILTER is required (or --all)")
		}
		if opts.Concurrency < 1 {
			return usageErrorf("--concurrency must be at least 1")
		}
		opts.Filter = spec
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		r, err := newRunner(cmd, newClient)
		if err != nil {
			return err
		}
		return r.Run(cmd.Context(), opts)
	}

	ff.register(cmd)
	f := cmd.Flags()
	f.BoolVarP(&all, "all", "a", false, "process every file in the pull request")
	f.BoolVarP(&opts.DryRun, "dry-run", "n", false, "show what would be changed without calling the API")
	f.IntVarP(&opts.Concurrency, "concurrency", "j", review.DefaultConcurrency, "number of files to update in parallel")
	return cmd
}

func newListCmd(newClient clientFunc) *cobra.Command {
	var (
		ff     filterFlags
		states []string
		opts   review.ListOptions
	)
	cmd := &cobra.Command{
		Use:     "ls [FILTER...]",
		Aliases: []string{"list"},
		Short:   "List pull request files with their viewed state",
		Long: `List pull request files with their viewed state and line changes.

States:
  unviewed   not marked as viewed
  viewed     marked as viewed
  changed    marked as viewed, but the file changed since then

Without filters every file is listed.

` + filterHelp,
		Example: `  gh pyra ls
  gh pyra ls -s changed
  gh pyra ls -w '*.go' -x vendor/`,
		Args: func(cmd *cobra.Command, args []string) error {
			spec, err := ff.parse(args)
			if err != nil {
				return err
			}
			opts.Filter = spec
			opts.States = nil
			for _, s := range states {
				st, err := review.ParseState(s)
				if err != nil {
					return UsageError{err}
				}
				opts.States = append(opts.States, st)
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRunner(cmd, newClient)
			if err != nil {
				return err
			}
			return r.List(cmd.Context(), opts)
		},
	}
	ff.register(cmd)
	cmd.Flags().StringSliceVarP(&states, "state", "s", nil, "only list files in `STATE` (unviewed, viewed, changed; repeatable)")
	return cmd
}

func newCountCmd(newClient clientFunc) *cobra.Command {
	return &cobra.Command{
		Use:   "count",
		Short: "Count viewed and not viewed pull request files",
		Args:  noArgs("count"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRunner(cmd, newClient)
			if err != nil {
				return err
			}
			return r.Count(cmd.Context())
		},
	}
}

func newPresetsCmd(newClient clientFunc) *cobra.Command {
	userPath, _ := config.UserPath()
	return &cobra.Command{
		Use:   "presets",
		Short: "List filter presets available for the pull request",
		Long: fmt.Sprintf(`List filter presets available for the pull request.

Presets are named lists of glob patterns, referenced as @NAME in filters.
They come from (later sources override earlier ones):
  1. built-in presets
  2. %s on the base branch of the pull request
  3. the user configuration file (%s; override with $GH_PYRA_CONFIG)

Configuration format:
  presets:
    noise:
      - pnpm-lock.yaml
      - '**/*.snap'
      - '!src/keep.snap'`, config.RepoPath, userPath),
		Args: noArgs("presets"),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := newRunner(cmd, newClient)
			if err != nil {
				return err
			}
			return r.Presets(cmd.Context())
		},
	}
}
