// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"text/template/parse"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func populatedTemplateContext(t *testing.T) *types.TemplateContext {
	t.Helper()
	values := types.OrderedMap[any]{}
	values.Set("port", 8080)
	infra := types.OrderedMap[any]{}
	infra.Set("replicas", 3)
	locals := types.OrderedMap[string]{}
	locals.Set("derived", "x")
	provides := types.OrderedMap[string]{}
	provides.Set("host", "db.local")
	features := types.OrderedMap[types.TemplateAttachment]{}
	features.Set("creds", types.TemplateAttachment{Type: "core/secret-value"})
	steps := types.OrderedMap[types.StepOutput]{}
	steps.Set("one", types.StepOutput{})
	labels := types.OrderedMap[string]{}
	labels.Set("tier", "web")
	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("db", types.EnrichedEdge{Target: "db"})

	return &types.TemplateContext{
		Context: types.ContextBlock{
			InstanceID: "api",
			Labels:     labels,
			Uses:       uses,
		},
		Values:         values,
		Infra:          infra,
		Locals:         locals,
		Provides:       provides,
		Features:       features,
		Metadata:       &types.TemplateMetadata{ReleaseName: "rel"},
		AttachmentName: "creds",
		Inject:         &types.InjectPipe{},
		InjectRaw:      map[string]any{"env": map[string]any{}},
		Steps:          steps,
		Source:         "oci://example/chart",
		Workload:       &types.Workload{},
		Boundary:       &types.BoundaryContext{Path: "default"},
	}
}

// A root the render context does not define halts, and it halts identically
// whether the author wrote it bare or wrapped it in a fallback. The wrapped form
// is the one that mattered: it used to resolve to an empty value and let the
// build finish at exit 0.
func TestEvalTemplate_UnknownRootHaltsBareAndShielded(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	forms := map[string]string{
		"bare":              `{{ .staleroot.key }}`,
		"bare whole-root":   `{{ .staleroot }}`,
		"default chained":   `{{ (default dict .staleroot).key }}`,
		"default whole":     `{{ default dict .staleroot }}`,
		"default piped":     `{{ .staleroot | default "x" }}`,
		"if-guarded":        `{{ if .staleroot }}{{ .staleroot.key }}{{ end }}`,
		"with-guarded":      `{{ with .staleroot }}{{ .key }}{{ end }}`,
		"range-guarded":     `{{ range .staleroot }}{{ . }}{{ end }}`,
		"nested in call":    `{{ printf "%v" (default dict .staleroot) }}`,
		"root var anchored": `{{ range .values }}{{ $.staleroot.key }}{{ end }}`,
	}
	for name, tmpl := range forms {
		t.Run(name, func(t *testing.T) {
			out, err := EvalTemplate("probe", tmpl, ctx)
			if err == nil {
				t.Fatalf("%s: expected halt, rendered %q at no error", name, out)
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("%s: expected ErrTemplateUnknownRoot, got: %v", name, err)
			}
			if !strings.Contains(err.Error(), "staleroot") {
				t.Errorf("%s: diagnostic does not name the offending root: %v", name, err)
			}
		})
	}
}

// The optional-key path is the explicit anti-requirement of the root check: an
// absent KEY under a defined root is normal authoring and must keep rendering.
// Only an absent ROOT is an error.
func TestEvalTemplate_OptionalKeyUnderValidRootStillRenders(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	forms := []string{
		`{{ (default dict .values.absentkey).nested }}`,
		`{{ default dict .values.absentkey }}`,
		`{{ default "fallback" .values.absentkey }}`,
		`{{ .values.absentkey }}`,
		`{{ (default dict .infra.absentkey).deep }}`,
		`{{ (default dict .features.nosuchattachment).provides }}`,
		`{{ default dict .features }}`,
		`{{ optional .infra "absentkey" 1 }}`,
		`{{ if .values.absentkey }}never{{ end }}`,
		`{{ with .values.absentkey }}{{ . }}{{ end }}`,
		`{{ .context.instance_id }}`,
		`static text only`,
	}
	for _, tmpl := range forms {
		if _, err := EvalTemplate("probe", tmpl, ctx); err != nil {
			t.Errorf("optional-key form %q must not halt, got: %v", tmpl, err)
		}
	}
}

