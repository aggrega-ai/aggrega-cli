// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// helmShape is one corpus entry: a chart file's bytes and whether the codec is
// expected to produce a patchable (structured) unit for its single document.
type helmShape struct {
	name       string
	body       string
	structured bool // true → the file's (single) unit carries a tree (PATCH target)
}

// realHelmShapes is the corpus of real Helm template shapes plus value-position
// holes. Control-flow regions, interpolated scalars, and structure-emitting
// injections are expected opaque; clean value-position holes and non-template
// files are expected structured.
func realHelmShapes() []helmShape {
	return []helmShape{
		// --- value-position holes: structured (patchable) ---
		{"plain-value-hole", "image:\n  repository: {{ .Values.image.repository }}\n  tag: {{ .Values.image.tag }}\n", true},
		{"colon-in-action", "metadata:\n  annotations:\n    note: {{ printf \"%s: %s\" .Values.a .Values.b | quote }}\n", true},
		{"quoted-action-brace", "data:\n  tmpl: {{ \"}}\" }}\n  other: {{ .Values.x }}\n", true},
		{"dict-literal-nested-brace", "data:\n  x: {{ dict \"a\" 1 | toJson }}\n", true},
		{"value-with-quote-pipe", "spec:\n  replicas: {{ .Values.replicaCount | default 1 }}\n", true},
		{"non-template-values", "# a comment\nimage:\n  repository: nginx\n  tag: \"1.25\"\n", true},

		// --- control-flow / structural: opaque ---
		{"range", "data:\n{{- range $k, $v := .Values.config }}\n  {{ $k }}: {{ $v | quote }}\n{{- end }}\n", false},
		{"if", "spec:\n  replicas: {{ .Values.replicaCount }}\n{{- if .Values.autoscaling.enabled }}\n  strategy:\n    type: RollingUpdate\n{{- end }}\n", false},
		{"define", "{{- define \"mychart.labels\" -}}\napp.kubernetes.io/name: {{ .Chart.Name }}\n{{- end }}\napiVersion: v1\n", false},
		{"include-nindent", "metadata:\n  labels:\n    {{- include \"mychart.selectorLabels\" . | nindent 4 }}\n", false},
		{"root-level-conditional", "{{- if .Values.serviceAccount.create }}\napiVersion: v1\nkind: ServiceAccount\n{{- end }}\n", false},
		{"with-trims", "data:\n  {{- with .Values.foo }}\n  v: {{ . }}\n  {{- end }}\n", false},
		{"interpolated-scalar", "spec:\n  image: \"{{ .Values.image.repository }}:{{ .Values.image.tag }}\"\n", false},
		{"toyaml-injection", "spec:\n  resources:\n    {{- toYaml .Values.resources | nindent 4 }}\n", false},
	}
}

// multidocAdversarialShapes covers the multi-document adversarial classes: a
// `---` separator between two templated documents, a `---` inside a block scalar
// that must NOT split, and a mixed file where one document is a clean value hole
// and another is control flow.
func multidocAdversarialShapes() []helmShape {
	return []helmShape{
		{"multidoc-two-value-holes", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Values.cm.name }}\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: {{ .Values.secret.name }}\n", false /* per-document, asserted separately */},
		{"sep-inside-block-scalar", "data:\n  script: |\n    line one\n    ---\n    line three with {{ .Values.token }}\n", true},
		{"multidoc-mixed-value-and-controlflow", "kind: ConfigMap\nmetadata:\n  name: {{ .Values.a }}\n---\ndata:\n{{- range $k, $v := .Values.m }}\n  {{ $k }}: {{ $v }}\n{{- end }}\n", false},
	}
}

// TestHelmCodec_RoundTripIdentity is the byte-for-byte identity gate: every
// corpus shape round-trips through Segment → Reassemble with no patches.
func TestHelmCodec_RoundTripIdentity(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	all := append(realHelmShapes(), multidocAdversarialShapes()...)
	for _, sh := range all {
		sh := sh
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()
			body := []byte(sh.body)
			units, err := codec.Segment(sh.name, body)
			if err != nil {
				t.Fatalf("Segment(%q): %v", sh.name, err)
			}
			// No patches → identity.
			out, _, err := codec.Reassemble(sh.name, body, units, nil)
			if err != nil {
				t.Fatalf("Reassemble(%q): %v", sh.name, err)
			}
			if string(out) != sh.body {
				t.Fatalf("round-trip not byte-identical for %q\n--- got ---\n%s\n--- want ---\n%s", sh.name, out, sh.body)
			}
		})
	}
}

