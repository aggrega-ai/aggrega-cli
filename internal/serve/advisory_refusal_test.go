// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// The graph request's failure path.
//
// A refusal rebuilds the panel too, and it rebuilds it from what the run FOUND.
// Two layers of the computation have to hand back a partial for that to be
// possible: the pipeline returns the findings it had raised alongside the halt,
// and the preview computation carries them out beside its error envelope. A
// caller cannot publish what it is never given, so those partials are what make
// this path publishable at all rather than an implementation nicety.
//
// So the Problems panel a reader is looking at is recomputed by the refused
// request, and it is recomputed from that request's own findings — not left
// holding whatever the request before it wrote, and not cleared as though the run
// had learned nothing.
//
// That property is the subject here, and it is asserted over the VIEW a client is
// left holding AND over the server's own diagnostic map, because the two can
// disagree. A client cannot see the map: an entry set there that never reaches the
// view satisfies an assertion over the map and changes nothing a reader sees. An
// entry that reaches the view and is absent from the map is the mirror fault —
// visible once, then gone at the next unrelated publication.
//
// The view is replayed from the frames the server pushed, which is what a client
// does with them. It is not read back from the server's own record of what it has
// sent: that record is written by the publication being asserted on, so reading it
// would compare the server against itself and agree whatever it did.

