// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// A build renders the closure the lockfile records, and nothing configures that.
// The assertions here cover the three halves of that contract a reader can
// observe: an ordinary build meeting an unpinned external reference halts naming
// it and what to run, the command line offers no switch that would soften the
// halt, and no command runs a standalone acquisition.
//
// The two absence assertions are structural rather than textual — a flag lookup
// and a walk of the registered command tree — because a search over help output
// would pass just as well against a flag that was merely renamed.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
)

// TestBuildHaltsOnAnUnpinnedExternalReference is the behaviour the removal of the
// build's acquisition dial rests on: an ORDINARY build — no dry run, no flag —
// meeting a reference the lockfile does not pin halts rather than fetching it,
// and tells the reader to run the pull step.
//
// The endpoint is deliberately unresolvable, so a run that tried to acquire
// would fail on the network rather than on the pin discipline. The condition in
// the cause chain is what separates those two outcomes.
func TestBuildHaltsOnAnUnpinnedExternalReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	const unpinned = "https://unreachable.invalid/values-schema.json"
	releasePath, catalogDir := dryRunFixture(t, root, unpinned)
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	storeRoot := externalinputs.StoreRoot(ws.Root(), ws.Config().ResolvedArtifactCacheDir())
	storeBefore := treeSnapshot(t, storeRoot)

	var out, diag bytes.Buffer
	err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{}, &diag, &out)
	if err == nil {
		t.Fatal("a build meeting an unpinned external reference must halt: there is no acquisition moment inside a build")
	}

	// Asserted against the whole cause chain rather than the registered condition
	// for the same reason the dry-run case is: the guest asks for the input, so the
	// refusal travels back out through the guest, which reports a failure of its
	// own, and only the condition's rendered text crosses that boundary.
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		unpinned,
		"is not pinned in the lockfile",
		"aggrega pull --release",
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the diagnostic does not carry %q; got:\n%s", want, chain)
		}
	}
	if d := diffSnapshots(storeBefore, treeSnapshot(t, storeRoot)); d != "" {
		t.Errorf("the build put bytes in the input store on its way to refusing:\n%s", d)
	}
}

// TestBuildOffersNoAcquisitionSwitch pins the absence a reader depends on: there
// is no flag on the build command that lets a render acquire an unpinned external
// input, in either direction. Both spellings are checked because the ruling
// refused the inverse as well as the original.
func TestBuildOffersNoAcquisitionSwitch(t *testing.T) {
	t.Parallel()
	c := newBuildCmd()
	for _, name := range []string{"no-acquire-inputs", "acquire-inputs"} {
		if f := c.Flags().Lookup(name); f != nil {
			t.Errorf("the build command registers --%s; a build renders the pinned closure and there is nothing to configure", name)
		}
	}
	// The positive control: the lookup finds a flag the command really does carry,
	// so a clean result above is a measurement of the flags rather than of a
	// lookup that never matches anything.
	if f := c.Flags().Lookup("dry-run"); f == nil {
		t.Fatal("the flag lookup found no --dry-run either, so the absences above measure the lookup and not the command")
	}
}

// TestNoStandaloneAcquisitionCommand pins the other absence: no command runs an
// acquisition pass of its own. Pinning happens at the pull step, so a reader has
// one command to learn and every printed remedy can name it.
func TestNoStandaloneAcquisitionCommand(t *testing.T) {
	t.Parallel()
	root := newRootCmd()
	var names []string
	for _, sub := range root.Commands() {
		names = append(names, sub.Name())
		if sub.Name() == "acquire" {
			t.Error("the command tree registers an acquire command; acquisition belongs to the pull step")
		}
	}
	// The positive control, for the same reason as above: the walk really does
	// enumerate the command tree.
	var sawPull bool
	for _, n := range names {
		if n == "pull" {
			sawPull = true
		}
	}
	if !sawPull {
		t.Fatalf("the walk found no pull command either, so it is not enumerating the tree; saw: %v", names)
	}
}
