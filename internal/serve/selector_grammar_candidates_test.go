// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeSelectorGrammarFixture lays out a workspace whose selectors are written
// in shapes the selector grammar refuses, each beside a well-formed control that
// the same node satisfies or fails on its labels alone.
//
// The refused shapes are the two that, read leniently, admit exactly what they
// were written to exclude: "!tier=edge" read as "the label named tier=edge is
// absent", which holds for every node, and a selector made of separators, read
// as no requirement at all. So a projection that stopped refusing them would
// offer the edge node through them — the direction these assertions rule out.
func writeSelectorGrammarFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"node.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: api
  labels:
    tier: edge
spec:
  usesSchema:
    excludes-edge:
      selector: "!tier=edge"
    separators:
      selector: " , , "
    not-edge:
      selector: "tier!=edge"
`,
		"worker.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: worker
  labels:
    tier: backend
`,
		"10-excludes-edge.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: excludes-edge
spec:
  appliesTo: "!tier=edge"
`,
		"20-separators.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: separators
spec:
  appliesTo: " , , "
`,
		"30-edge-only.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: edge-only
spec:
  appliesTo: "tier=edge"
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, rel)
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// TestConnectionCandidatesOfferNoTargetThroughASelectorTheGrammarRefuses pins
// what the editor is offered for a socket whose selector the grammar refuses:
// the socket is still reported, with the selector it declares, and its target
// list is empty — the answer a build reaches, where the same selector halts
// rather than admitting a target. The control socket beside it is evaluated
// normally, so an empty list is not the whole response failing.
func TestConnectionCandidatesOfferNoTargetThroughASelectorTheGrammarRefuses(t *testing.T) {
	srv := newTestServer(t, writeSelectorGrammarFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, _, sockets := connectionCandidatesOverWire(t, srv, map[string]any{"node": "api"})

	for _, tc := range []struct {
		socket   string
		selector string
	}{
		{"excludes-edge", "!tier=edge"},
		{"separators", " , , "},
	} {
		got := socketNamed(t, sockets, tc.socket)
		if got["selector"] != tc.selector {
			t.Errorf("%s[selector] = %v, want the declared %q: %s", tc.socket, got["selector"], tc.selector, body)
		}
		if names := targetNames(t, got); len(names) != 0 {
			t.Errorf("%s targets = %v, want none — the grammar refuses %q: %s", tc.socket, names, tc.selector, body)
		}
	}

	if names := targetNames(t, socketNamed(t, sockets, "not-edge")); !slices.Equal(names, []string{"worker"}) {
		t.Errorf("not-edge targets = %v, want [worker]: %s", names, body)
	}
}

// TestAttachCandidatesOfferNoFeatureWhoseAppliesToTheGrammarRefuses pins the
// same for the Features offered for a node: a Feature whose appliesTo the
// grammar refuses is not offered, and the well-formed control is.
func TestAttachCandidatesOfferNoFeatureWhoseAppliesToTheGrammarRefuses(t *testing.T) {
	srv := newTestServer(t, writeSelectorGrammarFixture(t), &bytes.Buffer{}, &bytes.Buffer{})
	body, _, candidates := attachCandidatesOverWire(t, srv, "api")

	if names := candidateNames(candidates); !slices.Equal(names, []string{"edge-only"}) {
		t.Errorf("candidates = %v, want [edge-only] — excludes-edge and separators carry an appliesTo the grammar refuses: %s",
			names, body)
	}
}
