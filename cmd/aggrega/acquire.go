// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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

// dispatchAcquire runs the shared build dispatch in acquisition mode with the
// rendered artifacts written to a temporary directory that is removed
// afterwards — the pipeline is reused whole, never forked, and dist/ is never
// touched. After a successful run it prints one line per newly-pinned external
// input (the dispatch already merged the pins into the lockfile) and a
// summary naming the count.
func dispatchAcquire(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string, diagOut, out io.Writer) error {
	discardDir, err := os.MkdirTemp("", "aggrega-acquire-")
	if err != nil {
		return fmt.Errorf("create discard output directory: %w", err)
	}
	defer os.RemoveAll(discardDir)

	var acquired []lockfile.ExternalInput
	run := buildRunOptions{
		outDir:     discardDir,
		onAcquired: func(recs []lockfile.ExternalInput) { acquired = recs },
	}
	// Rendered-output suppression: the build's stdout summary goes to
	// io.Discard along with the artifacts; acquire's own report below is
	// the command's stdout surface.
	if err := dispatchBuildWithDiag(rw, releasePath, catalogFlagDirs, false, buildDiagOptions{}, run, diagOut, io.Discard); err != nil {
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
