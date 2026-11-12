// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"testing"
)

// The subject here is which publications reach the wire, so every claim is
// asserted over the FRAMES that left the daemon rather than over the layers
// behind them. A publication suppressed and a publication sent leave a
// diagnostic store in exactly the same state, so no assertion over that store
// can tell them apart — which is the whole failure mode these tests exist for.
//
// 🔴 A PUBLICATION IS AN INSTRUCTION TO RE-ASK, WHICH IS WHY A REPEAT COSTS MORE
// THAN A WASTED FRAME. A client told a file's findings have changed replaces them
// and rebuilds whatever else it draws from the same compile; one of those things
// asks the compiler for a graph, and that request recomputes the findings and
// reports them again. So a frame carrying nothing new is the engine of a loop
// that compiles a workspace continuously with nobody typing.

// publishedFrames returns the aggrega/publishDiagnostics notifications on out, in
// order, with their params decoded.
func publishedFrames(t *testing.T, out *bytes.Buffer) []publishDiagnosticsParams {
	t.Helper()
	var pubs []publishDiagnosticsParams
	for _, body := range readAllFrames(t, out) {
		var f wireFrame
		if err := json.Unmarshal(body, &f); err != nil {
			t.Fatalf("decode frame: %v\nframe: %s", err, body)
		}
		if f.Method != "aggrega/publishDiagnostics" {
			continue
		}
		var params publishDiagnosticsParams
		if err := json.Unmarshal(f.Params, &params); err != nil {
			t.Fatalf("decode publishDiagnostics params: %v\nparams: %s", err, f.Params)
		}
		pubs = append(pubs, params)
	}
	return pubs
}

// advisory is one build-layer entry, for a layer a test replaces wholesale.
func advisory(file, message string) map[string][]Diagnostic {
	return map[string][]Diagnostic{
		file: {{Line: 1, Column: 1, Severity: editorSeverityWarning, Message: message}},
	}
}

// TestPublishDiagnostics_ARepeatCarryingNoNewsIsNotSent is the positive half of
// the suppression: the same advisory set, recomputed, reaches the client once.
//
// It drives the publication path the graph request drives — replace the whole
// build layer, then push every file the replacement touched — twice with
// identical input and no edit between, which is exactly the shape a panel left
// open produces. The layer names its file on both turns, because the layer's
// returned set is the files it MENTIONS rather than the files that moved; what
// decides the second frame is the comparison at the publication.
func TestPublishDiagnostics_ARepeatCarryingNoNewsIsNotSent(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	for turn := 0; turn < 2; turn++ {
		for _, f := range s.setRenderDiagnostics(advisory(file, "a socket name is a near miss"), nil) {
			_ = s.publishDiagnostics(ctx, f)
		}
	}
	flush()

	pubs := publishedFrames(t, &out)
	if len(pubs) != 1 {
		t.Fatalf("two identical recomputations published %d frames; want 1 — a second frame "+
			"restating what the client holds is what makes a panel that re-asks recompile "+
			"forever\nframes: %+v", len(pubs), pubs)
	}
	if len(pubs[0].Diagnostics) != 1 {
		t.Fatalf("the one frame carried %d diagnostics; want the advisory", len(pubs[0].Diagnostics))
	}
	// The layer is still recomputed and still names the file on the second turn,
	// so the single frame is the publication declining rather than the layer
	// having gone quiet. Without this the test would pass just as well against a
	// build that stopped raising the advisory at all.
	if got := s.setRenderDiagnostics(advisory(file, "a socket name is a near miss"), nil); len(got) != 1 || got[0] != file {
		t.Fatalf("the layer no longer names %s on a recomputation (%v), so the single frame "+
			"above measures the layer rather than the publication", file, got)
	}
}

