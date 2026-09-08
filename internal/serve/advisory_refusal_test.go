// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
)

// The graph request's failure path.
//
// A refusal publishes too, and it publishes what the run FOUND. Two layers of
// the computation have to hand back a partial for that to be possible: the
// pipeline returns the findings it had raised alongside the halt, and the preview
// computation carries them out beside its error envelope. A caller cannot publish
// what it is never given, so those partials are what make this path publishable
// at all rather than an implementation nicety.
//
// So the Problems panel a reader is looking at is recomputed by the refused
// request, and it is recomputed from that request's own findings — not left
// holding whatever the request before it wrote, and not cleared as though the run
// had learned nothing.
//
// That property is the subject here, and it is asserted over the FRAMES the
// server pushed AND over its own diagnostic map, because the two can disagree. A
// client cannot see the map: an entry set there and never pushed would satisfy an
// assertion over it and change nothing a reader sees. An entry pushed and never
// recorded is the mirror fault — visible once, then gone at the next unrelated
// publication.

// driveIrGraphOn runs one irGraph request against an already-configured server,
// writing the framed response and every pushed notification into out.
//
// It exists because the property under test spans two requests on ONE server —
// the diagnostic layer is server state, so a second server would start with an
// empty one and the assertion would pass without the refusal having cleared
// anything.
func driveIrGraphOn(t *testing.T, srv *Server, out *bytes.Buffer, id int, releaseRel string) {
	t.Helper()
	var in bytes.Buffer
	in.Write(framedRequest(id, "aggrega/irGraph", irGraphParams{Release: releaseRel}))
	srv.in = bufio.NewReader(&in)
	srv.out = out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, out)
}

// unrenderableRecipe rewrites the fixture's Recipe so its workload cannot be
// rendered, and changes nothing else.
//
// It is derived from the advisory fixture rather than written beside it so the
// two workspaces differ in exactly one thing: whether the render completes. A
// second hand-written workspace would be free to differ in something else, and
// the refusal could then be attributed to the wrong fault.
//
// The fault is a template the render cannot even parse. That places it in the
// render rather than in a gate ahead of it, which is what this test needs: a gate
// that refuses before the closure resolves leaves no resolved document for
// findings to be attributed to.
func unrenderableRecipe(t *testing.T, ws string) {
	t.Helper()
	writeFixtureFile(t, filepath.Join(ws, "catalog", "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: "{{ this action is never closed
`)
}

// TestGraphRefusalRepublishesTheLayerFromWhatTheRunReached is the proof for the
// second surface's failure path.
//
// It drives the same Release twice against one server. The first request reads
// the graph without rendering and succeeds, so the advisory its pre-render phases
// raised reaches the panel. The second renders the same Release with a workload
// the render cannot process, so it refuses. The refusal must republish the layer
// from what that run reached — which includes the advisory, because the phase
// that raises it runs ahead of the render that stopped.
//
// Why republication is the assertion and not an implementation detail: the entry
// left behind by the first request is a finding about a document, and the second
// request is a different question about the same document. A refusal that
// published nothing would leave the reader looking at an entry no run currently
// vouches for, with nothing on screen to say so — and a refusal that published an
// EMPTY layer would clear a finding the run had just made, which is the same loss
// wearing the opposite disguise.
func TestGraphRefusalRepublishesTheLayerFromWhatTheRunReached(t *testing.T) {
	t.Parallel()
	ws, releaseRel := advisoryGraphWorkspace(t)

	var out bytes.Buffer
	srv := newPinIsolationServer(t, ws, nil, &out, 1)

	// The seeding request: no render, so it succeeds and publishes the advisory.
	srv.buildOnInspect = false
	driveIrGraphOn(t, srv, &out, 1, releaseRel)
	seeded := out.String()
	if !strings.Contains(seeded, "unread_probe") {
		t.Fatalf("the first request published no advisory, so there is nothing for the refusal to have to "+
			"republish and this test asserts nothing.\npushed:\n%s", seeded)
	}

	// The refusing request: the render runs, over a workload it cannot process.
	aggPath := aggregatePathOf(t, srv, releaseRel)
	unrenderableRecipe(t, ws)
	out.Reset()
	srv.buildOnInspect = true
	driveIrGraphOn(t, srv, &out, 2, releaseRel)
	refused := out.String()

	if !strings.Contains(refused, `"error"`) {
		t.Fatalf("the second request did not refuse, so this is not the failure path the assertions below "+
			"are about.\nresponse:\n%s", refused)
	}
	if !strings.Contains(refused, "publishDiagnostics") {
		t.Fatalf("the refusal pushed no diagnostics notification at all. The findings a refused run reached "+
			"have no other route to the panel, and the entries the request before it wrote stay on screen "+
			"unattributed.\npushed:\n%s", refused)
	}

	// 🔴 The layer carries THIS run's advisory, and that is the headline property.
	//
	// The refusing run raised the finding in a phase ahead of the render and then
	// stopped in the render, so it held the advisory at the moment it refused. A
	// reader in the editor must see it: it is a finding about their document, and
	// losing it means re-running to discover what the run already knew.
	//
	// The token is the parameter name the fixture DECLARES, so the expectation comes
	// from the workspace rather than from the code under test. The implementation
	// reads it as input data and never as an expectation, which is what keeps this
	// from being satisfiable by a defect planted on both sides of the comparison.
	if !strings.Contains(refused, "unread_probe") {
		t.Errorf("the refusal published no entry naming the advisory this run raised. The finding was in hand "+
			"when the render stopped, so a reader is being shown a cleared panel for a run that had "+
			"something to say.\npushed:\n%s", refused)
	}

	// And the layer itself carries it, which is what the reader actually sees. The
	// pushed frame and the layer are separate assertions: an entry pushed and not
	// recorded disappears on the next unrelated publication, and one recorded and
	// not pushed is invisible until something else forces a refresh.
	got := srv.snapshotDiagnostics(aggPath)
	if len(got) == 0 {
		t.Fatalf("the layer for %s is empty after the refusal, so nothing the run found survived to the "+
			"place a reader reads", aggPath)
	}
	if !strings.Contains(strings.Join(diagMessages(got), "\n"), "unread_probe") {
		t.Errorf("the layer for %s carries %d entry(ies) and none names the advisory this run raised: %v",
			aggPath, len(got), diagMessages(got))
	}

	// Recorded so what a refused request actually puts on the panel is readable
	// from a verbose run rather than only inferable from the assertions that passed.
	t.Logf("the refusal published %d entry(ies) on %s: %v", len(got), aggPath, diagMessages(got))
}

// aggregatePathOf resolves the Aggregate path the advisories for this Release are
// attributed to, by asking the same computation the handler asks.
//
// Reading it from the computation rather than assembling it here is what keeps
// the assertion honest: a path spelled in the test would keep matching after the
// attribution moved to a different file, and the layer would then be asserted
// empty because nothing was ever published to the path being read.
func aggregatePathOf(t *testing.T, srv *Server, releaseRel string) string {
	t.Helper()
	preview, _ := srv.previewRelease(t.Context(), releaseRel, pipeline.Options{SkipRender: true})
	if preview == nil || preview.resolved == nil {
		t.Fatalf("the preview reached no resolution, so no file carries this Release's advisories")
	}
	return preview.resolved.AggregatePath
}
