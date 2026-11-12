// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The build tool renders through the shared preview computation, in its
// artifact-producing posture. What that posture buys is the subject here: the
// gates an artifact-producing surface owes, each pinned by the fault it catches.
//
// One fault per fixture, asserted against the gate that fault pins. An any-error
// assertion would keep passing once some other gate started refusing the same
// workspace, and would silently stop covering its subject.

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildToolRefusesAnUnmappedInstance is the behavioural consequence of
// putting this surface on the shared computation.
//
// The resolution-completeness verifier runs after the IR is built and before the
// render, halting when an instance resolved to no recipe. The fixture is an
// Aggregate declaring one component against a Manifest whose mapping table is
// empty, so the instance maps to nothing at all.
//
// What this case claims is that the build tool runs the gate. That the gate is
// what ATTRIBUTES the failure — rather than merely failing something that would
// have failed anyway — is a property of the shared computation, and it is pinned
// there against the same fault, with the gate switched off as its control. It
// cannot be pinned from here: no operation offers that posture, and nothing
// outside the computation's own package can assemble one.
//
// The positive control below is what keeps this case honest from this side: it
// drives the same workspace with the instance mapped and requires a build.
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
