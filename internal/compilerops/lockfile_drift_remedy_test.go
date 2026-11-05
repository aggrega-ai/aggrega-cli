// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile/lockfiletest"
)

// TestLockfileDriftRemedyNamesAPlainPullOfTheRelease pins what the one home of the
// drift remedy names: a pull of the Release it was given, and no upgrade. Every
// disagreement a build reports is cleared by that pull, and an upgrade would float
// pins the reader did not ask to move — and is refused outright in an offline
// workspace, where the plain pull still runs.
func TestLockfileDriftRemedyNamesAPlainPullOfTheRelease(t *testing.T) {
	const release = "releases/production.yaml"
	remedy := LockfileDriftRemedy(release)
	if want := "`aggrega pull --release " + release + "`"; !strings.Contains(remedy, want) {
		t.Errorf("the remedy does not name %s: %q", want, remedy)
	}
	if strings.Contains(remedy, "--upgrade") {
		t.Errorf("the remedy names an upgrade, which no disagreement a build reports needs: %q", remedy)
	}
}

// joinedElement reports whether text is the whole text of an error that a
// multi-error frame somewhere along err's chain carries as one of its elements.
func joinedElement(err error, text string) bool {
	if err == nil {
		return false
	}
	if multi, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range multi.Unwrap() {
			if e.Error() == text || joinedElement(e, text) {
				return true
			}
		}
		return false
	}
	return joinedElement(stderrors.Unwrap(err), text)
}

// TestADriftRefusalCarriesItsRemedyAsAnErrorOfItsOwn pins the shape that puts the
// remedy in front of a reader at a terminal. The command line renders a chain
// carrying several errors element by element and shows no frame's own text, so the
// remedy reaches the terminal only as an element of such a chain. A remedy
// formatted into a frame would still satisfy a check on the error's text — which is
// why this checks the element and not the text.
//
// The refused lockfile is built the way TestComposedLockfileIsHeldToTheBuildsDriftVerify
// builds its disagreeing one: the fixture's own pins plus one entity the Release no
// longer reaches, handed in as a lockfile that was on disk before the run.
func TestADriftRefusalCarriesItsRemedyAsAnErrorOfItsOwn(t *testing.T) {
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
			WithLockfile(lockfiletest.Mirroring(composedHeader(), walked.Resolved.Closure), false)),
		pipeline.Options{SkipRender: true})
	if err != nil {
		t.Fatalf("reconstruct the fixture's closure: %v", err)
	}
	drifted := lockfiletest.Mirroring(composedHeader(), seed.Resolved.Closure)
	drifted.Entities = append(drifted.Entities, lockfile.Entity{
		URI: "demo/withdrawn", ResolvedVersion: "1.0.0", Digest: "sha256:" + strings.Repeat("0", 64),
	})

	view, releasePath, workspaceRoot, wsCfg = fixtureWorkspace(t)
	preview, err := PreviewForArtifact(context.Background(),
		NewPreviewInputs(NoExternalInputs(), view, releasePath, workspaceRoot, wsCfg, "test",
			WithLockfile(drifted, true)),
		pipeline.Options{SkipRender: true})
	gate := GateNone
	if preview != nil {
		gate = preview.FailedGate
	}
	if gate != GateLockfileDrift {
		t.Fatalf("a pre-existing lockfile pinning an entity the Release no longer reaches must be refused at the "+
			"drift gate; failed gate = %v, err %v", gate, err)
	}
	if !stderrors.Is(err, lockfile.ErrLockfileDrift()) {
		t.Fatalf("the refusal must carry the drift condition; got %v", err)
	}
	if remedy := LockfileDriftRemedy(releasePath); !joinedElement(err, remedy) {
		t.Errorf("the refusal does not carry the remedy %q as an error of its own, so a terminal would not show "+
			"it; got:\n%v", remedy, err)
	}
}
