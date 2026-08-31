// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// envelopeParityDocument is the one malformed document both cross-surface pins
// in this file put through both surfaces: a Release carrying developer values:
// on a boundary component, which the Release envelope bans. One document for
// both pins, so the finding whose position is compared is the finding whose
// wording is compared.
//
// The document is deliberately ASCII — see the position pin below for why.
const envelopeParityDocument = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: r
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./d
      components:
        api:
          values:
            port: 9999
`

// TestEnvelopePositionParityAcrossSurfaces is the cross-surface pin for
// envelope-finding positions. The same malformed document is put through
// both surfaces a reader can encounter — the rendered error a terminal
// shows, and the diagnostic an editor shows — and the line and column each
// reports must be identical.
//
// It pairs a diagnostic with a finding on the FINDING's own text, so it is
// deliberately blind to whatever each surface says before that text: a change
// to the leading sentence must not redden a test about positions. That blind
// spot is covered by the wording pin below, and the two together are what make
// "one document, one verdict" a claim about both halves of the message.
//
// The failure class it excludes: the editor squiggling the offending key while
// the terminal names the key path and no position at all, so one fault tells a
// reader two different amounts depending on where they read it.
//
// The document is deliberately ASCII. Column numbers legitimately differ
// between position encodings, and the editor reports in whatever encoding
// its client negotiated, so a non-ASCII document would fail this test for a
// correct reason and hide the incorrect one. What must never differ is the
// walk that decides WHICH key a finding points at.
func TestEnvelopePositionParityAcrossSurfaces(t *testing.T) {
	t.Parallel()

	content := []byte(envelopeParityDocument)

	// The terminal surface: the structured error the CLI renders.
	_, err := envelope.ValidateBytes(envelope.KindRelease, content, "release.yaml")
	var envErr *envelope.Error
	if !errors.As(err, &envErr) {
		t.Fatalf("the document must fail the envelope with a structured error; got %v", err)
	}
	if len(envErr.Positions) == 0 {
		t.Fatal("the rendered error must carry a position for at least one finding")
	}

	// The editor surface: the diagnostics the daemon publishes.
	srv := newTestServer(t, t.TempDir(), nil, nil)
	diags, has := srv.envelopeDiagnosticsForBuffer("release.yaml", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("the same document must produce editor diagnostics; has=%v diags=%d", has, len(diags))
	}

	// Every diagnostic the editor emits corresponds to a finding the
	// terminal also reports, so each one's position must match the position
	// the terminal resolved for that same field path.
	compared := 0
	for _, f := range leafmostFindings(envErr.Findings) {
		loc, ok := envErr.Positions[f.FieldPath]
		if !ok {
			continue
		}
		var matched bool
		for _, d := range diags {
			if !strings.Contains(d.Message, f.Message) {
				continue
			}
			matched = true
			if d.Line != loc.Line || d.Column != loc.Column {
				t.Errorf("field path %q: the editor reports %d:%d and the terminal reports %d:%d; one document, one position",
					f.FieldPath, d.Line, d.Column, loc.Line, loc.Column)
			}
		}
		if !matched {
			t.Errorf("field path %q is positioned by the terminal but appears in no editor diagnostic", f.FieldPath)
			continue
		}
		compared++
	}
	if compared == 0 {
		t.Fatal("no field path was compared across the two surfaces; the parity check proved nothing")
	}
}

// TestEnvelopeSentenceParityAcrossSurfaces is the cross-surface pin for the
// envelope condition's WORDING, and the companion to the position pin above.
// The same malformed document goes through both surfaces a reader can
// encounter, and each must open with the sentence the condition is registered
// with — so a reader is told which condition was met in the same words wherever
// they read it.
//
// The expected sentence is read off the condition rather than written out here,
// and that is what makes the assertion worth having: a test naming the words
// itself passes for as long as both surfaces match the test, drift from the
// identity included. Read off the condition, it can only pass while both
// surfaces are composing from that one home.
//
// The two surfaces do NOT render the same whole message, and should not. The
// terminal groups every finding under one header and closes with the advice for
// the path; the editor emits one entry per finding, each positioned on its own
// key, and repeats neither the file it is already attached to nor advice it
// would say once per squiggle. What has to converge is the sentence naming the
// condition, which is the part a reader uses to recognise it.
func TestEnvelopeSentenceParityAcrossSurfaces(t *testing.T) {
	t.Parallel()

	content := []byte(envelopeParityDocument)
	sentence := envelope.ErrEntityEnvelope().Error()

	// The terminal surface: the message the rendered error carries.
	_, err := envelope.ValidateBytes(envelope.KindRelease, content, "release.yaml")
	if err == nil {
		t.Fatal("the document must fail the envelope")
	}
	if !strings.HasPrefix(err.Error(), sentence) {
		t.Errorf("the rendered error must open with the registered sentence %q; got %q", sentence, err.Error())
	}

	// The editor surface: every diagnostic the daemon publishes for the same
	// bytes. Every one, not the first: a surface that led with the sentence on
	// one entry and not on another would still satisfy a check of diags[0].
	srv := newTestServer(t, t.TempDir(), nil, nil)
	diags, has := srv.envelopeDiagnosticsForBuffer("release.yaml", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("the same document must produce editor diagnostics; has=%v diags=%d", has, len(diags))
	}
	for _, d := range diags {
		if !strings.HasPrefix(d.Message, sentence) {
			t.Errorf("every editor diagnostic must open with the registered sentence %q; got %q", sentence, d.Message)
		}
		if n := strings.Count(d.Message, sentence); n != 1 {
			t.Errorf("the sentence must be said once per diagnostic, not twice; said %d times in %q", n, d.Message)
		}
	}
}

// TestEnvelopeErrorTextCarriesPosition pins the terminal surface on its own
// terms: the rendered message names a line and a column, not only a key
// path. Reading the position off the structured error would not prove this —
// the message is what a reader actually sees.
func TestEnvelopeErrorTextCarriesPosition(t *testing.T) {
	t.Parallel()

	content := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/probe
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [old_key]
`)
	_, err := envelope.ValidateBytes(envelope.KindComponent, content, "component.yaml")
	if err == nil {
		t.Fatal("the retired key must fail the envelope")
	}
	// spec.secretNames sits on line 9 at column 3. The header names the
	// file, so the finding carries the position alone.
	if !strings.Contains(err.Error(), "; 9:3: spec.secretNames:") {
		t.Errorf("the rendered message must point at the offending key's line and column; got %q", err.Error())
	}
	// The header opens with the condition's registered sentence — read off the
	// condition, not restated here — and names the kind and the file after it.
	if want := envelope.ErrEntityEnvelope().Error(); !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the header must open with the registered sentence %q; got %q", want, err.Error())
	}
	if !strings.Contains(err.Error(), "(Component, at component.yaml)") {
		t.Errorf("the header must still name the kind and the file; got %q", err.Error())
	}
}
