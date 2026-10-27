// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
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
			out, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{})
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
		if _, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{}); err != nil {
			t.Errorf("optional-key form %q must not halt, got: %v", tmpl, err)
		}
	}
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
	direct, err := EvalTemplate("probe", `{{ .metadata.no_such_key }}`, ctx, redact.Keys{})
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
			out, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{})
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
		out, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{})
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
		out, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{})
		if err != nil {
			t.Errorf("%q must not halt — an absent key under an open root is normal authoring, got: %v", tmpl, err)
			continue
		}
		if out != "" {
			t.Errorf("%q rendered %q, want the empty string; an unseeded read emits the literal <no value>", tmpl, out)
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
		{"SelfInjectContext", SelfInjectContext{}},
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
	if _, err := EvalTemplate("locals-probe", `{{ default dict .features }}`, LocalsContext{}, redact.Keys{}); err == nil {
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
	if _, err := EvalTemplate("map-probe", `{{ .anything.at.all }}`, map[string]any{}, redact.Keys{}); err != nil {
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
		Ref:      gotmpl.RootRef{Segs: []string{"staleroot", "key"}, Path: ".staleroot.key"},
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
		if err := providesSelfContainmentHalt(t, tmpl, "r"); err == nil {
			t.Errorf("provides surface must halt on %q", tmpl)
		}
		if _, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{}); err == nil {
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
		if err := providesSelfContainmentHalt(t, tmpl, "r"); err != nil {
			t.Errorf("provides surface must accept %q, got: %v", tmpl, err)
		}
		if _, err := EvalTemplate("probe", tmpl, ctx, redact.Keys{}); err != nil {
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
	out, err := EvalTemplateWithDelims("artifacts[chart]", body, ctx, "[[", "]]", redact.Keys{})
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
