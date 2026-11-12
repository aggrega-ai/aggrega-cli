// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"strings"
	"testing"
)

// spliceArtifact runs one already-evaluated helm-chart artifact body through the
// exported SpliceArtifactProvenance placer with the non-default-delimiter policy
// (a non-round-trippable file skips-and-warns rather than erroring) and returns
// the placed body plus any warnings. It mirrors what the helm-chart guest does per
// entry: the host has already template-evaluated the content, so the body passes
// in verbatim.
func spliceArtifact(t *testing.T, content string) (string, []string) {
	t.Helper()
	_, state, nodeCtx := newArtifactsCtx()
	labels := BuildAggregaProvenanceLabels(nodeCtx)
	annotations := BuildAggregaProvenanceAnnotations(nodeCtx, recipeURI(state))
	body, warning, err := SpliceArtifactProvenance("art.yaml", []byte(content), labels, annotations, false, nodeCtx.YAMLIndent, false)
	if err != nil {
		t.Fatalf("SpliceArtifactProvenance: %v", err)
	}
	var warns []string
	if warning != "" {
		warns = append(warns, warning)
	}
	return string(body), warns
}

// TestSplice_MultiDocStampsEveryDoc is the regression test for the latent
// silent-multi-doc-drop the splice fixes: a two-document stream where BOTH
// documents must receive provenance, every Helm token must survive, and the
// SECOND document must not be dropped (the single-yaml.Unmarshal path the splice
// replaced decoded only the first document and dropped the rest). Doc 1 has an
// existing labels block (append path); doc 2 has none (create path).
func TestSplice_MultiDocStampsEveryDoc(t *testing.T) {
	t.Parallel()
	body, warns := spliceArtifact(t, `apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-a
  labels:
    app: a
data:
  ns: "{{ .Release.Namespace }}"
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: cm-b
data:
  ns: "{{ .Release.Namespace }}"
`)
	if len(warns) != 0 {
		t.Errorf("multi-doc splice should not warn; got %v", warns)
	}
	// Second document survives (the silent-drop regression).
	if !strings.Contains(body, "name: cm-b") {
		t.Fatalf("second document was dropped (the multi-doc bug); body:\n%s", body)
	}
	// Both documents stamped: component-id appears once per doc.
	if got := strings.Count(body, "aggrega.ai/component-id"); got != 2 {
		t.Errorf("provenance should land in BOTH documents; component-id count = %d, want 2; body:\n%s", got, body)
	}
	// Doc 1's existing label preserved (append, not replace).
	if !strings.Contains(body, "app: a") {
		t.Errorf("existing label must be preserved; body:\n%s", body)
	}
	// Every Helm token survives byte-faithfully (count + presence).
	if got := strings.Count(body, "{{ .Release.Namespace }}"); got != 2 {
		t.Errorf("both Helm tokens must survive; count = %d, want 2; body:\n%s", got, body)
	}
}

// TestSplice_CreatesLabelsBlock pins the create path: metadata without a labels
// block gets one, with the untouched body byte-faithful.
func TestSplice_CreatesLabelsBlock(t *testing.T) {
	t.Parallel()
	body, warns := spliceArtifact(t, `apiVersion: v1
kind: ConfigMap
metadata:
  name: only-name
data:
  k: v
`)
	if len(warns) != 0 {
		t.Errorf("unexpected warning: %v", warns)
	}
	if !strings.Contains(body, "aggrega.ai/component-id: api") {
		t.Errorf("labels block should be created with provenance; body:\n%s", body)
	}
	if !strings.Contains(body, "name: only-name") || !strings.Contains(body, "data:\n  k: v") {
		t.Errorf("untouched body must stay byte-faithful (2-space, original); body:\n%s", body)
	}
}

// TestSplice_AppendsToExistingLabels pins the append path and that user labels
// are preserved alongside the appended provenance.
func TestSplice_AppendsToExistingLabels(t *testing.T) {
	t.Parallel()
	body, _ := spliceArtifact(t, `apiVersion: v1
kind: ConfigMap
metadata:
  name: n
  labels:
    team: payments
data:
  k: v
`)
	if !strings.Contains(body, "team: payments") {
		t.Errorf("existing label dropped; body:\n%s", body)
	}
	if !strings.Contains(body, "aggrega.ai/component-id: api") {
		t.Errorf("provenance not appended; body:\n%s", body)
	}
}

// TestSplice_TrickyInputsStayByteFaithful runs the byte-offset logic on the
// inputs flagged as risky (YAML anchors / merge keys, folded + literal blocks,
// leading comments) rather than reasoning about them: the splice must inject provenance
// AND leave every other byte identical. The whole non-metadata body is asserted
// present verbatim.
func TestSplice_TrickyInputsStayByteFaithful(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
		want string // a verbatim slice of the untouched body that must survive
	}{
		{
			name: "leading comment + folded and literal blocks",
			body: `# a leading comment
apiVersion: v1
kind: ConfigMap
metadata:
  name: blocks
data:
  folded: >
    one two
    three four
  literal: |
    line1
    line2
`,
			want: "data:\n  folded: >\n    one two\n    three four\n  literal: |\n    line1\n    line2\n",
		},
		{
			name: "anchor and merge key",
			body: `apiVersion: v1
kind: ConfigMap
metadata:
  name: anchored
data:
  base: &base
    a: 1
  merged:
    <<: *base
    b: 2
`,
			want: "  base: &base\n    a: 1\n  merged:\n    <<: *base\n    b: 2\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			body, _ := spliceArtifact(t, tc.body)
			if !strings.Contains(body, "aggrega.ai/component-id: api") {
				t.Errorf("provenance not injected; body:\n%s", body)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("untouched body not byte-faithful;\nwant verbatim:\n%q\ngot body:\n%s", tc.want, body)
			}
			// The leading comment, if any, must survive at the top.
			if strings.HasPrefix(tc.body, "#") && !strings.HasPrefix(body, "# a leading comment") {
				t.Errorf("leading comment not preserved; body:\n%s", body)
			}
		})
	}
}

// TestSplice_FlowMetadataFallsBackButStillInjects pins that a non-spliceable
// metadata shape (flow-style mapping) routes to the multi-doc round-trip fallback,
// which still injects provenance — no coverage regression vs the round-trip path.
func TestSplice_FlowMetadataFallsBackButStillInjects(t *testing.T) {
	t.Parallel()
	body, warns := spliceArtifact(t, `apiVersion: v1
kind: ConfigMap
metadata: {name: flowy}
data:
  k: v
`)
	if len(warns) != 0 {
		t.Errorf("fallback should inject, not warn; got %v", warns)
	}
	if !strings.Contains(body, "aggrega.ai/component-id: api") {
		t.Errorf("flow-metadata case should still inject via the round-trip fallback; body:\n%s", body)
	}
}
