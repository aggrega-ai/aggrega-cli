// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

// The render-time external-input seam is a required input on the one preview
// computation, and the guarantee that makes it required is HYBRID. These tests
// cover the half the language cannot carry; the other half is not testable here
// and says so below rather than being quietly omitted.
//
// Compile-time half, NOT asserted by any test in this file and unassertable by
// one: a caller outside this package cannot write a PreviewInputs literal at all
// (every field is unexported, so every key is refused), and cannot call
// NewPreviewInputs without naming a seam. Both are proved by code that does not
// build, which cannot ship as a test — a test file that fails to compile fails
// the package rather than passing.
//
// Run-time half, asserted here: the bare zero value PreviewInputs{} still
// compiles, because Go admits the zero value of every struct. It is the one
// bypass the constructor cannot close, so the preview refuses it.
//
// The entry points are named per OPERATION and each supplies its own posture, so
// these tests drive the operation whose gates the case needs rather than
// assembling a posture — which they could not do from outside this package, and
// which nothing inside it should do either where an operation already says it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestPreviewRefusesTheZeroValue is the run-time half of the guarantee. The zero
// value carries no seam and no view, so a render under it would produce the
// not-available envelope the constructor exists to prevent — and would do it
// while looking like a correctly-typed call.
//
// It is asserted against the sentinel rather than the message text: the text is
// presentation and free to change, while the condition is what the refusal IS.
func TestPreviewRefusesTheZeroValue(t *testing.T) {
	t.Parallel()

	// The zero value compiles. That is the whole reason this test exists, and the
	// line below is the demonstration of it rather than incidental setup.
	var zero PreviewInputs

	out, err := PreviewForArtifact(context.Background(), zero, pipeline.Options{})
	if err == nil {
		t.Fatal("a zero-valued PreviewInputs must be refused; unrefused it renders with no external-input " +
			"seam attached, which is exactly the divergence the constructor exists to prevent")
	}
	if !errors.Is(err, errPreviewInputsZeroValue) {
		t.Fatalf("the refusal must be the not-constructed condition, not some downstream nil dereference "+
			"that happens to fail: %v", err)
	}
	// A result is still returned, on the same terms every other refusal returns
	// one: a caller reads its warnings before it reads the failure.
	if out == nil {
		t.Error("the refusal must still return a result, as every other gate refusal does")
	}
	// The message has to tell the caller what to do, because the fault is in
	// their construction and nothing downstream will explain it.
	for _, want := range []string{"NewPreviewInputs", "NoExternalInputs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so the caller is not told how to fix it:\n%v", want, err)
		}
	}
}

// TestConstructedInputsPassTheZeroValueGuard is the matched positive control. It
// is what makes the test above falsifiable: without it, a preview that refused
// EVERY input would satisfy that assertion perfectly.
//
// It asserts only that the guard let the run past — the run then fails on the
// nil view, which is a different fault and is asserted to be different.
func TestConstructedInputsPassTheZeroValueGuard(t *testing.T) {
	t.Parallel()

	in := NewPreviewInputs(NoExternalInputs(), nil, "release.yaml", t.TempDir(), workspaceconfig.Default(), "test")
	_, err := PreviewForInspection(context.Background(), in, pipeline.Options{})
	if err != nil && errors.Is(err, errPreviewInputsZeroValue) {
		t.Fatalf("a constructed input must clear the not-constructed guard; the guard is firing on "+
			"everything, so the refusal above proves nothing: %v", err)
	}
}

// TestNewPreviewInputsCarriesWhatItWasGiven pins the constructor's mapping from
// arguments to fields. It is dull on purpose: the constructor is now the ONLY
// door into this type, so an argument silently landing in the wrong field would
// mis-drive every surface at once with nothing else able to notice.
func TestNewPreviewInputsCarriesWhatItWasGiven(t *testing.T) {
	t.Parallel()

	seam := PinnedExternalInputs(t.TempDir(), ".aggrega/cache", workspaceconfig.Default(), nil, nil)
	lf := &lockfile.Lockfile{Version: 1}
	var sawPhase bool

	in := NewPreviewInputs(
		seam,
		nil,
		"release.yaml",
		"/workspace",
		workspaceconfig.Default(),
		"compiler-version",
		WithLockfile(lf, true),
		WithCatalogWarnings([]string{"an advisory"}),
		WithProgress(func(PreviewPhase) { sawPhase = true }),
	)

	if in.ExternalInputs() != seam {
		t.Error("the seam the constructor was given is not the seam it carries")
	}
	if in.releasePath != "release.yaml" || in.workspaceRoot != "/workspace" {
		t.Errorf("release path and workspace root did not land where they belong: %q, %q",
			in.releasePath, in.workspaceRoot)
	}
	if in.compilerVersion != "compiler-version" {
		t.Errorf("compilerVersion = %q", in.compilerVersion)
	}
	if in.lockfile != lf || !in.lockfilePreexisted {
		t.Error("WithLockfile must carry BOTH the lockfile and the pre-existed fact; " +
			"they travel together because neither answers anything alone")
	}
	if len(in.catalogWarnings) != 1 || in.catalogWarnings[0] != "an advisory" {
		t.Errorf("catalogWarnings = %v", in.catalogWarnings)
	}
	if in.phase == nil {
		t.Fatal("WithProgress carried no callback")
	}
	in.phase(PhaseClosure)
	if !sawPhase {
		t.Error("the carried callback is not the one supplied")
	}
	if !in.constructed {
		t.Error("the constructor must mark its result constructed, or the preview refuses everything it builds")
	}
}

// TestNoExternalInputsIsTheAbsentSeam states plainly what the sentinel is, so the
// hybrid guarantee is not read as stronger than it is. Naming it is a decision
// the reader of a call site can see; it is not a second enforcement, and the
// value it yields is indistinguishable at run time from a bare nil.
func TestNoExternalInputsIsTheAbsentSeam(t *testing.T) {
	t.Parallel()
	if NoExternalInputs() != nil {
		t.Error("the no-seam sentinel must yield no service; anything else would attach something " +
			"a caller asked not to have")
	}
	in := NewPreviewInputs(NoExternalInputs(), nil, "r.yaml", "/ws", workspaceconfig.Default(), "v")
	if in.ExternalInputs() != nil {
		t.Error("an input built with the no-seam sentinel must carry no seam")
	}
	// And the constructed marker is set regardless, so naming the sentinel is a
	// legitimate construction rather than a way to trip the zero-value guard.
	if !in.constructed {
		t.Error("naming the no-seam sentinel is a decision, not a defective construction")
	}
}
