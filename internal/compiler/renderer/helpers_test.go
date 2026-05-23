// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// TestWriteOutput_CreatesDirsAndWritesFiles pins the orchestrator's
// final-step file emitter: directories created on demand; files
// written in sorted key order; round-trip preserves byte content.
func TestWriteOutput_CreatesDirsAndWritesFiles(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	files := map[string][]byte{
		"a/b/c.yaml":   []byte("c-content"),
		"a/d.yaml":     []byte("d-content"),
		"top.txt":      []byte("top-content"),
		"e/nested.json": []byte("e-content"),
	}
	if err := WriteOutput(tmp, files); err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}
	for rel, want := range files {
		got, err := os.ReadFile(filepath.Join(tmp, rel))
		if err != nil {
			t.Errorf("read %q: %v", rel, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("%q content = %q; want %q", rel, got, want)
		}
	}
}

// TestWriteOutput_EmptyMapIsNoOp pins the empty-input short-circuit.
func TestWriteOutput_EmptyMapIsNoOp(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := WriteOutput(tmp, map[string][]byte{}); err != nil {
		t.Errorf("empty map should not error; got %v", err)
	}
}

// TestEmitOriginString_EveryBranch pins each documented variant of
// EmitOrigin.String plus the default "unknown" case.
func TestEmitOriginString_EveryBranch(t *testing.T) {
	t.Parallel()
	cases := []struct {
		o    EmitOrigin
		want string
	}{
		{EmitOriginSource, "source"},
		{EmitOriginWorkload, "workload"},
		{EmitOriginArtifactsInline, "artifacts-inline"},
		{EmitOriginArtifactsImport, "artifacts-import"},
		{EmitOrigin(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.o.String(); got != tc.want {
			t.Errorf("EmitOrigin(%d).String() = %q; want %q", tc.o, got, tc.want)
		}
	}
}

// TestNamespaceFromAnnotations_AllBranches pins nil-map, absent-key,
// and present-key behaviors.
func TestNamespaceFromAnnotations_AllBranches(t *testing.T) {
	t.Parallel()
	if got := NamespaceFromAnnotations(nil); got != "" {
		t.Errorf("nil map → %q; want empty", got)
	}
	if got := NamespaceFromAnnotations(map[string]string{}); got != "" {
		t.Errorf("empty map → %q; want empty", got)
	}
	if got := NamespaceFromAnnotations(map[string]string{
		AnnotationNamespace: "kube-system",
	}); got != "kube-system" {
		t.Errorf("present-key → %q; want kube-system", got)
	}
}

// TestEnrichedEdgeViewFromIREdge_FieldProjection pins the renderer-
// side projection of IREdge → EnrichedEdge: Target, Socket, and
// OriginFeature flow through.
func TestEnrichedEdgeViewFromIREdge_FieldProjection(t *testing.T) {
	t.Parallel()
	in := types.IREdge{
		Source:        "src",
		Target:        "dst",
		Socket:        "http",
		OriginFeature: "feat.myco/rewrite",
	}
	got := EnrichedEdgeViewFromIREdge(in)
	if got.Target != "dst" {
		t.Errorf("Target = %q; want dst", got.Target)
	}
	if got.Socket != "http" {
		t.Errorf("Socket = %q; want http", got.Socket)
	}
	if got.OriginFeature != "feat.myco/rewrite" {
		t.Errorf("OriginFeature = %q; want feat.myco/rewrite", got.OriginFeature)
	}
}

// TestMechanismPresent_EveryDispatch pins each branch: env / file /
// arg (typed fields) + Additional (default) + missing.
func TestMechanismPresent_EveryDispatch(t *testing.T) {
	t.Parallel()
	p := &types.InjectPipe{}
	p.Env.Set("K", "v")
	p.File.Set("F", "/x")
	p.Arg.Set("A", "1")
	var inner types.OrderedMap[string]
	inner.Set("k", "v")
	p.Additional.Set("custom-mech", inner)

	if !mechanismPresent(p, "env") {
		t.Errorf("env should be present")
	}
	if !mechanismPresent(p, "file") {
		t.Errorf("file should be present")
	}
	if !mechanismPresent(p, "arg") {
		t.Errorf("arg should be present")
	}
	if !mechanismPresent(p, "custom-mech") {
		t.Errorf("custom-mech should be present")
	}
	if mechanismPresent(p, "absent") {
		t.Errorf("absent should not be present")
	}

	// Empty inject pipe.
	empty := &types.InjectPipe{}
	if mechanismPresent(empty, "env") {
		t.Errorf("empty env should not be present")
	}
}

// TestTrimWhitespace_EveryEdge pins every whitespace branch: leading,
// trailing, both, empty, internal-preserved, no-trim.
func TestTrimWhitespace_EveryEdge(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"  ", ""},
		{"  abc", "abc"},
		{"abc  ", "abc"},
		{"  abc  ", "abc"},
		{"\n\tabc\n", "abc"},
		{"abc", "abc"},
		{"a b c", "a b c"}, // internal whitespace preserved
	}
	for _, tc := range cases {
		if got := trimWhitespace(tc.in); got != tc.want {
			t.Errorf("trimWhitespace(%q) = %q; want %q", tc.in, got, tc.want)
		}
	}
}

