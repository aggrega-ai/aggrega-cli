// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

func newAcquireCmd() *cobra.Command {
	var acquireRelease string
	var acquireCatalogDirs []string
	c := &cobra.Command{
		Use:   "acquire",
		Short: "Pin the external inputs a Release's render consumes",
		Long: "Acquire runs the Release's build pipeline in acquisition mode with rendered-" +
			"artifact output discarded: every external input the render requests is fetched, " +
			"pinned into the content-addressed input store, and recorded in the Release's " +
			"lockfile, so a subsequent build serves those inputs from the store without " +
			"refetching. One line is printed per newly-pinned URL.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return dispatchAcquire(rw, acquireRelease, acquireCatalogDirs, os.Stderr, os.Stdout)
		},
	}
	c.Flags().StringVar(&acquireRelease, "release", "", "Path to Release YAML file (required)")
	c.Flags().StringArrayVar(&acquireCatalogDirs, "catalog", nil, "Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Defaults to the current directory.")
	if err := c.MarkFlagRequired("release"); err != nil {
		panic(err)
	}
	return c
}

// dispatchAcquire runs the shared acquisition operation and reports what it
// pinned in this command's voice: one line per newly-pinned external input (the
// operation already merged the pins into the lockfile) and a summary naming the
// count.
//
// The progress surface is left off — a nil reporter — so acquire's own report on
// stdout is the whole of what a reader sees; the preview's advisories still
// reach the diagnostic stream, because a deprecated pin is worth saying whatever
// the operator asked the run to do.
func dispatchAcquire(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, diagOut, out io.Writer) error {
	acquired, err := buildops.Acquire(
		buildDispatchConfig(rw, releasePath, catalogFlagDirs, false, buildops.RunOptions{}, nil, diagOut, io.Discard))
	if err != nil {
		return err
	}

	for _, rec := range acquired {
		fmt.Fprintf(out, "pinned %s sha256:%s (%d bytes)\n", rec.URL, rec.Sha256, rec.Size)
	}
	if len(acquired) == 0 {
		fmt.Fprintln(out, "Acquire complete: no new external inputs (dependencies already pinned)")
		return nil
	}
	fmt.Fprintf(out, "Acquire complete: %d external input(s) pinned\n", len(acquired))
	return nil
}
