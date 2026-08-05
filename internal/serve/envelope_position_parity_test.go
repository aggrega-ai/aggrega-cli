// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestEnvelopePositionParityAcrossSurfaces is the cross-surface pin for
// envelope-finding positions. The same malformed document is put through
// both surfaces a reader can encounter — the rendered error a terminal
// shows, and the diagnostic an editor shows — and the line and column each
// reports must be identical.
//
// This is the test that would have caught the defect. The two surfaces were
// unequal: the editor squiggled the offending key while the terminal named
// the key path and no position at all, so the same fault told a reader two
// different amounts depending on where they read it.
//
// The document is deliberately ASCII. Column numbers legitimately differ
// between position encodings, and the editor reports in whatever encoding
// its client negotiated, so a non-ASCII document would fail this test for a
// correct reason and hide the incorrect one. What must never differ is the
// walk that decides WHICH key a finding points at.
func TestEnvelopePositionParityAcrossSurfaces(t *testing.T) {
	t.Parallel()

	content := []byte(`apiVersion: aggrega.ai/v1alpha1
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
`)

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
	if !strings.Contains(err.Error(), "component.yaml: Component document violates its envelope schema") {
		t.Errorf("the header must still name the file and kind; got %q", err.Error())
	}
}