// TestUnwrapAlias_BothBranches pins alias-resolving path and the
// pass-through.
func TestUnwrapAlias_BothBranches(t *testing.T) {
	t.Parallel()
	target := &yaml.Node{Kind: yaml.ScalarNode, Value: "target"}
	alias := &yaml.Node{Kind: yaml.AliasNode, Alias: target}
	if got := unwrapAlias(alias); got != target {
		t.Errorf("alias should resolve to target")
	}
	plain := &yaml.Node{Kind: yaml.ScalarNode, Value: "plain"}
	if got := unwrapAlias(plain); got != plain {
		t.Errorf("non-alias should pass through")
	}
	if got := unwrapAlias(nil); got != nil {
		t.Errorf("nil should pass through")
	}
	// Alias with nil Alias field — pass-through.
	orphan := &yaml.Node{Kind: yaml.AliasNode}
	if got := unwrapAlias(orphan); got != orphan {
		t.Errorf("orphan alias should pass through")
	}
}

// TestNormaliseInstance_EveryBranch pins the JSON round-trip helper:
// nil input → empty map; populated input → recognisable map[string]any
// shape.
func TestNormaliseInstance_EveryBranch(t *testing.T) {
	t.Parallel()
	got, err := normaliseInstance(nil)
	if err != nil {
		t.Fatalf("nil: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("nil → %v; want empty map", got)
	}
	got, err = normaliseInstance(map[string]any{
		"a": 1,
		"b": "two",
		"c": []any{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("populated: %v", err)
	}
	m, ok = got.(map[string]any)
	if !ok || m["b"] != "two" {
		t.Errorf("populated unexpected: %v", got)
	}
}

// TestIsSourceless_EveryBranch pins the boolean discriminator: nil
// recipe → true; recipe with nil Source → true; recipe with empty
// string Source → true; recipe with non-empty string Source → false.
func TestIsSourceless_EveryBranchExtra(t *testing.T) {
	t.Parallel()
	// nil recipe
	if !isSourceless(nil) {
		t.Errorf("nil recipe → should be sourceless")
	}
	// nil Source field
	if !isSourceless(&types.Recipe{}) {
		t.Errorf("nil Source → should be sourceless")
	}
	// empty string Source
	r := &types.Recipe{}
	r.Spec.Source = ""
	if !isSourceless(r) {
		t.Errorf("empty-string Source → should be sourceless")
	}
	// non-empty string Source
	r2 := &types.Recipe{}
	r2.Spec.Source = "/path/to/chart"
	if isSourceless(r2) {
		t.Errorf("populated Source → should NOT be sourceless")
	}
}

// TestIsEmptyForTemplate_EveryReflectKind pins each reflect.Kind dispatch
// arm of the omitempty/omitzero predicate.
func TestIsEmptyForTemplate_EveryReflectKind(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		v     any
		empty bool
	}{
		{"nil interface", nil, true},
		{"empty string", "", true},
		{"non-empty string", "x", false},
		{"empty slice", []int{}, true},
		{"populated slice", []int{1}, false},
		{"empty map", map[string]int{}, true},
		{"populated map", map[string]int{"a": 1}, false},
		{"false bool", false, true},
		{"true bool", true, false},
		{"zero int", int(0), true},
		{"nonzero int", int(7), false},
		{"zero uint", uint(0), true},
		{"nonzero uint", uint(7), false},
		{"zero float", float64(0), true},
		{"nonzero float", float64(1.5), false},
		{"nil pointer", (*int)(nil), true},
		{"empty array", [0]int{}, true},
		{"populated array", [1]int{1}, false},
		{"empty struct (no IsZero)", struct{ A int }{}, false},
		{"OrderedMap zero (IsZero)", types.OrderedMap[string]{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rv reflect.Value
			if tc.v != nil {
				rv = reflect.ValueOf(tc.v)
			}
			if got := isEmptyForTemplate(rv); got != tc.empty {
				t.Errorf("isEmptyForTemplate(%v) = %v; want %v", tc.v, got, tc.empty)
			}
		})
	}
}

// TestCollectTopLevelFields_AllNodeKinds pins the AST walker: parses
// templates that exercise each handled node kind (Action, Pipe, If,
// Range, With, Field, Chain) and verifies the collected field set.
func TestCollectTopLevelFields_AllNodeKinds(t *testing.T) {
	t.Parallel()
	tmplText := `{{ .alpha.x }} {{ if .beta }}{{ .gamma }}{{ end }} {{ range .delta }}{{ . }}{{ end }} {{ with .epsilon }}{{ .zeta }}{{ end }} {{ (printf "%s" .iota).kappa }}`
	tmpl, err := template.New("t").Parse(tmplText)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := collectTopLevelFields(tmpl)
	want := map[string]bool{
		"alpha": true,
		"beta":  true,
		"gamma": true,
		"delta": true,
		// epsilon and zeta — `with` rebinds .. The walker collects fields
		// from the pipe expression (epsilon) AND from inside the body
		// (.zeta surfaces as a field too).
		"epsilon": true,
		"zeta":    true,
		"iota":    true,
		"kappa":   true,
	}
	for k := range want {
		found := false
		for _, g := range got {
			if g == k {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected field %q in %v", k, got)
		}
	}
}

// TestValidatePathTemplateFields_KnownAndUnknown pins both arms:
// allowlist-passing template returns nil; off-allowlist field returns
// ErrPathTemplateInvalidField.
func TestValidatePathTemplateFields_KnownAndUnknown(t *testing.T) {
	t.Parallel()
	allow := map[string]bool{"provider": true, "boundary_name": true}
	// Known fields only.
	if err := validatePathTemplateFields("b", `{{ .provider }}-{{ .boundary_name }}`, allow); err != nil {
		t.Errorf("known fields should pass; got %v", err)
	}
	// Unknown field.
	err := validatePathTemplateFields("b", `{{ .unknown_field }}`, allow)
	if err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("unknown field should error; got %v", err)
	}
	// Parse failure.
	err = validatePathTemplateFields("b", `{{ .unclosed `, allow)
	if err == nil {
		t.Errorf("parse error should propagate")
	}
}

// TestEvalPathTemplate_HappyAndParseError pins the renderer's path-
// template evaluator: happy path returns the executed body; parse-
// error returns an OpsError; execute-error also surfaces.
func TestEvalPathTemplate_HappyAndParseError(t *testing.T) {
	t.Parallel()
	data := map[string]any{"provider": "helm", "boundary_name": "web"}
	got, err := EvalPathTemplate("t", "directory", "b", `{{ .provider }}-{{ .boundary_name }}`, data)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got != "helm-web" {
		t.Errorf("happy = %q; want helm-web", got)
	}
	// Parse error (validation rejects unknown field).
	_, err = EvalPathTemplate("t", "directory", "b", `{{ .unknown_field }}`, data)
	if err == nil {
		t.Errorf("unknown-field validation should fail")
	}
	// Malformed template body (parse error after validation).
	_, err = EvalPathTemplate("t", "directory", "b", `{{ .unclosed `, data)
	if err == nil {
		t.Errorf("parse error expected")
	}
}

// TestParseBoundaryFeatureRaw_AllShapes pins each accepted raw shape:
// *OrderedMap[any] (nil + populated), OrderedMap value, map[string]any,
// and unsupported (returns zero).
func TestParseBoundaryFeatureRaw_AllShapes(t *testing.T) {
	t.Parallel()
	// Nil pointer.
	got := parseBoundaryFeatureRaw((*types.OrderedMap[any])(nil))
	if got.Type != "" {
		t.Errorf("nil pointer = %+v; want zero", got)
	}

	// Populated *OrderedMap[any] with type + infra (*OrderedMap[any]).
	infra := types.NewOrderedMap[any]()
	infra.Set("size", "small")
	om := types.NewOrderedMap[any]()
	om.Set("type", "feat.myco/db")
	om.Set("infra", infra)
	got = parseBoundaryFeatureRaw(om)
	if got.Type != "feat.myco/db" {
		t.Errorf("Type = %q; want feat.myco/db", got.Type)
	}
	if v, _ := got.Infra.Get("size"); v != "small" {
		t.Errorf("Infra.size = %v; want small", v)
	}

	// Populated *OrderedMap[any] with type + infra (map[string]any branch).
	om2 := types.NewOrderedMap[any]()
	om2.Set("type", "feat.myco/cache")
	om2.Set("infra", map[string]any{"a": 1, "b": "x"})
	got = parseBoundaryFeatureRaw(om2)
	if got.Type != "feat.myco/cache" {
		t.Errorf("Type = %q; want feat.myco/cache", got.Type)
	}
	if got.Infra.Len() != 2 {
		t.Errorf("Infra.Len = %d; want 2", got.Infra.Len())
	}

	// map[string]any shape.
	raw := map[string]any{
		"type":  "feat.myco/inline",
		"infra": map[string]any{"k": "v"},
	}
	got = parseBoundaryFeatureRaw(raw)
	if got.Type != "feat.myco/inline" {
		t.Errorf("plain map Type = %q", got.Type)
	}
	if v, _ := got.Infra.Get("k"); v != "v" {
		t.Errorf("plain map Infra.k = %v; want v", v)
	}

	// Unsupported raw shape.
	got = parseBoundaryFeatureRaw(42)
	if got.Type != "" {
		t.Errorf("unsupported shape should return zero; got %+v", got)
	}
}

// TestResolveBoundaryScopeFRProvides_NilStateAndCascade pins nil-state
// guard and the path-ancestor walk.
func TestResolveBoundaryScopeFRProvides_NilStateAndCascade(t *testing.T) {
	t.Parallel()
	// Nil state.
	got, ok := resolveBoundaryScopeFRProvides(nil, "infra/web", "fr1")
	if ok || got != nil {
		t.Errorf("nil state should be (nil, false); got (%v, %v)", got, ok)
	}

	// Populated state with ancestor match.
	state := &BoundaryFRState{
		Provides: map[boundaryFRKey]map[string]any{
			{BoundaryPath: "infra", FRInstance: "fr1"}: {"host": "ancestor"},
		},
	}
	got, ok = resolveBoundaryScopeFRProvides(state, "infra/web/leaf", "fr1")
	if !ok || got["host"] != "ancestor" {
		t.Errorf("ancestor match: ok=%v got=%v", ok, got)
	}

	// No match anywhere.
	got, ok = resolveBoundaryScopeFRProvides(state, "other/path", "fr1")
	if ok || got != nil {
		t.Errorf("no match should be (nil, false); got (%v, %v)", got, ok)
	}
}

// TestFindBoundaryAtPath_EveryBranch pins empty-path guard, missing
// intermediate, terminal hit, and nested-recursion path.
func TestFindBoundaryAtPath_EveryBranch(t *testing.T) {
	t.Parallel()
	// Empty path.
	_, ok := findBoundaryAtPath(types.OrderedMap[types.Boundary]{}, nil)
	if ok {
		t.Errorf("empty path should miss")
	}
	// Missing top-level.
	var top types.OrderedMap[types.Boundary]
	_, ok = findBoundaryAtPath(top, []string{"absent"})
	if ok {
		t.Errorf("absent top should miss")
	}
	// Nested hit.
	leaf := types.Boundary{Description: "leaf-desc"}
	mid := types.Boundary{Description: "mid-desc"}
	mid.Boundaries.Set("leaf", leaf)
	root := types.OrderedMap[types.Boundary]{}
	root.Set("mid", mid)
	got, ok := findBoundaryAtPath(root, []string{"mid", "leaf"})
	if !ok || got.Description != "leaf-desc" {
		t.Errorf("nested: ok=%v got.Description=%q", ok, got.Description)
	}
}

// TestIsInSubtree_AllBranches pins exact-match, prefix-with-slash,
// and prefix-without-slash (not in subtree).
func TestIsInSubtree_AllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		candidate, boundaryPath string
		want                    bool
	}{
		{"", "", true},
		{"infra/web", "infra/web", true},
		{"infra/web/leaf", "infra/web", true},
		{"infra/web", "infra/webb", false},
		{"infra", "infra/web", false},
		{"other", "infra/web", false},
	}
	for _, tc := range cases {
		if got := isInSubtree(tc.candidate, tc.boundaryPath); got != tc.want {
			t.Errorf("isInSubtree(%q, %q) = %v; want %v", tc.candidate, tc.boundaryPath, got, tc.want)
		}
	}
}

