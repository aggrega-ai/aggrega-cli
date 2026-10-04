// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Every command that hands the graph back withholds the values an author
// declared sensitive unless the reader asks for them with --unredacted. The
// inspection command's two arms are proved beside its other graph assertions;
// this file proves the build's and the validation's, each driven through the
// code that reads the flag rather than through a hand-back the test assembled —
// a test that wrote the writer call itself would pass while the command ignored
// the flag.

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The fixture declares values sensitive at every position the graph carries
// authored data — a node's own value, an attachment on the node's own block, an
// attachment a connection carries, and the infra the Release writes on each
// attachment — and every one of them is a planted string, so a value reaching
// the output is found by name.
const (
	revealFixtureDir     = "../../internal/compiler/testdata/governance-withholding"
	revealFixtureRelease = revealFixtureDir + "/release.yaml"
	revealFixtureCatalog = revealFixtureDir + "/catalog/base"
)

// revealPlanted are the fixture's declared-sensitive values, one per position.
var revealPlanted = []string{
	"planted-node-own-value",
	"planted-component-credential",
	"planted-connection-credential",
	"planted-release-infra",
	"planted-connection-infra",
}

// revealPublic are values in the same payloads that no declaration covers. They
// are in the graph whichever arm runs, which is the positive control for the
// absence assertions: a search that could not see into the document would miss
// these as well.
var revealPublic = []string{"db-key", "gpt-key", "weekly", "daily"}

// assertRevealArm checks one emitted graph document against the arm that
// produced it.
func assertRevealArm(t *testing.T, subject, document string, revealed bool) {
	t.Helper()
	for _, value := range revealPlanted {
		if got := strings.Contains(document, value); got != revealed {
			t.Errorf("%s: the graph carries %q: %v, want %v", subject, value, got, revealed)
		}
	}
	if got := strings.Contains(document, redact.WithheldMarkerKey()); got == revealed {
		t.Errorf("%s: the graph carries the withheld marker: %v, want %v", subject, got, !revealed)
	}
	for _, value := range revealPublic {
		if !strings.Contains(document, value) {
			t.Errorf("%s: the graph does not carry %q, which no declaration covers", subject, value)
		}
	}
}

// TestBuildIRGraph_WithholdsUnlessRevealed runs one build per arm over a copy
// of the fixture, its lockfile filled by a pull, with the run options composed
// from the command's own flag state — so the flag reaching the writer is part of
// what is under test.
func TestBuildIRGraph_WithholdsUnlessRevealed(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name       string
		unredacted bool
	}{
		{"--ir-graph alone withholds", false},
		{"--ir-graph with --unredacted reveals", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			copyExampleInputs(t, revealFixtureDir, root)
			releasePath := filepath.Join(root, "release.yaml")
			catalogDir := filepath.Join(root, "catalog", "base")
			primeDryRunLockfile(t, root, releasePath, catalogDir)
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

			var out, diag bytes.Buffer
			run := rootedBuildOptions(t, root, buildRunOptions(&buildFlags{irGraph: true, unredacted: row.unredacted}, &out))
			if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, false,
				buildDiagOptions{}, run, &diag, &out); err != nil {
				t.Fatalf("build --ir-graph (unredacted=%v): %v\ndiagnostics:\n%s", row.unredacted, err, diag.String())
			}

			document, _ := splitLeadingGraph(t, out.String())
			assertRevealArm(t, "aggrega build --ir-graph", document, row.unredacted)
		})
	}
}

// TestValidateIRGraph_WithholdsUnlessRevealed drives the validation command
// through its own argument parsing, once per arm, over the fixture in place: the
// release scope writes nothing to disk, and with no lockfile beside the Release
// the closure resolves through the catalog directory named.
func TestValidateIRGraph_WithholdsUnlessRevealed(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name       string
		unredacted bool
	}{
		{"--ir-graph alone withholds", false},
		{"--ir-graph with --unredacted reveals", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")

			args := []string{"--release", revealFixtureRelease, "--catalog", revealFixtureCatalog, "--" + flagIRGraph}
			if row.unredacted {
				args = append(args, "--"+flagUnredacted)
			}
			var out bytes.Buffer
			cmd := newValidateCmd()
			cmd.SetOut(&out)
			cmd.SetErr(io.Discard)
			cmd.SetContext(withWorkspaceResolver(context.Background(), root, nil))
			cmd.SetArgs(args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("validate %v: %v", args, err)
			}

			document, _ := splitLeadingGraph(t, out.String())
			assertRevealArm(t, "aggrega validate --release --ir-graph", document, row.unredacted)
		})
	}
}

// TestUnredactedWithoutTheGraphIsRefused pins the refusal on the two commands
// whose only output that withholds is the graph. The reveal asked for alone
// would change nothing there, and an ignored request reads to the reader as one
// that was honoured.
//
// The second half of each row is the paired control: with the graph asked for,
// the same invocation gets past the check and fails later for a reason of its
// own — here, the command was run without a workspace — so a check that refused
// the reveal unconditionally fails the row.
func TestUnredactedWithoutTheGraphIsRefused(t *testing.T) {
	t.Parallel()

	const refusal = "no graph was asked for"
	for _, row := range []struct {
		name    string
		command func() *cobra.Command
	}{
		{"build", newBuildCmd},
		{"validate", newValidateCmd},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			execute := func(args ...string) error {
				c := row.command()
				c.SetOut(io.Discard)
				c.SetErr(io.Discard)
				c.SetArgs(append([]string{"--release", "release.yaml"}, args...))
				return c.Execute()
			}

			err := execute("--" + flagUnredacted)
			if err == nil {
				t.Fatal("--unredacted without --ir-graph was accepted; the reveal reaches nothing else this command writes")
			}
			for _, want := range []string{"--" + flagUnredacted, "--" + flagIRGraph, refusal} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}

			err = execute("--"+flagUnredacted, "--"+flagIRGraph)
			if err == nil {
				t.Fatal("the paired invocation succeeded with no workspace, so it cannot show where the check sits")
			}
			if strings.Contains(err.Error(), refusal) {
				t.Errorf("the refusal fired with --%s asked for; it must gate only the unpaired reveal: %v", flagIRGraph, err)
			}
		})
	}
}
