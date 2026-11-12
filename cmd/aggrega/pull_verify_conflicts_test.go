// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pullops"
)

// pullVerifyConflictRows is every pair `pull --verify` refuses on this command,
// with the sentence the command prints for it and the conflict the shared
// resolution's rule reports it as.
//
// The sentences are written out here rather than read from the command, because
// an expectation taken from the code under test agrees with any change to it.
var pullVerifyConflictRows = []struct {
	name     string
	args     []string
	sentence string
	with     pullops.VerifyConflict
}{
	{
		name:     "--pin-sources",
		args:     []string{"--pin-sources"},
		sentence: "--verify and --pin-sources are mutually exclusive: --verify writes no entity.lock, so it neither records a pin an entity.lock is missing nor halts on one",
		with:     pullops.VerifyWithPinSources,
	},
	{
		name:     "--pin-sources=false",
		args:     []string{"--pin-sources=false"},
		sentence: "--verify and --pin-sources are mutually exclusive: --verify writes no entity.lock, so it neither records a pin an entity.lock is missing nor halts on one",
		with:     pullops.VerifyWithPinSources,
	},
	{
		name:     "a bare --upgrade",
		args:     []string{"--upgrade"},
		sentence: "--verify and --upgrade are mutually exclusive",
		with:     pullops.VerifyWithUpgrade,
	},
	{
		name:     "--upgrade naming a target",
		args:     []string{"--upgrade", refusalComponentURI},
		sentence: "--verify and --upgrade are mutually exclusive",
		with:     pullops.VerifyWithUpgrade,
	},
	{
		name:     "a target named without --upgrade",
		args:     []string{refusalComponentURI},
		sentence: "positional arguments name upgrade targets and require --upgrade",
		with:     pullops.VerifyWithUpgradeTargets,
	},
	{
		name:     "--dry-run",
		args:     []string{"--dry-run"},
		sentence: "--verify and --dry-run are mutually exclusive: --verify already writes no lockfile, entity.lock or plugin trust record",
		with:     pullops.VerifyWithDryRun,
	},
}

// pullVerifyRoot builds the root command for `pull --verify` over the workspace
// at root, followed by extra.
//
// Standard input is an empty reader, so a regression that put the bare-upgrade
// confirmation ahead of the refusal meets a stream nobody can answer on and says
// so, rather than waiting on the terminal the suite runs under.
func pullVerifyRoot(root, catalogDir string, extra []string) *cobra.Command {
	r := newRootCmd()
	r.SetIn(bytes.NewReader(nil))
	r.SetOut(io.Discard)
	r.SetErr(io.Discard)
	args := []string{"--workspace-root", root, "pull", "--release", filepath.Join(root, "release.yaml"), "--verify", "--quiet"}
	if catalogDir != "" {
		args = append(args, "--catalog", catalogDir)
	}
	r.SetArgs(append(args, extra...))
	return r
}

// TestPullVerifyConflictsKeepTheirWordingAndExitCode holds each pair to the
// sentence this command prints for it and the exit code it ends with, and to
// leaving the lockfile as it was.
//
// These are non-regression rows: the command refused every one of these pairs
// before the rule moved into the shared resolution, with these sentences, so they
// pass on both sides of that move. What moved is who decides, which the next test
// holds. The first row is the positive control — the same command over the same
// workspace with no second request answers — so each refusal below is the pair's
// and not the workspace's.
func TestPullVerifyConflictsKeepTheirWordingAndExitCode(t *testing.T) {
	// Not parallel: its rows set the process environment, and the command tree's pre-run writes the process-global display decision.
	type row struct {
		name     string
		args     []string
		sentence string
	}
	rows := []row{{name: "--verify on its own: the verify answers"}}
	for _, r := range pullVerifyConflictRows {
		rows = append(rows, row{name: r.name, args: r.args, sentence: r.sentence})
	}
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			// The configuration layer above the workspace is emptied before the
			// fixture's pull, so the pull and the command resolve the same workspace
			// whatever the machine running this carries.
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			f := writeRefusalFixture(t)
			lockPath := filepath.Join(f.root, "release.lock")
			before, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile before the command: %v", err)
			}

			err = pullVerifyRoot(f.root, f.catalogDir, tc.args).ExecuteContext(context.Background())
			code := executeRoot(context.Background(), pullVerifyRoot(f.root, f.catalogDir, tc.args))

			if tc.sentence == "" {
				if err != nil {
					t.Fatalf("the verify refused a workspace nothing changed in: %v", err)
				}
				if code != 0 {
					t.Errorf("the verify answered and the command exited %d", code)
				}
			} else {
				if err == nil {
					t.Fatalf("pull --verify %v was accepted; the pair must be refused", tc.args)
				}
				if got := err.Error(); got != tc.sentence {
					t.Errorf("the refusal reads %q; want %q", got, tc.sentence)
				}
				// What the command prints is the formatter's rendering of the error, so
				// that is held to the sentence too: a refusal carrying anything the
				// formatter treats as a condition would print more than the sentence.
				if got := formatCompilerError(err); got != tc.sentence {
					t.Errorf("the command prints %q; want %q", got, tc.sentence)
				}
				if code != 1 {
					t.Errorf("the refusal exited %d; want 1", code)
				}
			}

			after, err := os.ReadFile(lockPath)
			if err != nil {
				t.Fatalf("read the lockfile after the command: %v", err)
			}
			if !bytes.Equal(before, after) {
				t.Errorf("the command rewrote the lockfile")
			}
		})
	}
}

// TestPullVerifyConflictsAreTheSharedRule holds each pair's refusal to being the
// shared resolution's rule, worded by this command, rather than a check of the
// command's own.
//
// The rule decides every verify exclusion once, for the command line, the
// assistant tool and the daemon alike, so a check kept here would be a second copy
// free to disagree with it. The refusal's chain is what says which rule raised it.
// Nothing is resolved before the refusal, so the workspace is an empty directory.
func TestPullVerifyConflictsAreTheSharedRule(t *testing.T) {
	for _, tc := range pullVerifyConflictRows {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			root := t.TempDir()

			err := pullVerifyRoot(root, "", tc.args).ExecuteContext(context.Background())
			if err == nil {
				t.Fatalf("pull --verify %v was accepted; the pair must be refused", tc.args)
			}
			var conflict *pullops.VerifyConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("the refusal is the command's own rather than the shared rule's: %v", err)
			}
			if conflict.With != tc.with {
				t.Errorf("the rule reported conflict %d; want %d", conflict.With, tc.with)
			}
		})
	}
}