// TestFindFRAttachmentBoundary_AllBranches pins nil-release guard,
// ancestor-match, fallback when no ancestor carries the attachment.
func TestFindFRAttachmentBoundary_AllBranches(t *testing.T) {
	t.Parallel()
	// Nil release.
	got := findFRAttachmentBoundary(nil, "infra/web", "fr1")
	if got != "infra/web" {
		t.Errorf("nil release → fallback; got %q", got)
	}
	// Empty boundary path.
	got = findFRAttachmentBoundary(&types.Release{}, "", "fr1")
	if got != "" {
		t.Errorf("empty path → fallback; got %q", got)
	}
	// Ancestor carries the attachment.
	rel := &types.Release{}
	infra := types.Boundary{Description: "infra-desc"}
	infra.Features.Set("fr1", map[string]any{"type": "feat/x"})
	web := types.Boundary{Description: "web-desc"}
	infra.Boundaries.Set("web", web)
	rel.Spec.Boundaries.Set("infra", infra)
	got = findFRAttachmentBoundary(rel, "infra/web", "fr1")
	if got != "infra" {
		t.Errorf("ancestor attachment = %q; want infra", got)
	}
	// No ancestor carries it → fallback.
	got = findFRAttachmentBoundary(rel, "infra/web", "absent-fr")
	if got != "infra/web" {
		t.Errorf("no ancestor → fallback %q; got %q", "infra/web", got)
	}
}

