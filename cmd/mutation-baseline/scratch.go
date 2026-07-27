// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

// setupScratch creates an isolated working copy of srcDir under $TMPDIR
// and returns the scratch root. The mutation runner operates on this
// scratch copy; the original source tree at srcDir is never modified.
//
// Layout under the returned scratch root:
//
//	<scratchDir>/                  — full copy of srcDir (.git, dist,
//	                                 .trace, node_modules excluded)
//	<scratchDir>/.fake-home/       — empty $HOME for `go test` subprocesses
//	                                 so any tool that writes under $HOME
//	                                 cannot pollute the operator's home
//
// Symlinks under srcDir are not followed and not copied. Encountering one
// fails the copy fast: a mutation run against a symlink-resolved source
// tree could mutate files outside the scratch root.
//
// The caller is responsible for cleanup. The recommended pattern is:
//
//	scratch, err := setupScratch(src)
//	if err != nil { ... }
//	defer os.RemoveAll(scratch)
//	installSignalHandler(scratch)
func setupScratch(srcDir string) (string, error) {
	scratchDir, err := os.MkdirTemp("", "mutation-baseline-")
	if err != nil {
		return "", fmt.Errorf("create scratch dir: %w", err)
	}

	if err := copyTree(srcDir, scratchDir); err != nil {
		_ = os.RemoveAll(scratchDir)
		return "", fmt.Errorf("copy source into scratch: %w", err)
	}

	fakeHome := filepath.Join(scratchDir, ".fake-home")
	if err := os.MkdirAll(fakeHome, 0o755); err != nil {
		_ = os.RemoveAll(scratchDir)
		return "", fmt.Errorf("create fake home: %w", err)
	}

	return scratchDir, nil
}

// scratchExcludeDirs lists directory names skipped during copyTree. The
// exclusions are by basename match at any depth. `.git` is excluded for
// size reasons; `dist`, `bin`, `.trace`, and `node_modules` are excluded
// because they are build outputs / harness artifacts and copying them
// wastes time without affecting the mutation surface.
var scratchExcludeDirs = map[string]bool{
	".git":         true,
	"dist":         true,
	"bin":          true,
	".trace":       true,
	"node_modules": true,
}

// copyTree recursively copies src to dst. Preserves regular-file
// permission bits (so the executable bit on scripts survives the copy).
// Skips directories named in scratchExcludeDirs. Fails fast when a
// symlink is encountered anywhere under src — see setupScratch for the
// rationale.
//
// Creates dst if it does not already exist, so callers can pass a path
// inside a freshly-allocated tmpdir without an extra MkdirAll.
func copyTree(src, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}

		// d.Type returns the bits that don't require an extra stat call;
		// ModeSymlink is included.
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink encountered at %s — refuse to copy (would let mutations escape the scratch root)", path)
		}

		if d.IsDir() {
			if scratchExcludeDirs[d.Name()] {
				return filepath.SkipDir
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(filepath.Join(dst, rel), info.Mode().Perm())
		}

		if !d.Type().IsRegular() {
			// Sockets, devices, pipes, etc. — refuse rather than guess.
			return fmt.Errorf("non-regular file at %s (mode %s) — refuse to copy", path, d.Type())
		}

		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(dst, rel), info.Mode().Perm())
	})
}

// copyFile reads src and writes it to dst with the given mode. Used by
// copyTree for regular files; honours the executable bit by passing the
// source's permission mask through.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// installSignalHandler wires SIGINT and SIGTERM to a cleanup goroutine
// that removes the scratch directory and exits with 130 (the conventional
// shell exit code for Ctrl-C). Without this, a Ctrl-C during a long run
// leaves an orphan scratch directory; the deferred cleanup in main does
// not fire on signal-driven exit.
//
// SIGKILL cannot be caught — but because the scratch copy is the only
// thing that gets mutated, a SIGKILL leaks at most one tmpdir under
// $TMPDIR, and the original source tree is unaffected.
func installSignalHandler(scratchDir string) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-ch
		fmt.Fprintf(os.Stderr, "\nmutation-baseline: received %s — cleaning up scratch %s\n", sig, scratchDir)
		_ = os.RemoveAll(scratchDir)
		os.Exit(130)
	}()
}
