// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// composedFromClosure is the lockfile a resolution composes from a closure it
// computed, across every axis the drift verify compares, so a verify over the
// same closure is clean and one over a different closure names the difference.
func composedFromClosure(cl closure.Closure) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{Version: 1, Release: "release.yaml", GeneratedAt: "2026-01-01T00:00:00Z"}
	for _, d := range cl.ReleaseDocuments {
		lf.ReleaseDocuments = append(lf.ReleaseDocuments, lockfile.ReleaseDocument{Path: d.Path, Digest: d.Digest})
	}
	for _, e := range cl.Entities {
		lf.Entities = append(lf.Entities, lockfile.Entity{URI: e.URI, ResolvedVersion: e.ResolvedVersion, Digest: e.Digest})
	}
	for _, p := range cl.Plugins {
		lf.Plugins = append(lf.Plugins, lockfile.Plugin{URI: p.URI, ResolvedVersion: p.ResolvedVersion, Digest: p.Digest})
	}
	for _, l := range cl.SchemaLibraries {
		lf.SchemaLibraries = append(lf.SchemaLibraries, lockfile.SchemaLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.MappingLibraries {
		lf.MappingLibraries = append(lf.MappingLibraries, lockfile.MappingLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.WorkloadLibraries {
		lf.WorkloadLibraries = append(lf.WorkloadLibraries, lockfile.WorkloadLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	for _, l := range cl.ArtifactLibraries {
		lf.ArtifactLibraries = append(lf.ArtifactLibraries, lockfile.ArtifactLibrary{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest})
	}
	return lf
}

// TestComposedLockfileIsHeldToTheBuildsDriftVerify pins the guard a resolution
// relies on to never write a lockfile its own build refuses: a lockfile handed in
// as composed-and-not-yet-written is verified exactly as a pre-existing one is,
// and a disagreement is reported as the composing resolution and the build
// disagreeing — not as the drift condition, whose advice to resolve again would
// loop, since resolving again composes the same lockfile.
//
// The rows are one fault each against the same fixture, and the controls are
// what make the guarded row mean something: the same disagreeing lockfile handed
// in as one a fill already wrote is not verified again, and handed in as one
// that was on disk is refused with the drift condition itself.
func TestComposedLockfileIsHeldToTheBuildsDriftVerify(t *testing.T) {
	// The honest lockfile is composed from the closure a build of the fixture
	// reconstructs, which is what a resolution writes for it. The reconstruction
	// runs only where a lockfile is supplied, so a first pass supplies the walked
	// closure's own as one a fill wrote (not verified) to obtain the reconstructed
	// closure the second pass composes from.
	view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
	walked, err := PreviewForArtifact(context.Background(),
		NewPreviewInputs(NoExternalInputs(), view, releasePath, workspaceRoot, wsCfg, "test"),
		pipeline.Options{SkipRender: true})
	if err != nil {
		t.Fatalf("walk the fixture's closure: %v", err)
	}
	view, releasePath, workspaceRoot, wsCfg = fixtureWorkspace(t)
	seed, err := PreviewForArtifact(context.Background(),
		NewPreviewInputs(NoExternalInputs(), view, releasePath, workspaceRoot, wsCfg, "test",
			WithLockfile(composedFromClosure(walked.Resolved.Closure), false)),
		pipeline.Options{SkipRender: true})
	if err != nil {
		t.Fatalf("reconstruct the fixture's closure: %v", err)
	}
	honest := composedFromClosure(seed.Resolved.Closure)
	if len(honest.Entities) == 0 {
		t.Fatal("the fixture's closure pins no entity, so every row below would compare empty sets")
	}
	withdrawn := composedFromClosure(seed.Resolved.Closure)
	withdrawn.Entities = append(withdrawn.Entities, lockfile.Entity{
		URI: "demo/withdrawn", ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64),
	})

	cases := []struct {
		name         string
		option       PreviewOption
		wantGate     PreviewGate
		wantComposed bool
		wantDrift    bool
	}{
		{"composed and agreeing renders", WithComposedLockfile(honest), GateNone, false, false},
		{"composed and disagreeing is refused as a disagreement", WithComposedLockfile(withdrawn), GateLockfileDrift, true, false},
		{"filled by this run is not verified again", WithLockfile(withdrawn, false), GateNone, false, false},
		{"pre-existing and disagreeing is refused as drift", WithLockfile(withdrawn, true), GateLockfileDrift, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view, releasePath, workspaceRoot, wsCfg := fixtureWorkspace(t)
			preview, err := PreviewForArtifact(context.Background(),
				NewPreviewInputs(NoExternalInputs(), view, releasePath, workspaceRoot, wsCfg, "test", tc.option),
				pipeline.Options{SkipRender: true})
			gate := GateNone
			if preview != nil {
				gate = preview.FailedGate
			}
			if gate != tc.wantGate {
				t.Fatalf("failed gate = %v; want %v (err: %v)", gate, tc.wantGate, err)
			}
			if got := IsComposedLockfileRefusal(err); got != tc.wantComposed {
				t.Errorf("IsComposedLockfileRefusal = %v; want %v (err: %v)", got, tc.wantComposed, err)
			}
			if got := stderrors.Is(err, lockfile.ErrLockfileDrift()); got != tc.wantDrift {
				t.Errorf("errors.Is(err, ErrLockfileDrift()) = %v; want %v — a composed lockfile's refusal "+
					"must not carry the drift condition, whose remedy is to resolve again (err: %v)", got, tc.wantDrift, err)
			}
			if tc.wantComposed {
				msg := err.Error()
				if !strings.Contains(msg, `entity "demo/withdrawn" no longer used by the release`) {
					t.Errorf("the refusal must name each disagreement the verify found; got:\n%s", msg)
				}
				if strings.Contains(msg, lockfile.ErrLockfileDrift().Error()) {
					t.Errorf("the refusal must not open the findings with the drift condition's own sentence, "+
						"which says the lockfile is out of date; got:\n%s", msg)
				}
			}
		})
	}
}
