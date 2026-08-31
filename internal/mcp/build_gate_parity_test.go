// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool renders through the shared preview computation, in its
// artifact-producing posture. What that posture buys is the subject here: the
// gates an artifact-producing surface owes, each pinned by the fault it catches.
//
// One fault per fixture, asserted against the gate that fault pins. An any-error
// assertion would keep passing once some other gate started refusing the same
// workspace, and would silently stop covering its subject.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
)

// TestBuildToolRefusesAnUnmappedInstance is the behavioural consequence of
// putting this surface on the shared computation.
//
// The resolution-completeness verifier runs after the IR is built and before the
// render, halting when an instance resolved to no recipe. It is supplied by the
// caller as a pipeline option, so a surface that supplies none never runs it. The
// fixture is an Aggregate declaring one component against a Manifest whose
// mapping table is empty, so the instance maps to nothing at all.
//
// The companion case below drives the same fixture with the verifier absent, which
// is what makes this one attributable: the two differ in that one policy field and
// in nothing else.
func TestBuildToolRefusesAnUnmappedInstance(t *testing.T) {
	s := newTestServer(t)
	root, dirs := writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityEmptyManifest,
	})
	releasePath := filepath.Join(root, "release.yaml")

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if !res.IsError {
		t.Fatalf("an unmapped instance must refuse the build; got a success payload: %s", res.Content[0].Text)
	}
	// Pinned to the resolution-completeness gate by the condition it raises, not
	// by the summary sentence: the sentence is presentation and free to change,
	// while the condition is what the gate IS.
	text := res.Content[0].Text
	if !strings.Contains(text, "recipe resolution") {
		t.Errorf("the refusal must come from the resolution-completeness gate:\n%s", text)
	}
}

// TestUnmappedInstanceMisattributesWhenTheVerifierIsNotSupplied is the negative
// control for the case above, and it is what makes that case's claim falsifiable:
// it drives the SAME fixture through the shared computation with the
// resolution-completeness verifier switched off — the configuration of a surface
// that supplies no verifier. The two runs differ in that one policy field and in
// nothing else, so whatever differs between them is attributable to the gate.
//
// What differs is the ATTRIBUTION, not the acceptance, and that is worth being
// exact about because the tempting claim is the stronger one. With no verifier
// the build still fails — it reaches the render and dies in the engine
// factory for a node that resolved to no recipe. So the gate does not convert a
// silent success into a refusal here; it converts a downstream failure naming an
// internal construction step into one naming the fault the author can act on.
//
// The assertion is two-sided for that reason: the unverified run must fail, and
// it must fail as something OTHER than recipe resolution. A one-sided assertion
// would pass if the verifier had somehow run anyway.
func TestUnmappedInstanceMisattributesWhenTheVerifierIsNotSupplied(t *testing.T) {
	s := newTestServer(t)
	root, dirs := writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityEmptyManifest,
	})
	releasePath := filepath.Join(root, "release.yaml")

	tw := resolvedToolWorkspace(t, s, root, dirs)
	lf := loadReleaseLockfile(releasePath)

	policy := compilerops.ArtifactPolicy()
	policy.VerifyLockfileDrift = false
	policy.VerifyRecipeResolution = false

	_, err := compilerops.Preview(
		context.Background(),
		compilerops.PreviewInputs{
			View:            tw.newView(lf),
			ReleasePath:     releasePath,
			WorkspaceRoot:   tw.root,
			WorkspaceConfig: tw.cfg,
			Lockfile:        lf,
		},
		policy,
		pipeline.Options{},
	)
	if err == nil {
		t.Fatal("the unmapped instance fails with or without the verifier; a success here means the fixture stopped carrying its fault")
	}
	if strings.Contains(err.Error(), "recipe resolution") {
		t.Fatalf("with the verifier off, the failure must NOT be the resolution-completeness gate's — "+
			"this run exists to show what a surface supplying no verifier reports:\n%v", err)
	}
}

// TestBuildToolBuildsTheSameWorkspaceWhenTheInstanceIsMapped is the matched
// positive control on the same fixture pair. Without it the assertion above would
// also pass on a tool that refused every build — the failure mode a single
// negative case structurally cannot distinguish. The two cases differ in the
// mapping table and in nothing else.
func TestBuildToolBuildsTheSameWorkspaceWhenTheInstanceIsMapped(t *testing.T) {
	s := newTestServer(t)
	root, dirs := writeReleaseParityWorkspace(t, releaseParityCase{
		aggregate: releaseParityBackendAggregate,
		manifest:  releaseParityCleanManifest,
	})
	releasePath := filepath.Join(root, "release.yaml")

	res := callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
	})
	if res.IsError {
		t.Fatalf("the same workspace with the instance mapped must build: %s", res.Content[0].Text)
	}
	payload := unmarshalBuildPayload(t, res)
	if len(payload.Files) == 0 {
		t.Fatal("a successful build must return the rendered files")
	}
}