// Inside a range or with body the dot is rebound to the element, so a field read
// there names a field of that element and is not a context root. Treating it as
// one would reject every iterating template in the catalog. Asserted on the walk
// itself, since what a rebound field resolves to at execute time is the
// evaluator's business and not the root check's.
func TestCollectTemplateRootRefs_DotRebinding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want []string
	}{
		{`{{ range .values }}{{ .anything }}{{ end }}`, []string{"values"}},
		{`{{ range $k, $v := .infra }}{{ $k }}{{ .whatever }}{{ end }}`, []string{"infra"}},
		{`{{ with .metadata }}{{ .release_name }}{{ end }}`, []string{"metadata"}},
		{`{{ with .values.absentkey }}{{ .deep.deeper }}{{ else }}{{ .infra.replicas }}{{ end }}`, []string{"values", "infra"}},
		{`{{ range .context.uses }}{{ .target }}{{ .provides.host }}{{ end }}`, []string{"context"}},
		{`{{ range .steps }}{{ range .outputs }}{{ .deep }}{{ end }}{{ end }}`, []string{"steps"}},
	}
	for _, tc := range cases {
		got := map[string]bool{}
		for _, ref := range collectTemplateRootRefs(mustParseForRoots(t, tc.tmpl)) {
			got[ref.Segs[0]] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: collected roots %v, want exactly %v", tc.tmpl, keysOf(got), tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%q: collected roots %v, want %v", tc.tmpl, keysOf(got), tc.want)
			}
		}
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A chained field hangs off the receiver's value, not off the dot, so it is not
// a root. This is what lets a defensive wrapper stay legal while the root it
// wraps is still checked.
func TestCollectTemplateRootRefs_ChainIsNotARoot(t *testing.T) {
	t.Parallel()
	tree := mustParseForRoots(t, `{{ (default dict .values.tls).ca }}`)
	refs := collectTemplateRootRefs(tree)
	var paths []string
	for _, r := range refs {
		paths = append(paths, r.Path)
	}
	if len(refs) != 1 || refs[0].Segs[0] != "values" {
		t.Fatalf("expected exactly the wrapped root .values.tls, got %v", paths)
	}
}

// $ names the context root under any rebinding, so a root-anchored reference
// inside a ranged body is still checked.
func TestCollectTemplateRootRefs_RootVariableInsideRebinding(t *testing.T) {
	t.Parallel()
	tree := mustParseForRoots(t, `{{ $notroot := .values }}{{ range .values }}{{ $.infra.replicas }}{{ $notroot.x }}{{ end }}`)
	got := map[string]bool{}
	for _, r := range collectTemplateRootRefs(tree) {
		got[r.Segs[0]] = true
	}
	if !got["values"] || !got["infra"] {
		t.Errorf("expected roots values and infra, got %v", keysOf(got))
	}
	if got["notroot"] || got["x"] {
		t.Errorf("a template-declared variable must not read as a context root: %v", keysOf(got))
	}
}

func refPaths(refs []templateRootRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Path)
	}
	return out
}

// Aliasing a WHOLE root — binding it to a variable, or rebinding the dot to it with
// a with — is checked on the same terms as reading a key beneath it directly. The
// diagnostic is asserted to be the direct spelling's verbatim, because a reference
// that resolves to the same path is the same reference and there is no second rule
// for it.
//
// The alias used to escape the check entirely, and the cost was not merely an
// unvalidated read: one walk feeds both the admission check and the absent-leaf
// seeding, so a reference neither of them attributed was neither refused nor
// softened, and a mistyped key under a closed pillar reached the emitted artifact as
// the literal text "<no value>".
func TestEvalTemplate_WholeRootAliasIsCheckedLikeTheDirectSpelling(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	direct, err := EvalTemplate("probe", `{{ .metadata.no_such_key }}`, ctx)
	if err == nil {
		t.Fatalf("the direct spelling must halt; it is the terms every other spelling is held to, rendered %q", direct)
	}
	wantErr := err.Error()
	aliased := map[string]string{
		"variable":               `{{ $m := .metadata }}{{ $m.no_such_key }}`,
		"with-rebound dot":       `{{ with .metadata }}{{ .no_such_key }}{{ end }}`,
		"with declaration":       `{{ with $m := .metadata }}{{ $m.no_such_key }}{{ end }}`,
		"variable of a variable": `{{ $m := .metadata }}{{ $n := $m }}{{ $n.no_such_key }}`,
		"root-anchored variable": `{{ $m := $.metadata }}{{ $m.no_such_key }}`,
		"nested with":            `{{ with .metadata }}{{ with . }}{{ .no_such_key }}{{ end }}{{ end }}`,
	}
	for name, tmpl := range aliased {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			out, err := EvalTemplate("probe", tmpl, ctx)
			if err == nil {
				t.Fatalf("expected a halt, rendered %q", out)
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("expected ErrTemplateUnknownRoot, got: %v", err)
			}
			if err.Error() != wantErr {
				t.Errorf("diagnostic differs from the direct spelling's\n alias: %s\ndirect: %s", err.Error(), wantErr)
			}
		})
	}
}

