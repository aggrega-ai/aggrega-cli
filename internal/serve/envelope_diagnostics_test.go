// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func parseDoc(t *testing.T, s string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(s), &n); err != nil {
		t.Fatalf("parse: %v", err)
	}
	return &n
}

// TestEnvelopeDiagnosticsForBuffer_PositionBetterThan1To1 pins that an
// envelope finding squiggles the offending key, not line 1: a Release
// with values: on a boundary component lands the diagnostic on the
// boundary path (line > 1) via the Tier-A position walk, and carries the
// rendered envelope framing.
func TestEnvelopeDiagnosticsForBuffer_PositionBetterThan1To1(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
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
	diags, has := srv.envelopeDiagnosticsForBuffer(content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings; has=%v diags=%d", has, len(diags))
	}
	d := diags[0]
	if d.Line <= 1 {
		t.Errorf("diagnostic should land on the offending key (line > 1), not line 1; got line %d", d.Line)
	}
	if !strings.Contains(d.Message, "violates its envelope schema") {
		t.Errorf("diagnostic must carry the envelope framing; got %q", d.Message)
	}
}

// TestEnvelopeDiagnosticsForBuffer_NonAggregaGuard pins the guard at the
// unit level: foreign YAML and unknown kinds produce no envelope leg
// activity (has=false), so the daemon never lint-bombs them.
func TestEnvelopeDiagnosticsForBuffer_NonAggregaGuard(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	cases := map[string]string{
		"k8s-manifest": "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: x\n",
		"compose-file": "services:\n  web:\n    image: nginx\n",
		"unknown-kind": "apiVersion: aggrega.ai/v1alpha1\nkind: Nonsense\nspec: {}\n",
		"bare-yaml":    "just: data\n",
	}
	for name, content := range cases {
		name, content := name, content
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags, has := srv.envelopeDiagnosticsForBuffer([]byte(content), parseDoc(t, content))
			if has || diags != nil {
				t.Errorf("%s: foreign/unknown doc must not engage the envelope leg; has=%v diags=%d", name, has, len(diags))
			}
		})
	}
}

// TestEnvelopeDiagnosticsForBuffer_CleanFallsThrough pins that a
// well-formed aggrega document produces no findings (has=false), so the
// handler falls through to its existing flow (Tier-4 / cascade) unchanged.
func TestEnvelopeDiagnosticsForBuffer_CleanFallsThrough(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte(`apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: a
  version: "1.0.0"
spec:
  components: {}
`)
	if diags, has := srv.envelopeDiagnosticsForBuffer(content, parseDoc(t, string(content))); has || diags != nil {
		t.Errorf("clean aggrega doc must fall through (has=false); has=%v diags=%d", has, len(diags))
	}
}