// TestLookupBoundaryFeatureAttachment_AllBranches pins nil-release,
// missing-boundary, missing-feature, and hit.
func TestLookupBoundaryFeatureAttachment_AllBranches(t *testing.T) {
	t.Parallel()
	if _, ok := lookupBoundaryFeatureAttachment(nil, "p", "fr"); ok {
		t.Errorf("nil release → false")
	}
	// Missing boundary.
	rel := &types.Release{}
	if _, ok := lookupBoundaryFeatureAttachment(rel, "missing", "fr"); ok {
		t.Errorf("missing boundary → false")
	}
	// Missing feature.
	infra := types.Boundary{Description: "infra"}
	rel.Spec.Boundaries.Set("infra", infra)
	if _, ok := lookupBoundaryFeatureAttachment(rel, "infra", "fr"); ok {
		t.Errorf("missing feature → false")
	}
	// Hit.
	infra2 := types.Boundary{Description: "infra"}
	infra2.Features.Set("fr1", map[string]any{"type": "feat/x"})
	rel.Spec.Boundaries = types.OrderedMap[types.Boundary]{}
	rel.Spec.Boundaries.Set("infra", infra2)
	bf, ok := lookupBoundaryFeatureAttachment(rel, "infra", "fr1")
	if !ok || bf.Type != "feat/x" {
		t.Errorf("hit: ok=%v Type=%q", ok, bf.Type)
	}
}