// A key the root's type DOES define keeps rendering through every alias spelling.
// This is what makes the check above a tightening of one reference rather than a
// refusal of the alias construct, and it is the assertion that would fail if the
// resolution were ever widened into refusing a bare root or a rebound dot.
func TestEvalTemplate_WholeRootAliasKeepsDeclaredKeysRendering(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	forms := []string{
		`{{ .metadata.release_name }}`,
		`{{ $m := .metadata }}{{ $m.release_name }}`,
		`{{ with .metadata }}{{ .release_name }}{{ end }}`,
		`{{ with $m := .metadata }}{{ $m.release_name }}{{ end }}`,
		`{{ $m := .metadata }}{{ $n := $m }}{{ $n.release_name }}`,
	}
	for _, tmpl := range forms {
		out, err := EvalTemplate("probe", tmpl, ctx)
		if err != nil {
			t.Errorf("%q must render, got: %v", tmpl, err)
			continue
		}
		if out != "rel" {
			t.Errorf("%q rendered %q, want %q", tmpl, out, "rel")
		}
	}
}

// An absent key under an OPEN root is normal authoring, so reading one through an
// alias must produce exactly what the direct spelling produces. It is the same walk
// that feeds the absent-leaf seeding, so an alias the walk did not attribute used to
// render the literal "<no value>" where the direct spelling rendered nothing — a
// divergence that reached the emitted artifact without any root being closed.
func TestEvalTemplate_AliasedAbsentKeyUnderOpenRootRendersEmpty(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	forms := []string{
		`{{ .values.absentkey }}`,
		`{{ $v := .values }}{{ $v.absentkey }}`,
		`{{ with .values }}{{ .absentkey }}{{ end }}`,
		`{{ $v := .values }}{{ with $v }}{{ .absentkey }}{{ end }}`,
	}
	for _, tmpl := range forms {
		out, err := EvalTemplate("probe", tmpl, ctx)
		if err != nil {
			t.Errorf("%q must not halt — an absent key under an open root is normal authoring, got: %v", tmpl, err)
			continue
		}
		if out != "" {
			t.Errorf("%q rendered %q, want the empty string; an unseeded read emits the literal <no value>", tmpl, out)
		}
	}
}

// What the walk deliberately does NOT name, pinned so the residue is not later
// mistaken for a second hole and closed by guesswork. A range body's dot is one
// element of the container, and what a function yields is decided at evaluation
// rather than by the text — so neither is a path the template names, and
// attributing either would refuse authoring on an inference the walk has no basis
// to make. The last case is the shipped edge-configuration idiom: attributing the
// inner reads there would treat an edge's own fields as context pillars.
func TestCollectTemplateRootRefs_UnnameableDotIsNotAttributed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want []string
	}{
		{`{{ range .context.uses }}{{ .target }}{{ end }}`, []string{".context.uses"}},
		{`{{ range $k, $v := .infra }}{{ $k }}{{ $v.anything }}{{ end }}`, []string{".infra"}},
		{`{{ with default dict .metadata }}{{ .no_such_key }}{{ end }}`, []string{".metadata"}},
		{`{{ $m := default dict .metadata }}{{ $m.no_such_key }}`, []string{".metadata"}},
		{`{{ with index .context.uses "main" }}{{ with .values }}{{ .protocol }}{{ end }}{{ end }}`, []string{".context.uses"}},
	}
	for _, tc := range cases {
		got := refPaths(collectTemplateRootRefs(mustParseForRoots(t, tc.tmpl)))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%q collected %v, want exactly %v", tc.tmpl, got, tc.want)
		}
	}
}