// TestHelmCodec_Classification asserts each single-document corpus shape is
// classified structured (patchable) or opaque (control-flow / interpolated /
// structure-emitting) as expected — the value-versus-control-flow surface.
func TestHelmCodec_Classification(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	for _, sh := range realHelmShapes() {
		sh := sh
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()
			units, err := codec.Segment(sh.name, []byte(sh.body))
			if err != nil {
				t.Fatalf("Segment: %v", err)
			}
			if len(units) != 1 {
				t.Fatalf("expected 1 unit, got %d", len(units))
			}
			if units[0].Structured() != sh.structured {
				t.Fatalf("%q: structured=%v, want %v (tree=%s)", sh.name, units[0].Structured(), sh.structured, string(units[0].Tree))
			}
		})
	}
}

// TestHelmCodec_ValueHolePatch_EditsOnlyTargetScalar is the patch-isolation
// gate: a PATCH that replaces one value-position scalar edits only that scalar;
// every `{{ … }}` template action in the file stays byte-identical.
func TestHelmCodec_ValueHolePatch_EditsOnlyTargetScalar(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/deployment.yaml"
	const body = "image:\n  repository: {{ .Values.image.repository }}\n  tag: {{ .Values.image.tag }}\n  pullPolicy: IfNotPresent\n"

	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	u := units[0]
	if !u.Structured() {
		t.Fatalf("expected structured unit")
	}

	// Replace the non-template scalar /image/pullPolicy → the template actions
	// for repository and tag must survive verbatim.
	ops := []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: u.Token, Op: "replace", Path: "/image/pullPolicy", Value: "Always"}}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, _, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	got := string(out)
	for _, action := range []string{"{{ .Values.image.repository }}", "{{ .Values.image.tag }}"} {
		if !strings.Contains(got, action) {
			t.Errorf("template action %q not preserved byte-identical:\n%s", action, got)
		}
	}
	if !strings.Contains(got, "pullPolicy: Always") {
		t.Errorf("targeted scalar not edited:\n%s", got)
	}
	if strings.Contains(got, "IfNotPresent") {
		t.Errorf("old scalar value still present:\n%s", got)
	}
}

// TestHelmCodec_ValueHoleReplacedByLiteral pins the second guest intent: a
// PATCH that replaces the value-hole scalar itself with a literal drops the
// template action (the guest deliberately overwrote it).
func TestHelmCodec_ValueHoleReplacedByLiteral(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/cm.yaml"
	const body = "data:\n  region: {{ .Values.region }}\n"

	units, _ := codec.Segment(name, []byte(body))
	u := units[0]
	ops := []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: u.Token, Op: "replace", Path: "/data/region", Value: "us-east-1"}}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, _, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "{{ .Values.region }}") {
		t.Errorf("expected the action overwritten by the literal; still present:\n%s", got)
	}
	if !strings.Contains(got, "region: us-east-1") {
		t.Errorf("literal not present:\n%s", got)
	}
}

// TestHelmCodec_ControlFlowIsOpaque asserts a control-flow document carves to an
// opaque unit (no tree) — the mechanical guardrail (a PATCH against it halts in
// the core) plus the operator path (patch values.yaml instead).
func TestHelmCodec_ControlFlowIsOpaque(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/cm.yaml"
	const body = "data:\n{{- range $k, $v := .Values.config }}\n  {{ $k }}: {{ $v | quote }}\n{{- end }}\n"

	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	for _, u := range units {
		if u.Structured() {
			t.Fatalf("control-flow document produced a tree (should be opaque): tree=%s", string(u.Tree))
		}
		if len(u.Bytes) == 0 {
			t.Fatalf("opaque unit must still carry the original bytes (the guest's view)")
		}
	}

	// Operator path: the same chart's values.yaml IS patchable (non-template).
	const valuesName = "values.yaml"
	const values = "config:\n  log_level: info\n"
	vunits, _ := codec.Segment(valuesName, []byte(values))
	if !vunits[0].Structured() {
		t.Fatalf("values.yaml should be a structured PATCH target")
	}
}

