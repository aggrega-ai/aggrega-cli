// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package compileropstest provides shared fixtures for tests that need a
// Release's lockfile to describe exactly what the Release resolves to.
//
// It exists as its own package because two test suites need the same fixture —
// the command line's, which asserts what a command reading a Release prints, and
// the assistant tools', which asserts what a tool reading a Release answers — and
// a fixture written twice would let the two suites disagree about what a
// matching lockfile is.
package compileropstest

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile/lockfiletest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// WriteMatchingLockfile writes beside the Release at releasePath a lockfile
// pinning exactly what a build of that Release compares a lockfile against —
// every entity, library, plugin and Release file the build's resolution reaches,
// at the version and digest it reaches each at — after handing it to edit, which
// adds the facts a pull records beside those pins and the comparison reads none
// of, such as a withdrawal marker. edit may be nil.
//
// It is composed rather than pulled because a pull refuses a Manifest whose
// mapping table is inconsistent, and a fixture may carry such a mapping on
// purpose: the finding it raises can be one of the things a test asserts is
// reported. So the pins come from the closure the inspection preview resolves and
// rebuilds against a lockfile — the computation a build's drift comparison reads
// — seeded by a lockfile pinning nothing, and the result is held, before this
// returns, to compilerops.HoldReleaseToLockfile, the check the reading surfaces
// run: a composition that drifted would otherwise make every case built on it
// refuse for that rather than for anything it asserts.
//
// viewFor builds the caller's view of the workspace pinned to the lockfile it is
// handed — each test suite resolves its workspace its own way, and the view is
// the one thing this needs from that resolution. workspaceRoot and
// workspaceConfig are the ones that view reads.
func WriteMatchingLockfile(
	t testing.TB,
	viewFor func(*lockfile.Lockfile) *catalog.LazyLookup,
	releasePath, workspaceRoot string,
	workspaceConfig workspaceconfig.Config,
	edit func(*lockfile.Lockfile),
) {
	t.Helper()
	ctx := context.Background()
	lockPath := lockfile.PathFor(releasePath)

	seed := &lockfile.Lockfile{Version: 1, Release: filepath.Base(releasePath), GeneratedAt: "2026-06-03T10:00:00Z"}
	preview, err := compilerops.PreviewForInspection(ctx,
		compilerops.NewPreviewInputs(compilerops.NoExternalInputs(), viewFor(seed), releasePath, workspaceRoot,
			workspaceConfig, "test", compilerops.WithLockfile(seed, true)),
		pipeline.Options{SkipRender: true})
	if preview == nil || preview.Resolved == nil ||
		preview.FailedGate == compilerops.GateClosureResolution || preview.FailedGate == compilerops.GateAxisReconstruction {
		t.Fatalf("the Release did not resolve, so there is no closure to pin: %v", err)
	}

	lf := lockfiletest.Mirroring(seed, preview.Resolved.Closure)
	if edit != nil {
		edit(lf)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	written, _, err := lockfile.Load(workspaceRoot, lockPath)
	if err != nil {
		t.Fatalf("the composed lockfile does not load: %v", err)
	}
	if err := compilerops.HoldReleaseToLockfile(ctx, viewFor(written), releasePath, workspaceRoot, workspaceConfig, written); err != nil {
		t.Fatalf("the composed lockfile does not match what the Release resolves to, so every case built on "+
			"this fixture would be refused for that: %v", err)
	}
}

// EntityRow returns the one entity row recorded under exactly the reference uri,
// failing the test where there is not exactly one: a marker meant for a row that
// is not there would leave the case asserting against a lockfile that records
// none.
//
// The match is on the whole reference, because one entity can be pinned under
// more than one — an Aggregate naming a Component with its version and a
// Manifest's mapping key naming it without leaves a row for each. A marker is
// reported per row, under the row's own reference, so the reference chosen is the
// one the report then names.
func EntityRow(t testing.TB, lf *lockfile.Lockfile, uri string) *lockfile.Entity {
	t.Helper()
	var found *lockfile.Entity
	var uris []string
	for i := range lf.Entities {
		uris = append(uris, lf.Entities[i].URI)
		if lf.Entities[i].URI != uri {
			continue
		}
		if found != nil {
			t.Fatalf("two rows are recorded under %q: %v", uri, uris)
		}
		found = &lf.Entities[i]
	}
	if found == nil {
		t.Fatalf("no row is recorded under %q; the rows are %v", uri, uris)
	}
	return found
}