// TestPublishDiagnostics_AChangedStateIsAlwaysSent is the negative control, and
// it is what separates a guard from a mute. A guard that suppressed the repeat
// above and also suppressed this has not fixed the loop, it has stopped reporting.
func TestPublishDiagnostics_AChangedStateIsAlwaysSent(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	for _, f := range s.setRenderDiagnostics(advisory(file, "the first finding"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	for _, f := range s.setRenderDiagnostics(advisory(file, "a different finding"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	flush()

	pubs := publishedFrames(t, &out)
	if len(pubs) != 2 {
		t.Fatalf("a genuinely changed advisory set published %d frames; want 2\nframes: %+v",
			len(pubs), pubs)
	}
	if got := pubs[1].Diagnostics[0].Message; got != "a different finding" {
		t.Errorf("the second frame carried %q; want the changed finding", got)
	}
}

// TestPublishDiagnostics_AClearingIsAlwaysSent pins the case a careless
// comparison breaks, and it breaks it silently: the author fixes the fault, the
// build stops raising the advisory, and the squiggle stays on screen because the
// frame that would have retracted it was judged uninteresting.
//
// An empty composed state is not equal to the state the client was told, so the
// retraction is news by the same rule that suppressed the repeat — one rule, both
// outcomes, rather than a special case for clearing that a later edit could miss.
func TestPublishDiagnostics_AClearingIsAlwaysSent(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	for _, f := range s.setRenderDiagnostics(advisory(file, "the finding the author then fixed"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	// The run that raises nothing: the layer empties, and its returned set names
	// the file it used to hold so the retraction reaches the client.
	for _, f := range s.setRenderDiagnostics(nil, nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	flush()

	pubs := publishedFrames(t, &out)
	if len(pubs) != 2 {
		t.Fatalf("an advisory that went away published %d frames; want 2 — the second one is "+
			"what takes the entry off the author's screen\nframes: %+v", len(pubs), pubs)
	}
	if len(pubs[1].Diagnostics) != 0 {
		t.Errorf("the retraction frame carried %d diagnostics; want none", len(pubs[1].Diagnostics))
	}
}

// TestPublishDiagnostics_AnotherLayerMovingIsNews is why the comparison is taken
// over the composed state rather than inside a layer.
//
// The build layer here is recomputed to exactly what it already held, so a
// comparison made inside that layer would find nothing to report — while the file
// genuinely has a new problem on it, raised by a different request against a
// different layer. The client would be left showing the advisory and none of the
// semantic findings, with nothing anywhere reporting a fault.
func TestPublishDiagnostics_AnotherLayerMovingIsNews(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	for _, f := range s.setRenderDiagnostics(advisory(file, "a standing advisory"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	// A validation writes the semantic layer for the same file. The build layer
	// is then recomputed unchanged, exactly as a graph request would.
	s.setDiagnostics(file, checkedText{}, []Diagnostic{
		{Line: 7, Column: 3, Severity: editorSeverityError, Message: "a semantic violation"},
	})
	for _, f := range s.setRenderDiagnostics(advisory(file, "a standing advisory"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	flush()

	pubs := publishedFrames(t, &out)
	if len(pubs) != 2 {
		t.Fatalf("a file whose OTHER layer gained a finding published %d frames; want 2 — a "+
			"comparison confined to the build layer is what loses this one\nframes: %+v",
			len(pubs), pubs)
	}
	if len(pubs[1].Diagnostics) != 2 {
		t.Fatalf("the second frame carried %d diagnostics; want both layers composed",
			len(pubs[1].Diagnostics))
	}
	if got := messages(pubs[1].Diagnostics); got[0] != "a semantic violation" || got[1] != "a standing advisory" {
		t.Errorf("the second frame carried %v; want the semantic finding then the advisory", got)
	}
}

// TestPublishDiagnostics_AFreshClientIsToldEverythingAgain pins what the
// handshake does about the record.
//
// A workspace entry outlives the connection that filled it — the registry keeps
// it so a reconnecting editor is answered from a warm catalog — while the editor
// in front of it has an empty problem list. Forgetting the record at the handshake
// is what keeps the startup flush a flush; without it the daemon would conclude it
// had already reported every file and stay silent on a workspace full of problems.
func TestPublishDiagnostics_AFreshClientIsToldEverythingAgain(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	for _, f := range s.setRenderDiagnostics(advisory(file, "a standing advisory"), nil) {
		_ = s.publishDiagnostics(ctx, f)
	}
	// The control, in the same run and against the same state: without the
	// handshake between them a second publication of this state is silent.
	_ = s.publishDiagnostics(ctx, file)

	s.forgetPublished()
	for _, f := range s.diagnosticsFiles() {
		_ = s.publishDiagnostics(ctx, f)
	}
	flush()

	pubs := publishedFrames(t, &out)
	if len(pubs) != 2 {
		t.Fatalf("published %d frames; want 2 — one for the advisory, one for the flush a "+
			"fresh client is owed, and none for the repeat in between\nframes: %+v",
			len(pubs), pubs)
	}
	if len(pubs[1].Diagnostics) != 1 || pubs[1].Diagnostics[0].Message != "a standing advisory" {
		t.Errorf("the flush frame carried %+v; want the advisory the workspace still holds",
			pubs[1].Diagnostics)
	}
}
