// Command gh-pyra is a GitHub CLI extension: a swiss army knife for pull requests.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/harnyk/gh-pyra/internal/cli"
	"github.com/harnyk/gh-pyra/internal/github"
	"github.com/harnyk/gh-pyra/internal/hide"
)

// Set by goreleaser via -ldflags.
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := cli.NewRootCmd(version, func() (hide.Client, error) { return github.New() })
	cmd, err := root.ExecuteContextC(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		if _, ok := errors.AsType[cli.UsageError](err); ok {
			fmt.Fprint(os.Stderr, "\n"+cmd.UsageString())
			os.Exit(2)
		}
		os.Exit(1)
	}
}