// A path bound to a variable and then iterated through it is one reference, iterated.
// Resolving the alias to the path it aliases is what lets the two occurrences share a
// single entry so the flag merges: without the merge the binding's non-iterated entry
// would have an empty string seeded at the leaf, and the range would then fail on a
// string where an absent reference would have iterated as nothing.
func TestCollectTemplateRootRefs_AliasedIterationMergesOntoOneReference(t *testing.T) {
	t.Parallel()
	refs := collectTemplateRootRefs(mustParseForRoots(t, `{{ $list := .values.items }}{{ range $list }}{{ . }}{{ end }}`))
	if got := refPaths(refs); strings.Join(got, ",") != ".values.items" {
		t.Fatalf("collected %v, want exactly [.values.items]", got)
	}
	if !refs[0].Iterated {
		t.Error("a path iterated through an alias must be marked iterated, or its leaf is seeded as a scalar")
	}
}

// A variable reassigned to something the walk cannot name UNBINDS rather than
// keeping the path it used to hold, so a later read is not attributed to a root the
// author never wrote at that point.
func TestCollectTemplateRootRefs_ReassignedVariableDoesNotKeepAStalePath(t *testing.T) {
	t.Parallel()
	refs := collectTemplateRootRefs(mustParseForRoots(t,
		`{{ $m := .metadata }}{{ range .values }}{{ $m := . }}{{ $m.no_such_key }}{{ end }}`))
	for _, r := range refs {
		if r.Path == ".metadata.no_such_key" {
			t.Errorf("a shadowed variable must not resolve through its outer binding: %v", refPaths(refs))
		}
	}
}

// The admitted root set and the keys actually delivered to a template come from
// one derivation. This pins that: whatever the context walk emits as top-level
// keys for a fully-populated context is exactly what the check admits. A future
// change to either side that breaks the correspondence fails here rather than
// silently reopening the launder-an-unknown-root hole.
func TestContextRootNames_MatchesDeliveredTemplateKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		data any
	}{
		{"TemplateContext", populatedTemplateContext(t)},
		{"ProvidesContext", ProvidesContext{AttachmentName: "creds"}},
		{"LocalsContext", LocalsContext{}},
		{"SelfInjectContext", SelfInjectContext{Metadata: &types.TemplateMetadata{}}},
		{"AssertionContext", AssertionContext{Recipe: &AssertionRecipeContext{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			declared, ok := contextRootNames(tc.data)
			if !ok {
				t.Fatalf("%s: root set must be derivable from the type", tc.name)
			}
			walked, isMap := toLowercaseMap(tc.data).(map[string]any)
			if !isMap {
				t.Fatalf("%s: context walk did not produce a keyed object", tc.name)
			}
			for key := range walked {
				if !containsString(declared, key) {
					t.Errorf("%s: template receives key %q that the admitted root set omits — a reference to it would halt though the data carries it", tc.name, key)
				}
			}
			// Every declared root must be a field the walk can emit; a declared
			// root the walk never names would be admitted but unreachable.
			for _, root := range declared {
				if _, ok := walked[root]; !ok && !mayBeOmitted(tc.data, root) {
					t.Errorf("%s: admitted root %q is not a key the context walk emits", tc.name, root)
				}
			}
		})
	}
}

// mayBeOmitted reports whether a declared root is allowed to be missing from the
// walked output because its field carries omitempty and the value is zero.
func mayBeOmitted(data any, root string) bool {
	// A fully-populated context is passed for the types that can be fully
	// populated; the narrower contexts intentionally leave optional pillars
	// zero, so absence there is expected rather than a divergence.
	switch data.(type) {
	case *types.TemplateContext:
		return false
	default:
		return true
	}
}

func containsString(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}

// A narrower context admits a narrower root set, taken from its own type. A
// pillar the wide render context carries is not reachable from a surface whose
// context does not declare it.
func TestContextRootNames_NarrowSurfacesAdmitLess(t *testing.T) {
	t.Parallel()
	locals, ok := contextRootNames(LocalsContext{})
	if !ok {
		t.Fatal("LocalsContext root set must be derivable")
	}
	sorted := append([]string(nil), locals...)
	sort.Strings(sorted)
	// No provides root: a local resolves before its node's provides do, which is
	// what lets a provides template read .locals. A locals root IS declared, and
	// declaring it is what lets an entry read an earlier sibling; what the entry
	// receives through it is a prefix of the block, which is a property of how the
	// pillar is POPULATED per entry rather than of what this type admits.
	//
	// attachment_name is declared because a FeatureRecipe's own locals populate it.
	// The base Recipe locals surface and the Recipe source surface refuse it through
	// a declared narrowing rather than by the type withholding it — which is the
	// distinction this assertion is over: the TYPE's universe, not any one surface's
	// admitted set.
	want := []string{"attachment_name", "context", "infra", "locals", "values"}
	if strings.Join(sorted, ",") != strings.Join(want, ",") {
		t.Fatalf("LocalsContext roots = %v, want %v", sorted, want)
	}
	if _, err := EvalTemplate("locals-probe", `{{ default dict .features }}`, LocalsContext{}); err == nil {
		t.Error("a pillar the locals context does not declare must halt, not render empty")
	}
}

