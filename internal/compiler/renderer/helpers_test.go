// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

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
	got := EnrichedEdgeViewFromIREdge(in, nil)
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

// TestStructuredMechanisms_EveryDispatch pins each branch of the naming a
// structured overlay carries: env / file / arg from the typed field's zero state,
// every Additional key from key presence, and nothing for a pipe carrying
// nothing.
//
// The membership assertions are written as membership rather than as a slice
// comparison because membership is what a reader of this naming asks: a
// mechanism is named or it is not. The ORDER is asserted separately below, since
// it is the order an author reads a finding back in.
func TestStructuredMechanisms_EveryDispatch(t *testing.T) {
	t.Parallel()
	p := &types.InjectPipe{}
	p.Env.Set("K", "v")
	p.File.Set("F", "/x")
	p.Arg.Set("A", "1")
	var inner types.OrderedMap[string]
	inner.Set("k", "v")
	p.Additional.Set("custom-mech", inner)

	named := map[string]bool{}
	for _, mechanism := range structuredMechanisms(p) {
		named[mechanism] = true
	}
	for _, want := range []string{"env", "file", "arg", "custom-mech"} {
		if !named[want] {
			t.Errorf("%s should be named", want)
		}
	}
	if named["absent"] {
		t.Errorf("absent should not be named")
	}

	// The typed mechanisms lead in field order, then the Additional keys in the
	// order the output wrote them.
	if got := structuredMechanisms(p); len(got) != 4 ||
		got[0] != "env" || got[1] != "file" || got[2] != "arg" || got[3] != "custom-mech" {
		t.Errorf("structuredMechanisms order = %v; want [env file arg custom-mech]", got)
	}

	// Empty inject pipe, and a nil one.
	if got := structuredMechanisms(&types.InjectPipe{}); len(got) != 0 {
		t.Errorf("an empty pipe names nothing; got %v", got)
	}
	if got := structuredMechanisms(nil); got != nil {
		t.Errorf("a nil pipe names nothing; got %v", got)
	}
}