// TestHelmCodec_ValuesYAMLPreservesAnchorAndComment is the values.yaml O4 gate:
// a PATCH on a values.yaml preserves an anchor AND a comment elsewhere in the
// document — the stock node-span codec the helm codec delegates to.
func TestHelmCodec_ValuesYAMLPreservesAnchorAndComment(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "values.yaml"
	const body = `# chart defaults
defaults: &defaults
  cpu: "100m"
  mem: '256Mi'
resources:
  <<: *defaults
  disk: "10Gi"   # keep this comment
replicaCount: 1
`
	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	u := units[0]
	if !u.Structured() {
		t.Fatalf("values.yaml not structured")
	}
	ops := []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: u.Token, Op: "replace", Path: "/replicaCount", Value: float64(3)}}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, _, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "&defaults") || !strings.Contains(got, "<<: *defaults") {
		t.Errorf("anchor/merge-key not preserved:\n%s", got)
	}
	if !strings.Contains(got, "# chart defaults") || !strings.Contains(got, "# keep this comment") {
		t.Errorf("comment not preserved:\n%s", got)
	}
	if !strings.Contains(got, "replicaCount: 3") {
		t.Errorf("targeted scalar not edited:\n%s", got)
	}
}

// TestHelmCodec_MultidocAllValueHolesIsPerDocument asserts a multi-document
// template whose every document is clean value-holes classifies per document:
// the blanked scaffold parses as a whole, so each document is an addressable,
// structured unit.
func TestHelmCodec_MultidocAllValueHolesIsPerDocument(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/multi.yaml"
	const body = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Values.cm.name }}\n---\napiVersion: v1\nkind: Secret\nmetadata:\n  name: {{ .Values.secret.name }}\n"

	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("expected 2 units, got %d", len(units))
	}
	for i, u := range units {
		if !u.Structured() {
			t.Errorf("document %d (value hole) should be structured", i)
		}
	}

	// Patch document 0 → document 1's action survives byte-identical
	// (neighbor-shift property), and document 0's action is overwritten.
	u := units[0]
	ops := []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: u.Token, Op: "replace", Path: "/metadata/name", Value: "fixed-name"}}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, _, err := codec.Reassemble(name, []byte(body), units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "{{ .Values.secret.name }}") {
		t.Errorf("sibling document's action not preserved:\n%s", got)
	}
	if strings.Contains(got, "{{ .Values.cm.name }}") {
		t.Errorf("patched document still carries its action (literal expected):\n%s", got)
	}
	if !strings.Contains(got, "name: fixed-name") {
		t.Errorf("patch not applied:\n%s", got)
	}
}

// TestHelmCodec_MultidocWithControlFlowIsWholeFileOpaque asserts the honest
// limit: when a multi-document template file carries a control-flow region in
// ANY document, the file's document structure is indeterminate at compile time
// (the blanked scaffold does not parse as a whole), so the file carves to a
// single OPAQUE unit. A PATCH halts loudly; the operator patches values.yaml.
func TestHelmCodec_MultidocWithControlFlowIsWholeFileOpaque(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/multi.yaml"
	const body = "kind: ConfigMap\nmetadata:\n  name: {{ .Values.a }}\n---\ndata:\n{{- range $k, $v := .Values.m }}\n  {{ $k }}: {{ $v }}\n{{- end }}\n"

	units, err := codec.Segment(name, []byte(body))
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(units) != 1 {
		t.Fatalf("expected 1 whole-file unit, got %d", len(units))
	}
	if units[0].Structured() {
		t.Fatalf("a file with a control-flow region must be opaque; got a tree: %s", string(units[0].Tree))
	}
	if len(units[0].Bytes) == 0 {
		t.Fatalf("opaque unit must carry the original bytes (the guest's view)")
	}

	// Round-trip identity holds for the whole file.
	out, _, err := codec.Reassemble(name, []byte(body), units, nil)
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if string(out) != body {
		t.Fatalf("opaque whole-file round-trip not byte-identical:\n%s", out)
	}
}

