// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a DELIBERATELY non-conforming Wasm guest into the host's
// refusals of a plugin contract violation, and assert that each refusal names a
// condition and that the condition's code reaches the emitted diagnostic.
//
// The guest is the load-bearing part rather than a convenience. None of these
// conditions is reachable from any authored document: an Aggregate, a Release, a
// Recipe and a Manifest together decide which guest runs and what it is handed,
// and none of them decides what the guest hands back. So no corpus of YAML can
// produce one, and a proof that reads an identity off a hand-built error value
// would establish only that the identity was declared — which is satisfied while
// nothing can emit it.
//
// The guest source is at internal/wasm/testdata/nonconforming-step; it hand-writes
// its wire bytes because the generated codec is built from the schema and cannot
// express a payload the contract forbids.

// nonconformingStepLookup builds the non-conforming step guest and returns a
// lookup resolving any plugin URI to it. The build has no skip path: the Go
// toolchain targeting wasip1 ships with every Go distribution, so a fixture that
// reported itself skipped here would be a defect rather than an environment
// fact — and a silently skipped proof of an emitted code is indistinguishable
// from a proof that passed.
func nonconformingStepLookup(t *testing.T) pluginpath.Lookup {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("..", "..", "wasm", "testdata", "nonconforming-step"))
	if err != nil {
		t.Fatalf("resolve guest source: %v", err)
	}
	path := wasmtest.EnsureLocalGuestBuilt(t, src, "nonconforming-step.wasm")
	return pluginpath.NewResolver(
		pluginpath.ResolutionContext{Override: path}, pluginpath.PinPostureNone, pluginpath.PinMaps{}).Resolve
}

// runNonconformingStep drives one pipeline step through the real executor with
// the non-conforming guest, asking it for the named violation, and returns the
// error the host raised.
func runNonconformingStep(t *testing.T, violation string) error {
	t.Helper()
	lookup := nonconformingStepLookup(t)

	own := []EmittedFile{{
		Name:   "workload.yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n"),
	}}
	steps := []types.PipelineStep{{
		ID:       "nonconforming",
		Plugin:   "nonconforming-step.wasm",
		Function: "step",
		With:     "violation: " + violation + "\n",
	}}

	_, err := runNodePipeline(context.Background(), steps, own, nil, false, "helm", "",
		renderctx.RenderContext{}, &types.TemplateContext{}, 2, lookup, nil, "contract-node")
	return err
}

// assertEmitsCode asserts the error names the expected condition, that the code
// the diagnostic head carries is the one that condition registers, and that the
// registered sentence reaches the emitted text. It logs the emitted diagnostic
// so the proof is readable in the test output rather than only asserted.
func assertEmitsCode(t *testing.T, err error, condition error, wantShort string) {
	t.Helper()
	if err == nil {
		t.Fatal("the non-conforming guest produced no halt; the host must refuse the payload it returned")
	}
	if !errors.Is(err, condition) {
		t.Fatalf("expected %v in the error chain; got: %v", condition, err)
	}
	gotShort, ok := cerrors.ShortCodeOf(err)
	if !ok {
		t.Fatalf("the halt carries no short code, so a reader has nothing to quote; got: %v", err)
	}
	if gotShort != wantShort {
		t.Fatalf("short code = %q, want %q", gotShort, wantShort)
	}
	sentence, ok := cerrors.MessageOf(err)
	if !ok {
		t.Fatalf("the halt carries no registered sentence; got: %v", err)
	}
	emitted := cerrors.CauseChain(err)
	if !strings.Contains(emitted, sentence) {
		t.Fatalf("the emitted diagnostic does not carry the registered sentence.\nsentence: %s\nemitted: %s",
			sentence, emitted)
	}
	t.Logf("EMITTED [%s] %s", gotShort, emitted)
}

// TestNonconformingGuest_EmitFileWithNoFile drives the guest into the narrower of
// the two side-effect refusals: a kind the contract DOES define, arriving without
// the member its arm reads.
func TestNonconformingGuest_EmitFileWithNoFile(t *testing.T) {
	err := runNonconformingStep(t, "emit-file-absent")
	assertEmitsCode(t, err, ErrPipelineEmitFileAbsent(), "E0271")
}

// TestNonconformingGuest_UndefinedSideEffectKind drives the guest into the other
// side-effect refusal: a kind no arm matches at all. It is a separate condition
// from the one above, and driving both from one guest is what shows they are
// separately reachable rather than two spellings of one halt.
func TestNonconformingGuest_UndefinedSideEffectKind(t *testing.T) {
	err := runNonconformingStep(t, "kind-undefined")
	assertEmitsCode(t, err, ErrPipelineSideEffectKindUndefined(), "E0272")
}

// TestNonconformingGuest_ConformingSideEffectIsAccepted is the positive control,
// and it is what keeps the two tests above failing on their own subject. Without
// it a guest that never ran — one whose describe() was refused, say — would
// produce a halt for both cases and both assertions would still pass on the
// wrong cause.
func TestNonconformingGuest_ConformingSideEffectIsAccepted(t *testing.T) {
	if err := runNonconformingStep(t, "none"); err != nil {
		t.Fatalf("the conforming side effect must be accepted, so a halt above is attributable to the violation asked for; got: %v", err)
	}
}