// driveIrGraphOn runs one irGraph request against an already-configured server,
// writing the framed response and every pushed notification into out.
//
// It exists because the property under test spans two requests on ONE server —
// the diagnostic layer is server state, so a second server would start with an
// empty one and every assertion below would be about a panel nothing had ever
// written to.
func driveIrGraphOn(t *testing.T, srv *Server, out *bytes.Buffer, id int, releaseRel string) {
	t.Helper()
	driveRequestOn(t, srv, out, id, "aggrega/irGraph", irGraphParams{Release: releaseRel})
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
	writeFixtureFile(t, filepath.Join(ws, "catalog", "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: "{{ this action is never closed
`)
}

// clientView replays the frames a client received and returns what it is left
// showing for file, with told reporting whether it was told anything about that
// file at all. The last frame naming a file is what that file's entries are: a
// publication replaces a file's list rather than adding to it.
//
// Being told nothing is a distinct outcome from being told a file is clean, which
// is why told is returned rather than folded into an empty view.
//
// Both sides of the path comparison go through the canonicalisation every per-file
// store here uses, so a frame naming the document in one spelling and a resolution
// naming it in another are one file rather than two that never meet.
func clientView(srv *Server, frames []publishDiagnosticsParams, file string) (view []Diagnostic, told bool) {
	key := srv.canonicalFile(file)
	for _, f := range frames {
		if srv.canonicalFile(f.File) == key {
			view, told = f.Diagnostics, true
		}
	}
	return view, told
}

// sameView reports whether two renderings of one file's entries show a reader the
// same thing. The wire spells "no entries" as an empty array and the store spells
// it as no slice at all; those denote one view, and every other difference — a
// position moved, a message reworded, an entry added — is a difference a reader
// would see.
func sameView(a, b []Diagnostic) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
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
// 🔴 THE PROPERTY IS WHAT THE READER IS LEFT LOOKING AT, AND A PUSHED FRAME IS NOT
// HOW TO READ IT. A frame goes out when the composed state differs from what the
// client already holds, so a refusal that reaches exactly the state the reader is
// already shown sends nothing and leaves that reader correct. Demanding a frame
// here would be demanding one that carries no news — and a client told its
// findings have changed re-asks, which is the recompilation loop the publication
// comparison exists to end. So the assertion is over the view: what the client
// holds, replayed from the frames it received, against what the server composes
// for that file. It reads the same whether the refusal had news to send or not.
//
// Why that view is the assertion and not an implementation detail: the entry left
// behind by the first request is a finding about a document, and the second
// request is a different question about the same document. A refusal that left
// the composed state empty clears a finding the run had just made; one that left
// the earlier request's entry standing shows the reader something no run currently
// vouches for. Those are the same loss wearing opposite disguises, and they are
// caught here by different assertions — the first by the entry the view and the
// layer must both name, the second by the entry planted below on the layer and on
// the reader's screen, which only a republication takes away.
func TestGraphRefusalRepublishesTheLayerFromWhatTheRunReached(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	ws, releaseRel := advisoryGraphWorkspace(t)

	var out bytes.Buffer
	srv := newPinIsolationServer(t, ws, nil, &out, 1)

	// The seeding request: no render, so it succeeds and publishes the advisory.
	srv.buildOnInspect = false
	driveIrGraphOn(t, srv, &out, 1, releaseRel)
	aggPath := aggregatePathOf(t, srv, releaseRel)
	seeded := out.String()
	received := publishedFrames(t, &out)
	view, told := clientView(srv, received, aggPath)
	if !told || !strings.Contains(strings.Join(diagMessages(view), "\n"), "unread_probe") {
		t.Fatalf("the first request left the client holding no advisory for %s, so there is nothing for "+
			"the refusal to have to republish and this test asserts nothing.\npushed:\n%s", aggPath, seeded)
	}

	// An entry no run raises, put on the layer and pushed, so the reader is left
	// looking at something only a republication takes away.
	//
	// It stands in for what a reader is really looking at when a request refuses:
	// whatever the request before it left there. A distinguishable entry is what
	// lets the assertions below tell a panel the refusal rebuilt from a panel it
	// never touched — the two are identical on this fixture otherwise, because the
	// refusing run reaches exactly what the seeding run reached.
	const staleEntry = "a stale entry no run raised"
	pushCtx, flushPush := serializerContext(t, &out)
	for _, f := range srv.setRenderDiagnostics(map[string][]Diagnostic{
		aggPath: {{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: staleEntry}},
	}, nil) {
		_ = srv.publishDiagnostics(pushCtx, f)
	}
	flushPush()
	received = append(received, publishedFrames(t, &out)...)
	if view, _ = clientView(srv, received, aggPath); !strings.Contains(strings.Join(diagMessages(view), "\n"), staleEntry) {
		t.Fatalf("the client does not hold the entry planted for the refusal to take away (%v), so what "+
			"the refusal leaves on screen cannot be told from what was already there", diagMessages(view))
	}

	// The refusing request: the render runs, over a workload it cannot process.
	unrenderableRecipe(t, ws)
	out.Reset()
	srv.buildOnInspect = true
	driveIrGraphOn(t, srv, &out, 2, releaseRel)
	refused := out.String()

	if !strings.Contains(refused, `"error"`) {
		t.Fatalf("the second request did not refuse, so this is not the failure path the assertions below "+
			"are about.\nresponse:\n%s", refused)
	}
	received = append(received, publishedFrames(t, &out)...)

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
	got := srv.snapshotDiagnostics(aggPath)
	if len(got) == 0 {
		t.Fatalf("the layer for %s is empty after the refusal, so nothing the run found survived to the "+
			"place a reader reads", aggPath)
	}
	if strings.Contains(strings.Join(diagMessages(got), "\n"), staleEntry) {
		t.Fatalf("the layer for %s still carries an entry no run raised, so the refusal did not rebuild it "+
			"from what this run reached and every assertion here is about the request before it: %v",
			aggPath, diagMessages(got))
	}
	if !strings.Contains(strings.Join(diagMessages(got), "\n"), "unread_probe") {
		t.Errorf("the layer for %s carries %d entry(ies) and none names the advisory this run raised: %v",
			aggPath, len(got), diagMessages(got))
	}

	// And the client is left holding it, which is what a reader actually sees. The
	// view and the layer are separate assertions: an entry the view carries and the
	// layer does not disappears at the next unrelated publication, and one the layer
	// carries and the view does not is invisible until something forces a refresh.
	view, told = clientView(srv, received, aggPath)
	if !told {
		t.Fatalf("the client was told nothing about %s across either request, so it is showing a reader "+
			"an empty panel for a document two runs have findings on", aggPath)
	}
	if !sameView(view, got) {
		t.Errorf("after the refusal the client is showing %v for %s while the server composes %v. A reader "+
			"is looking at something no run currently vouches for, and nothing on screen says so.",
			diagMessages(view), aggPath, diagMessages(got))
	}
	if !strings.Contains(strings.Join(diagMessages(view), "\n"), "unread_probe") {
		t.Errorf("the client holds no entry naming the advisory this run raised: %v. The finding was in "+
			"hand when the render stopped, so a reader is being shown a cleared panel for a run that had "+
			"something to say.", diagMessages(view))
	}

	// Recorded so what a refused request actually leaves on the panel is readable
	// from a verbose run rather than only inferable from the assertions that passed.
	t.Logf("after the refusal %s carries %d entry(ies): %v; the client was pushed %d frame(s) in total",
		aggPath, len(got), diagMessages(got), len(received))
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