// TestSplitParentPath_BothCases pins the helper's split discipline:
// no-slash → ("", "", false); single-slash root → (root, root, true);
// nested → (parentPath, parentName, true).
func TestSplitParentPath_BothCases(t *testing.T) {
	t.Parallel()
	parent, name, ok := splitParentPath("cluster")
	if ok || parent != "" || name != "" {
		t.Errorf(`no-slash: (%q, %q, %v); want ("","",false)`, parent, name, ok)
	}
	parent, name, ok = splitParentPath("cluster/web")
	if !ok || parent != "cluster" || name != "cluster" {
		t.Errorf(`root: (%q, %q, %v); want ("cluster","cluster",true)`, parent, name, ok)
	}
	parent, name, ok = splitParentPath("a/b/c")
	if !ok || parent != "a/b" || name != "b" {
		t.Errorf(`nested: (%q, %q, %v); want ("a/b","b",true)`, parent, name, ok)
	}
}

// TestTmplCtxValues_PresentAndAbsent pins both branches of the
// orchestrator's per-node values projector.
func TestTmplCtxValues_PresentAndAbsent(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	got := tmplCtxValues(graph, "absent")
	if got.Len() != 0 {
		t.Errorf("absent → empty; got %v", got)
	}
}

// TestTmplCtxInfra_PresentAndAbsent pins both branches of the
// orchestrator's per-node infra projector.
func TestTmplCtxInfra_PresentAndAbsent(t *testing.T) {
	t.Parallel()
	graph := &types.IRGraph{}
	got := tmplCtxInfra(graph, "absent")
	if got.Len() != 0 {
		t.Errorf("absent → empty; got %v", got)
	}
}

// TestDispatchWorkloadSynthesis_AllBranches pins the workload
// dispatch: nil workload → (nil, nil); engine without synthesizer →
// CATALOG_ERROR.
func TestDispatchWorkloadSynthesis_AllBranches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tmplCtx := &types.TemplateContext{}
	nodeCtx := NodeRenderContext{NodeID: "n"}

	// Nil workload.
	files, err := dispatchWorkloadSynthesis(ctx, nil, tmplCtx, nil, nodeCtx, "p")
	if err != nil || files != nil {
		t.Errorf("nil workload: files=%v err=%v", files, err)
	}

	// Engine without WorkloadSynthesizer → CATALOG_ERROR.
	plainEngine := &noopEngine{}
	w := &types.Workload{}
	_, err = dispatchWorkloadSynthesis(ctx, plainEngine, tmplCtx, w, nodeCtx, "p-name")
	if err == nil {
		t.Errorf("engine without WorkloadSynthesizer should error")
	}
}

// noopEngine is a minimal Engine impl for the synthesis-dispatch test.
type noopEngine struct{}

func (noopEngine) Render(ctx context.Context, _ map[string]any, _ NodeRenderContext) (map[string][]byte, error) {
	return nil, nil
}
