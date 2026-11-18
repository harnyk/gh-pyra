// Package cli defines the gh-pyra command tree.
package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/harnyk/gh-pyra/internal/hide"
)

// ClientFactory creates the GitHub client lazily, only after arguments are valid.
type ClientFactory func() (hide.Client, error)

// UsageError signals invalid command-line usage (exit code 2).
type UsageError struct{ error }

func usageErrorf(format string, args ...any) error {
	return UsageError{fmt.Errorf(format, args...)}
}

// NewRootCmd builds the `gh pyra` command.
func NewRootCmd(version string, newClient ClientFactory) *cobra.Command {
	root := &cobra.Command{
		Use: "pyra",
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "gh pyra",
		},
		Short:         "Swiss army knife for GitHub pull requests",
		Long:          "Swiss army knife for GitHub pull requests.\n\nCommands operate on the pull request of the current branch.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
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
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return UsageError{err} })
	root.CompletionOptions.HiddenDefaultCmd = true

	root.AddCommand(
		newMarkCmd(newClient, false),
		newMarkCmd(newClient, true),
		newCountCmd(newClient),
	)
	return root
}

const filterHelp = `By default FILTER is a literal substring of the file path (not a regex).
With --wildcard, FILTER is a glob pattern matched against the whole path:
  *       any characters except /
  **      any characters including / (whole path segments)
  ?       one character except /
  [abc]   one character from the set ([!abc] negates)
  {a,b}   either alternative
A pattern without / is also matched against the file name, so '*.spec.ts'
selects such files in any directory. Quote patterns to keep the shell from
expanding them.

Several FILTERs may be given; a file is selected when it matches any of them.
With --all, process every file in the pull request (FILTER must be omitted).`

// newMarkCmd builds `hide` (mark as viewed) or `show` (unmark as viewed).
func newMarkCmd(newClient ClientFactory, unhide bool) *cobra.Command {
	opts := hide.Options{Unhide: unhide}

	cmd := &cobra.Command{
		Use:   "hide [--wildcard] FILTER... | --all",
		Short: "Mark matching pull request files as viewed",
		Long:  "Mark matching pull request files as viewed, collapsing them in the review UI.\n\n" + filterHelp,
		Example: `  gh pyra hide '.spec.' 'pnpm-lock.yaml'
  gh pyra hide -w '*.spec.ts' 'generated/**'
  gh pyra hide --all --dry-run`,
	}
	if unhide {
		cmd.Use = "show [--wildcard] FILTER... | --all"
		cmd.Aliases = []string{"unhide"}
		cmd.Short = "Unmark matching pull request files as viewed"
		cmd.Long = "Unmark matching pull request files as viewed, expanding them again in the review UI.\n\n" + filterHelp
		cmd.Example = `  gh pyra show '.spec.'
  gh pyra show -w 'src/**'
  gh pyra show --all`
	}

	cmd.Args = func(cmd *cobra.Command, args []string) error {
		switch {
		case opts.All:
			if len(args) != 0 {
				return usageErrorf("FILTER must be omitted with --all")
			}
			if opts.Wildcard {
				return usageErrorf("--wildcard cannot be used with --all")
			}
		case len(args) == 0:
			return usageErrorf("at least one FILTER is required (or --all)")
		default:
			if _, err := hide.NewMatcher(args, opts.Wildcard); err != nil {
				return UsageError{err}
			}
		}
		if opts.Concurrency < 1 {
			return usageErrorf("--concurrency must be at least 1")
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		client, err := newClient()
		if err != nil {
			return err
		}
		opts.Filters = args
		r := &hide.Runner{Client: client, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
		return r.Run(cmd.Context(), opts)
	}

	f := cmd.Flags()
	f.BoolVarP(&opts.Wildcard, "wildcard", "w", false, "treat FILTERs as glob patterns instead of substrings")
	f.BoolVarP(&opts.All, "all", "a", false, "process every file in the pull request")
	f.BoolVarP(&opts.DryRun, "dry-run", "n", false, "show what would be changed without calling the API")
	f.IntVarP(&opts.Concurrency, "concurrency", "j", hide.DefaultConcurrency, "number of files to update in parallel")
	return cmd
}

func newCountCmd(newClient ClientFactory) *cobra.Command {
	return &cobra.Command{
		Use:   "count",
		Short: "Count viewed and not viewed pull request files",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 0 {
				return usageErrorf("count takes no arguments")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := newClient()
			if err != nil {
				return err
			}
			r := &hide.Runner{Client: client, Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr()}
			return r.Count(cmd.Context())
		},
	}
}
