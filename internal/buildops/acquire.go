// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package buildops

import (
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// Acquire runs the shared build orchestration in acquisition mode with the
// rendered artifacts written to a temporary directory that is removed
// afterwards — the pipeline is reused whole, never forked, and the real output
// directory is never touched.
//
// It returns the external inputs this run newly pinned, in the order the render
// requested them, and empty when the run pinned nothing. The dispatch has
// already merged them into the lockfile; reporting them is the caller's, so the
// wording belongs to the surface a reader is looking at.
//
// The rendered-output suppression is part of the contract rather than a caller's
// choice: the run's status stream is pointed at a discard sink along with the
// artifacts, so a caller's own report is the only thing a reader sees. The trace
// is off for the same reason — an acquisition pass exists to pin inputs, not to
// leave intermediate files behind.
func Acquire(cfg Config) ([]lockfile.ExternalInput, error) {
	discardDir, err := os.MkdirTemp("", "aggrega-acquire-")
	if err != nil {
		return nil, fmt.Errorf("create discard output directory: %w", err)
	}
	defer os.RemoveAll(discardDir)

	var acquired []lockfile.ExternalInput
	cfg.TraceEnabled = false
	cfg.Options.OutDir = discardDir
	cfg.Options.OnAcquired = func(recs []lockfile.ExternalInput) { acquired = recs }
	cfg.Out = io.Discard

	if _, err := Run(cfg); err != nil {
		return nil, err
	}
	return acquired, nil
}