// A context whose type is not a struct has no derivable root set, and no root
// check applies rather than every reference being refused.
func TestContextRootNames_NonStructIsNotCheckable(t *testing.T) {
	t.Parallel()
	if _, ok := contextRootNames(map[string]any{"values": 1}); ok {
		t.Error("a map context must not yield a derived root set")
	}
	if _, ok := contextRootNames(nil); ok {
		t.Error("a nil context must not yield a derived root set")
	}
	if _, err := EvalTemplate("map-probe", `{{ .anything.at.all }}`, map[string]any{}); err != nil {
		if errors.Is(err, ErrTemplateUnknownRoot()) {
			t.Errorf("an uncheckable context must not raise an unknown-root halt: %v", err)
		}
	}
}

// The diagnostic has to stand on its own: it names the offending path and the
// roots that are available, in the same shape the provides surface uses.
func TestTemplateUnknownRootErr_NamesOffenderAndAvailableRoots(t *testing.T) {
	t.Parallel()
	roots, _ := contextRootNames(populatedTemplateContext(t))
	msg := templateUnknownRootErr(cerrors.CatalogError, "workload[db-recipe]", &templateScopeViolation{
		Ref:      templateRootRef{Segs: []string{"staleroot", "key"}, Path: ".staleroot.key"},
		Admitted: roots,
		Level:    scopeLevelRoot,
	}).Error()
	for _, want := range []string{"workload[db-recipe]", ".staleroot.key", ".values", ".infra", ".features", ".context"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic omits %q: %s", want, msg)
		}
	}
}

// The evaluator and the provides surface identify a root the same way, because
// they share one walk and differ only in the allowlist they pass it. This pins
// the parity so a second, divergent notion of "root" cannot appear on one
// surface without failing here.
func TestRootIdentification_ParityAcrossSurfaces(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)

	staleForms := []string{
		`{{ .staleroot.key }}`,
		`{{ (default dict .staleroot).key }}`,
		`{{ default dict .staleroot }}`,
	}
	for _, tmpl := range staleForms {
		if err := CheckProvidesSelfContainment(tmpl, "r"); err == nil {
			t.Errorf("provides surface must halt on %q", tmpl)
		}
		if _, err := EvalTemplate("probe", tmpl, ctx); err == nil {
			t.Errorf("general evaluator must halt on %q", tmpl)
		}
	}

	// A chained fallback over a root BOTH surfaces admit must halt on neither —
	// the trailing key is not a root on either side.
	okForms := []string{
		`{{ (default dict .values.tls).ca }}`,
		`{{ (default dict .infra.absent).deep }}`,
		`{{ range .values.absentkey }}{{ .anything }}{{ end }}`,
	}
	for _, tmpl := range okForms {
		if err := CheckProvidesSelfContainment(tmpl, "r"); err != nil {
			t.Errorf("provides surface must accept %q, got: %v", tmpl, err)
		}
		if _, err := EvalTemplate("probe", tmpl, ctx); err != nil {
			t.Errorf("general evaluator must accept %q, got: %v", tmpl, err)
		}
	}
}

// An engine that renders with non-default delimiters leaves a downstream
// templating pass's own syntax as opaque text, so a downstream root is not read
// as an Aggrega root.
func TestEvalTemplateWithDelims_DownstreamSyntaxIsNotAnAggregaRoot(t *testing.T) {
	t.Parallel()
	ctx := populatedTemplateContext(t)
	body := `image: {{ .Values.image.repository }}:[[ .values.port ]]`
	out, err := EvalTemplateWithDelims("artifacts[chart]", body, ctx, "[[", "]]")
	if err != nil {
		t.Fatalf("downstream syntax must stay opaque, got: %v", err)
	}
	if !strings.Contains(out, ".Values.image.repository") {
		t.Errorf("downstream template text was consumed rather than passed through: %q", out)
	}
	if !strings.Contains(out, "8080") {
		t.Errorf("Aggrega-delimited reference did not render: %q", out)
	}
}

func mustParseForRoots(t *testing.T, text string) parse.Node {
	t.Helper()
	tree, err := gotmpl.ParseTree(text, "{{", "}}")
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return tree
}
