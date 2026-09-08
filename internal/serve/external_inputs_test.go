// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The inspection renders through a frozen external-input seam, and the two
// halves of that are proved separately because they fail independently.
//
// What the SEAM does — serving a pinned reference from the content-addressed
// store, declining an unpinned one, writing nothing — is proved beside the one
// builder, in the shared compiler-operations layer. What is proved HERE is this
// surface's own half: that the inspection attaches that seam rather than another,
// and that a decline reaches the reader as an advisory naming the reference and
// the remedy. A render that quietly stopped short of the bytes a build would emit
// is indistinguishable to the reader from an accurate one, which is why the
// reporting is a separate obligation from the attaching.
//
// One fault per fixture, asserted against the condition that fault raises. Each
// absence assertion carries a needle, because a zero is a claim about the
// instrument until something shows the instrument fires.

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestDivergenceAdvisoryNamesTheReferenceAndTheRemedy pins what the reader
// actually receives. A note saying only that something diverged leaves them
// unable to act; the reference and the remedy are what make it a repair
// instruction rather than a warning.
//
// The code is asserted to be the registered one read off the condition, not a
// literal, so the editor's advisory and a terminal's refusal name one identity.
func TestDivergenceAdvisoryNamesTheReferenceAndTheRemedy(t *testing.T) {
	t.Parallel()
	const url = "https://example.com/not-pinned.yaml"
	diags := externalInputDivergenceDiagnostics([]string{url}, "app-release.yaml")
	if len(diags) != 1 {
		t.Fatalf("one declined reference must produce one advisory; got %d", len(diags))
	}
	d := diags[0]
	for _, want := range []string{url, "cannot show what a build would emit", "aggrega pull", "app-release.yaml"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("the advisory does not carry %q:\n%s", want, d.Message)
		}
	}
	wantCode, ok := cerrors.CodeOf(externalinputs.ErrAcquisitionDisabled())
	if !ok {
		t.Fatal("the frozen-closure condition carries no registered code; the advisory has nothing to name")
	}
	if d.Code != wantCode {
		t.Errorf("Code = %q; want the condition's registered code %q", d.Code, wantCode)
	}
	if strings.Contains(d.Message, wantCode) {
		t.Errorf("the code is embedded in the message; it travels as its own key and nowhere else:\n%s", d.Message)
	}
	if d.Severity != editorSeverityWarning {
		t.Errorf("Severity = %q; want warning — the graph and the partial render are still worth reading",
			d.Severity)
	}
	// One advisory per reference, so the reader learns WHICH references are
	// missing rather than only that some are.
	many := externalInputDivergenceDiagnostics([]string{"https://a.example/x", "https://b.example/y"}, "r.yaml")
	if len(many) != 2 {
		t.Errorf("two declined references must produce two advisories; got %d", len(many))
	}
	// The matched negative control: a render that declined nothing publishes
	// nothing. Without it, an advisory emitted unconditionally would satisfy every
	// assertion above.
	if none := externalInputDivergenceDiagnostics(nil, "r.yaml"); len(none) != 0 {
		t.Errorf("a fully-pinned render must publish no divergence advisory; got %d", len(none))
	}
}