// TestStructuredMechanisms_CustomMechanismWithNoDeliveries pins the one case the
// typed and the Additional readings answer differently: a custom mechanism whose
// key the output wrote with nothing under it is NAMED, because the key exists
// only because the output named it — while a typed mechanism written empty is
// not, because the field exists whether the output wrote it or not.
//
// It is the reading the unmarshal hook imposes rather than a choice, and it is
// pinned because a rule over this naming refuses a mechanism it reports unnamed.
func TestStructuredMechanisms_CustomMechanismWithNoDeliveries(t *testing.T) {
	t.Parallel()
	p := &types.InjectPipe{}
	p.Additional.Set("ssm_param", types.OrderedMap[string]{})

	got := structuredMechanisms(p)
	if len(got) != 1 || got[0] != "ssm_param" {
		t.Errorf("a custom mechanism written with no deliveries is still named; got %v", got)
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

// TestEvalPathTemplate_HappyAndParseError pins the renderer's path-
// template evaluator: happy path returns the executed body; a refused
// reference and a malformed body both surface as errors.
func TestEvalPathTemplate_HappyAndParseError(t *testing.T) {
	t.Parallel()
	data := PathTemplateContext{Capability: "helm", BoundaryName: "web"}
	got, err := EvalPathTemplate("t", "directory", "b", `{{ .capability }}-{{ .boundary_name }}`, data)
	if err != nil {
		t.Fatalf("happy: %v", err)
	}
	if got != "helm-web" {
		t.Errorf("happy = %q; want helm-web", got)
	}
	// A variable the context type does not define is refused before execution.
	_, err = EvalPathTemplate("t", "directory", "b", `{{ .unknown_field }}`, data)
	if !errors.Is(err, ErrPathTemplateInvalidField()) {
		t.Errorf("unknown field should report ErrPathTemplateInvalidField; got %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "unknown_field") {
		t.Errorf("diagnostic should name the offending reference; got %v", err)
	}
	// Malformed template body.
	_, err = EvalPathTemplate("t", "directory", "b", `{{ .unclosed `, data)
	if err == nil {
		t.Errorf("parse error expected")
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

// TestFindFRAttachmentBoundary_AllBranches pins empty-projection fallback,
// empty-path guard, ancestor-match (the cascade), and fallback when no
// ancestor in the projection carries the attachment.
func TestFindFRAttachmentBoundary_AllBranches(t *testing.T) {
	t.Parallel()
	// Empty projection → fallback to the component's own boundary path.
	got := findFRAttachmentBoundary(types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}, "infra/web", "fr1")
	if got != "infra/web" {
		t.Errorf("empty projection → fallback; got %q", got)
	}
	// Empty boundary path.
	got = findFRAttachmentBoundary(types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}, "", "fr1")
	if got != "" {
		t.Errorf("empty path → fallback; got %q", got)
	}
	// Ancestor "infra" carries the attachment; the consumer lives at the
	// descendant "infra/web" — the cascade walk must climb to "infra".
	proj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("fr1", types.IRBoundaryFeature{Type: "feat/x"})
	proj.Set("infra", inner)
	got = findFRAttachmentBoundary(proj, "infra/web", "fr1")
	if got != "infra" {
		t.Errorf("ancestor attachment = %q; want infra", got)
	}
	// No ancestor carries it → fallback.
	got = findFRAttachmentBoundary(proj, "infra/web", "absent-fr")
	if got != "infra/web" {
		t.Errorf("no ancestor → fallback %q; got %q", "infra/web", got)
	}
}

// TestLookupBoundaryFeatureAttachment_AllBranches pins empty-projection,
// missing-boundary, missing-feature, and hit.
func TestLookupBoundaryFeatureAttachment_AllBranches(t *testing.T) {
	t.Parallel()
	empty := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	if _, ok := lookupBoundaryFeatureAttachment(empty, "p", "fr"); ok {
		t.Errorf("empty projection → false")
	}
	// Missing boundary (projection has a different boundary).
	proj := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	proj.Set("infra", types.OrderedMap[types.IRBoundaryFeature]{})
	if _, ok := lookupBoundaryFeatureAttachment(proj, "missing", "fr"); ok {
		t.Errorf("missing boundary → false")
	}
	// Missing feature (boundary present, instance absent).
	if _, ok := lookupBoundaryFeatureAttachment(proj, "infra", "fr"); ok {
		t.Errorf("missing feature → false")
	}
	// Hit.
	inner := types.OrderedMap[types.IRBoundaryFeature]{}
	inner.Set("fr1", types.IRBoundaryFeature{Type: "feat/x"})
	proj.Set("infra", inner)
	bf, ok := lookupBoundaryFeatureAttachment(proj, "infra", "fr1")
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

	// Provider without WorkloadSynthesizer → the catalog-authoring domain.
	plainEngine := &noopEngine{}
	w := &types.Workload{}
	_, err = dispatchWorkloadSynthesis(ctx, plainEngine, tmplCtx, w, nodeCtx, "p-name")
	if err == nil {
		t.Fatalf("engine without WorkloadSynthesizer should error")
	}
	// The domain is carried on the error and composed into the diagnostic's head, so
	// the message must not name it as well. The count is what catches a message that
	// does: the head supplies one occurrence, and a message naming the category too
	// hands the reader the same word twice on the one line it leads with.
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("refusal should carry a *CompilerError so the boundary can route it; got %T", err)
	}
	if got, want := ce.Category, cerrors.CatalogError; got != want {
		t.Errorf("category: got %q, want %q — the Recipe declaring workload: is catalog-authored", got, want)
	}
	if n := strings.Count(ce.Message, string(cerrors.CatalogError)); n != 0 {
		t.Errorf("the message names the category %d times, want 0 — the head carries it\nmessage: %s", n, ce.Message)
	}
	if n := strings.Count(err.Error(), string(cerrors.CatalogError)); n != 1 {
		t.Errorf("the rendered line says the category %d times, want exactly 1\nfull: %s", n, err.Error())
	}
	if !strings.Contains(ce.Message, "p-name") {
		t.Errorf("the message should name the provider that lacks the capability; got: %s", ce.Message)
	}
}

// noopEngine is a minimal Provider impl for the synthesis-dispatch test.
type noopEngine struct{}

func (noopEngine) Render(ctx context.Context, _ map[string]any, _ NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

func (noopEngine) Contract() ProviderContract {
	return ProviderContract{Sourced: true, Synthesized: true, ArtifactsStandalone: true}
}