// reassembleHelm runs the executor's fold-back for one structured unit:
// Segment → apply ops to the addressed unit's tree → Reassemble. It mirrors
// exactly what runNodePipeline does so the test exercises the real path.
func reassembleHelm(t *testing.T, codec frp.Codec, name string, body []byte, path string, value any) []byte {
	t.Helper()
	units, err := codec.Segment(name, body)
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	if len(units) != 1 || !units[0].Structured() {
		t.Fatalf("expected one structured unit, got %d (structured=%v)", len(units), len(units) == 1 && units[0].Structured())
	}
	u := units[0]
	ops := []frp.JSONPatchOp{{Scope: frp.DocumentScopeOwn, Key: u.Token, Op: "replace", Path: path, Value: value}}
	newTree, err := frp.StockApplier{}.Apply(u.Tree, ops)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	out, _, err := codec.Reassemble(name, body, units, []frp.PatchedUnit{{Token: u.Token, Tree: newTree, Ops: ops}})
	if err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	return out
}

// TestHelmCodec_ValuesYAMLPatchedDeterministicAcrossRuns is the end-to-end
// double-run gate: a chart-sourced node's values.yaml is patched, and the
// result is byte-identical across two independent runs of the same input — the
// determinism contract GitOps diffs rely on. An anchor and a comment elsewhere
// in the document survive in both runs.
func TestHelmCodec_ValuesYAMLPatchedDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "values.yaml"
	const body = `# chart defaults
image:
  repository: nginx
  tag: "1.25"
resources: &res
  cpu: "100m"
limits:
  <<: *res          # merged defaults
replicaCount: 1
`
	run1 := reassembleHelm(t, codec, name, []byte(body), "/replicaCount", float64(5))
	run2 := reassembleHelm(t, codec, name, []byte(body), "/replicaCount", float64(5))

	if string(run1) != string(run2) {
		t.Fatalf("two runs differ (non-deterministic):\n--- run1 ---\n%s\n--- run2 ---\n%s", run1, run2)
	}
	got := string(run1)
	if !strings.Contains(got, "replicaCount: 5") {
		t.Errorf("patch not applied:\n%s", got)
	}
	if !strings.Contains(got, "&res") || !strings.Contains(got, "<<: *res") || !strings.Contains(got, "# merged defaults") || !strings.Contains(got, "# chart defaults") {
		t.Errorf("anchor/merge-key/comment not preserved:\n%s", got)
	}
}

// TestHelmCodec_TemplateValuePatchDeterministicAcrossRuns is the second
// double-run gate on the template path: a value-hole chart template is patched
// and the result is byte-identical across two runs, with the untouched template
// actions preserved verbatim in both.
func TestHelmCodec_TemplateValuePatchDeterministicAcrossRuns(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/deployment.yaml"
	const body = "kind: Deployment\nmetadata:\n  name: {{ .Values.name }}\nspec:\n  replicas: 1\n  image: {{ .Values.image }}\n"

	run1 := reassembleHelm(t, codec, name, []byte(body), "/spec/replicas", float64(3))
	run2 := reassembleHelm(t, codec, name, []byte(body), "/spec/replicas", float64(3))
	if string(run1) != string(run2) {
		t.Fatalf("two runs differ (non-deterministic):\n--- run1 ---\n%s\n--- run2 ---\n%s", run1, run2)
	}
	got := string(run1)
	if !strings.Contains(got, "replicas: 3") {
		t.Errorf("patch not applied:\n%s", got)
	}
	for _, action := range []string{"{{ .Values.name }}", "{{ .Values.image }}"} {
		if !strings.Contains(got, action) {
			t.Errorf("template action %q not preserved:\n%s", action, got)
		}
	}
}

// TestHelmCodec_PlaceholderCollisionHalts asserts the placeholder-collision
// guard: a chart file that already contains the placeholder sentinel halts
// loudly rather than risking a corrupt restore.
func TestHelmCodec_PlaceholderCollisionHalts(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)
	const name = "templates/odd.yaml"
	// Source carries the sentinel prefix AND a real action (so the template path
	// runs and the collision guard fires).
	body := "data:\n  weird: __AGGREGA_HOLE_0__\n  real: {{ .Values.x }}\n"
	_, err := codec.Segment(name, []byte(body))
	if err == nil {
		t.Fatalf("expected a collision halt; got nil")
	}
	if !strings.Contains(err.Error(), "placeholder sentinel") {
		t.Errorf("expected a placeholder-collision error; got %v", err)
	}
}