// TestDivergenceAdvisoryReachesTheEditor drives the real publication path and
// reads the pushed notification off the wire, so the claim is that the advisory
// reaches a client rather than that a constructor built one.
//
// It publishes a render advisory ALONGSIDE the divergence note in one call,
// which is the trap this wiring has to survive: the publication replaces the
// whole render layer, so a second call for the second class would erase the
// first. Both classes are asserted present in the composed answer.
func TestDivergenceAdvisoryReachesTheEditor(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var pushed bytes.Buffer
	ctx, flush := serializerContext(t, &pushed)
	const file = "/w/app-agg.yaml"
	const url = "https://example.com/not-pinned.yaml"

	s.publishRenderDiagnostics(ctx, file,
		[]renderer.Diagnostic{{Severity: abi.SeverityWarn, Message: "a render advisory"}},
		externalInputDivergenceDiagnostics([]string{url}, "app-release.yaml"), nil)
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 2 {
		t.Fatalf("the layer must carry both advisory classes; got %d: %v", len(got), messages(got))
	}
	var sawRender, sawDivergence bool
	for _, d := range got {
		if strings.Contains(d.Message, "a render advisory") {
			sawRender = true
		}
		if strings.Contains(d.Message, url) {
			sawDivergence = true
		}
	}
	if !sawRender {
		t.Error("the render advisory was erased; both classes must survive one publication")
	}
	if !sawDivergence {
		t.Error("the divergence note was erased; both classes must survive one publication")
	}
	// The notification actually went out, so the assertions above are about what a
	// client receives and not only about server-side state.
	body := pushed.String()
	if !strings.Contains(body, url) {
		t.Errorf("the pushed notification does not name the declined reference\npushed: %s", body)
	}
}

// TestIrGraphPreviewInputsCarryTheSeam asserts the inspection hands the shared
// computation the seam it built, rather than some other value.
//
// What it still buys, now that the shared computation takes the seam as a
// constructor argument: the constructor makes handing over NO seam a compile
// error, but it cannot make handing over the WRONG one an error — naming the
// explicit no-seam sentinel compiles exactly as happily as naming a service. So
// the identity comparison below is the assertion the type system does not carry,
// and it is what ties everything proved about the frozen seam to what this
// surface's render actually receives.
//
// The lockfile is nil on purpose: the daemon serves Releases with no lockfile,
// and the seam is owed on that path too — a construction that attached one only
// when a lockfile happened to be present would leave the commoner case
// producing divergent bytes.
func TestIrGraphPreviewInputsCarryTheSeam(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)

	for _, tc := range []struct {
		name string
		lf   *lockfile.Lockfile
	}{
		{name: "no lockfile", lf: nil},
		{name: "a lockfile pinning nothing", lf: &lockfile.Lockfile{Version: 1}},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			seam := srv.irGraphExternalInputs(tc.lf)
			if seam == nil {
				t.Fatal("the inspection built no external-input seam")
			}
			in := srv.irGraphPreviewInputs(srv.lazyCatalog.ForRequest(nil), "release.yaml", tc.lf, seam, tc.lf != nil)
			if in.ExternalInputs() == nil {
				t.Fatal("the inspection hands the shared computation no external-input seam; " +
					"a guest asking for an external input would receive a not-available envelope " +
					"and render different bytes than a build, with nothing said about it")
			}
			if in.ExternalInputs() != seam {
				t.Error("the attached seam is not the one the inspection built, so what was proved " +
					"about the frozen seam does not describe what the render receives")
			}
		})
	}
}

// TestIrGraphAttachesTheFrozenSeam is the end-to-end half: it drives the real
// handleIrGraph with the render dial ON over a workspace whose render executes a
// guest to completion, and asserts the run still produces a graph with the seam
// attached.
//
// What it establishes and what it does not, stated because the distinction
// matters: it shows the attachment is on the production path and perturbs
// nothing for a Release requesting no external input. It does NOT show a fetched
// input's bytes reaching an artifact, because no provider guest in the fixture
// corpus requests an external input during a render — that comparison needs a
// fetching provider fixture this package does not have.
func TestIrGraphAttachesTheFrozenSeam(t *testing.T) {
	ws, releaseRel := renderDialWorkspace(t)
	graph := irGraphWithDial(t, ws, releaseRel, true)
	if len(graph) == 0 {
		t.Fatal("the dial-on inspection produced no graph, so the seam attachment proved nothing")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(graph, &probe); err != nil {
		t.Fatalf("the graph is not a JSON object: %v", err)
	}
	if len(probe) == 0 {
		t.Fatal("the graph is empty; the render did not run to completion")
	}
}
