// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the host's half of leaving rendered documents out:
// the one evaluation of an omits list, the one check of the counts a provider
// returns for it, and the template-field enumeration carrying its fields to every
// rule invoked over that enumeration. The rules refusing a provider that does not
// claim to honour the list are proven beside their declarations, over the engines
// declared below. Every expectation is read from a condition's own accessor or from
// the fixture each row writes, never from the code under test.

// omitsContext is the context an omits list is evaluated against in these proofs:
// a node carrying the name its target gives the node's resources, a values pillar
// with one plain value and one an author declared sensitive.
func omitsContext() *types.TemplateContext {
	ctx := &types.TemplateContext{}
	ctx.Context.InstanceID = "app"
	ctx.Context.RenderedName = "app-release"
	ctx.Values.Set("ns", "apps")
	ctx.Values.Set("enabled", false)
	ctx.Values.Set("password", "hunter2")
	return ctx
}

// omitsWithheld is the set a failure over omitsContext is withheld by: the
// password is declared sensitive.
func omitsWithheld() redact.Keys {
	return redact.Keys{Values: map[string]bool{"password": true}}
}

// TestEvaluateOmits_ResolvesEachEntryOnTheValuesMappingTerms is the evaluation
// contract, one row per behaviour the item states: when first and judged by the
// shared truthiness rule, a falsy when dropping the entry without its other fields
// being evaluated, the fields rendering against the context (the rendered name
// among it), an empty namespace or subchart left absent, and positions counted over
// the list the author wrote.
func TestEvaluateOmits_ResolvesEachEntryOnTheValuesMappingTerms(t *testing.T) {
	t.Parallel()

	// failIfRendered is a field that halts when it is evaluated, so a row whose when
	// drops the entry proves the field was never reached.
	const failIfRendered = `{{ fail "this field must not be evaluated" }}`

	for _, tc := range []struct {
		name    string
		entries []types.OmitEntry
		want    []ResolvedOmit
	}{
		{
			name:    "no entries resolve to none",
			entries: nil,
			want:    nil,
		},
		{
			name:    "kind, name and namespace render against the context the values mapping reads",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "{{ .context.rendered_name }}-token", Namespace: "{{ .values.ns }}"}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "Secret", Name: "app-release-token", Namespace: "apps"}, Position: 0}},
		},
		{
			name:    "an absent when keeps the entry",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token"}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "Secret", Name: "token"}, Position: 0}},
		},
		{
			name:    "a truthy when keeps the entry",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token", When: "yes"}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "Secret", Name: "token"}, Position: 0}},
		},
		{
			name:    "a when rendering false drops the entry and evaluates none of its fields",
			entries: []types.OmitEntry{{Kind: failIfRendered, Name: failIfRendered, Namespace: failIfRendered, When: "{{ .values.enabled }}"}},
			want:    nil,
		},
		{
			name:    "a when rendering zero drops the entry",
			entries: []types.OmitEntry{{Kind: failIfRendered, Name: failIfRendered, When: "0"}},
			want:    nil,
		},
		{
			name:    "a when rendering the empty string drops the entry",
			entries: []types.OmitEntry{{Kind: failIfRendered, Name: failIfRendered, When: `{{ "" }}`}},
			want:    nil,
		},
		{
			name:    "a namespace rendering to the empty string is left absent",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token", Namespace: `{{ "" }}`}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "Secret", Name: "token"}, Position: 0}},
		},
		{
			name:    "a subchart renders against the context the values mapping reads",
			entries: []types.OmitEntry{{Kind: "CustomResourceDefinition", Name: "widgets.example.com", Subchart: "{{ .values.ns }}/webhooks"}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "CustomResourceDefinition", Name: "widgets.example.com", Subchart: "apps/webhooks"}, Position: 0}},
		},
		{
			name:    "a subchart rendering to the empty string is left absent",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token", Subchart: `{{ "" }}`}},
			want:    []ResolvedOmit{{Entry: OmitEntry{Kind: "Secret", Name: "token"}, Position: 0}},
		},
		{
			name:    "a when rendering false evaluates no subchart either",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token", Subchart: failIfRendered, When: "false"}},
			want:    nil,
		},
		{
			name: "positions count the entries a when dropped",
			entries: []types.OmitEntry{
				{Kind: failIfRendered, Name: failIfRendered, When: "false"},
				{Kind: "ConfigMap", Name: "settings"},
				{Kind: failIfRendered, Name: failIfRendered, When: "False"},
				{Kind: "Secret", Name: "token"},
			},
			want: []ResolvedOmit{
				{Entry: OmitEntry{Kind: "ConfigMap", Name: "settings"}, Position: 1},
				{Entry: OmitEntry{Kind: "Secret", Name: "token"}, Position: 3},
			},
		},
		{
			name:    "a field whose template reads a value declared sensitive is marked withheld",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "{{ .values.password }}", Namespace: "{{ .values.ns }}"}},
			want: []ResolvedOmit{{
				Entry:    OmitEntry{Kind: "Secret", Name: "hunter2", Namespace: "apps"},
				Position: 0,
				Withheld: OmitFieldsWithheld{Name: true},
			}},
		},
		{
			name:    "a subchart whose template reads a value declared sensitive is marked withheld",
			entries: []types.OmitEntry{{Kind: "Secret", Name: "token", Subchart: "{{ .values.password }}"}},
			want: []ResolvedOmit{{
				Entry:    OmitEntry{Kind: "Secret", Name: "token", Subchart: "hunter2"},
				Position: 0,
				Withheld: OmitFieldsWithheld{Subchart: true},
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvaluateOmits(tc.entries, omitsContext(), omitsWithheld())
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestEvaluateOmits_AWhenThatFailsHaltsAsAFailedTemplateDoes pins the failure half
// of the gate: a when that does not render halts naming the field it was written
// in, exactly as a values mapping that does not render halts, and so does a kind,
// name or namespace of a kept entry.
func TestEvaluateOmits_AWhenThatFailsHaltsAsAFailedTemplateDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		entry types.OmitEntry
		field string
	}{
		{name: "a when", entry: types.OmitEntry{Kind: "Secret", Name: "token", When: `{{ fail "gate broke" }}`}, field: "omits[0].when"},
		{name: "a kind", entry: types.OmitEntry{Kind: `{{ fail "gate broke" }}`, Name: "token"}, field: "omits[0].kind"},
		{name: "a name", entry: types.OmitEntry{Kind: "Secret", Name: `{{ fail "gate broke" }}`}, field: "omits[0].name"},
		{name: "a namespace", entry: types.OmitEntry{Kind: "Secret", Name: "token", Namespace: `{{ fail "gate broke" }}`}, field: "omits[0].namespace"},
		{name: "a subchart", entry: types.OmitEntry{Kind: "Secret", Name: "token", Subchart: `{{ fail "gate broke" }}`}, field: "omits[0].subchart"},
		{name: "a reference to a root the context does not define", entry: types.OmitEntry{Kind: "Secret", Name: "{{ .nosuch.value }}"}, field: "omits[0].name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvaluateOmits([]types.OmitEntry{tc.entry}, omitsContext(), omitsWithheld())
			if err == nil {
				t.Fatalf("a broken template resolved to %+v", got)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("the failure does not name the field %q it was written in: %v", tc.field, err)
			}
		})
	}
	// A failing template reading a declared-sensitive value is reported without it,
	// on the terms every other template failure is.
	_, err := EvaluateOmits([]types.OmitEntry{{Kind: "Secret", Name: `{{ fail (printf "bad %s" .values.password) }}`}},
		omitsContext(), omitsWithheld())
	if err == nil {
		t.Fatal("the failing template resolved")
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the failure quotes a value declared sensitive: %v", err)
	}
}

// TestRequireOmitCounts_ReadsOneCountPerEntryAndHaltsOnTheFirstZero is the count
// check, one row per shape a report can take: lined up and every entry matching,
// not lined up in each direction, a negative count, and a zero — the first in the
// order the author wrote the entries being the one named.
func TestRequireOmitCounts_ReadsOneCountPerEntryAndHaltsOnTheFirstZero(t *testing.T) {
	t.Parallel()

	two := []ResolvedOmit{
		{Entry: OmitEntry{Kind: "ConfigMap", Name: "settings"}, Position: 1},
		{Entry: OmitEntry{Kind: "Secret", Name: "token", Namespace: "apps", Subchart: "auth"}, Position: 3},
	}

	for _, tc := range []struct {
		name   string
		sent   []ResolvedOmit
		counts []int
		want   error
		says   []string
	}{
		{name: "no entries and no counts", sent: nil, counts: nil},
		{name: "every entry matched", sent: two, counts: []int{1, 2}},
		{name: "no counts for entries handed over", sent: two, counts: nil,
			want: ErrOmitReportMismatch(), says: []string{"handed 2 omits entries", "returned 0 omitted counts"}},
		{name: "fewer counts than entries", sent: two, counts: []int{1},
			want: ErrOmitReportMismatch(), says: []string{"handed 2 omits entries", "returned 1 omitted counts"}},
		{name: "more counts than entries", sent: two, counts: []int{1, 1, 1},
			want: ErrOmitReportMismatch(), says: []string{"handed 2 omits entries", "returned 3 omitted counts"}},
		{name: "counts for a render handed none", sent: nil, counts: []int{0},
			want: ErrOmitReportMismatch(), says: []string{"handed 0 omits entries", "returned 1 omitted counts"}},
		{name: "a count below zero", sent: two, counts: []int{1, -1},
			want: ErrOmitReportMismatch(), says: []string{"a count of -1 for the entry at omits[3]"}},
		{name: "an entry matching nothing", sent: two, counts: []int{1, 0},
			want: ErrOmitMatchedNothing(),
			says: []string{`node "app"`, `provider "provider/helm:1.0.0"`, `"web-chart" omits[3]`, `kind "Secret"`, `name "token"`, `namespace "apps"`, `subchart "auth"`}},
		{name: "the first entry matching nothing is the one named", sent: two, counts: []int{0, 0},
			want: ErrOmitMatchedNothing(), says: []string{`"web-chart" omits[1]`, `name "settings"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := RequireOmitCounts(tc.sent, tc.counts, "web-chart", "app", "provider/helm:1.0.0")
			if tc.want == nil {
				if err != nil {
					t.Fatalf("a report that lines up was refused: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("the refusal does not carry %v: %v", tc.want, err)
			}
			// The composition rule binds the diagnostic's message, which the
			// rendered head (category and node) precedes; so the message is read,
			// never err.Error(), which carries that head.
			sentence, _ := cerrors.MessageOf(tc.want)
			var compiled *cerrors.CompilerError
			if !errors.As(err, &compiled) {
				t.Fatalf("the refusal is not a compiler diagnostic: %T %v", err, err)
			}
			if !strings.HasPrefix(compiled.Message(), sentence) {
				t.Errorf("the refusal does not open with the condition's registered sentence %q: %q", sentence, compiled.Message())
			}
			if n := strings.Count(compiled.Message(), sentence); n != 1 {
				t.Errorf("the refusal states the registered sentence %d times, want exactly 1: %q", n, compiled.Message())
			}
			for _, want := range tc.says {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if errors.Is(tc.want, ErrOmitMatchedNothing()) && errors.Is(err, ErrOmitReportMismatch()) {
				t.Errorf("one report carries both conditions: %v", err)
			}
		})
	}
}

// TestRequireOmitCounts_WithholdsAFieldItsTemplateProducedFromADeclaredValue pins
// that the zero-count halt never quotes a field whose template read a value an
// author declared sensitive: the field is shown as the placeholder, and the value
// it resolved to appears nowhere in the report.
func TestRequireOmitCounts_WithholdsAFieldItsTemplateProducedFromADeclaredValue(t *testing.T) {
	t.Parallel()
	sent := []ResolvedOmit{{
		Entry:    OmitEntry{Kind: "Secret", Name: "hunter2", Namespace: "apps"},
		Position: 0,
		Withheld: OmitFieldsWithheld{Name: true},
	}}
	err := RequireOmitCounts(sent, []int{0}, "web-chart", "app", "provider/helm:1.0.0")
	if !errors.Is(err, ErrOmitMatchedNothing()) {
		t.Fatalf("the refusal does not carry %v: %v", ErrOmitMatchedNothing(), err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal quotes a field produced from a value declared sensitive: %v", err)
	}
	if !strings.Contains(err.Error(), "name "+redact.Placeholder()) {
		t.Errorf("the withheld field is not shown as the placeholder: %v", err)
	}
	if !strings.Contains(err.Error(), `kind "Secret"`) || !strings.Contains(err.Error(), `namespace "apps"`) {
		t.Errorf("the fields no declared value produced are not quoted: %v", err)
	}
}

// omitsClaimEngine is a provider engine whose claim to honour omits is fixed by
// the test, and which counts how often the claim is read through its own report.
type omitsClaimEngine struct {
	claims bool
	reads  *int
}

func (e omitsClaimEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

func (e omitsClaimEngine) Contract() ProviderContract { return ProviderContract{Sourced: true} }

func (e omitsClaimEngine) HonoursOmits() bool {
	*e.reads++
	return e.claims
}

// claimlessEngine is a provider engine that makes no claim about omits at all.
type claimlessEngine struct{}

func (claimlessEngine) Render(context.Context, map[string]any, NodeRenderContext) ([]RenderedFile, error) {
	return nil, nil
}

func (claimlessEngine) Contract() ProviderContract { return ProviderContract{Sourced: true} }

// TestTheTemplateFieldEnumerationsCarryEachOmitsField holds both template-field
// enumerations to the omits fields: each field of each entry, its gate first, under
// the label a finding names it by and the authored keys that locate it, a field the
// author left empty absent — on a Recipe, on an attachment's FeatureRecipe, and on a
// boundary-scope FeatureRecipe's own enumeration.
func TestTheTemplateFieldEnumerationsCarryEachOmitsField(t *testing.T) {
	t.Parallel()
	entries := []types.OmitEntry{
		{Kind: "Secret", Name: "{{ .context.rendered_name }}-token", When: "{{ .values.on }}"},
		{Kind: "ConfigMap", Name: "settings", Namespace: "apps", Subchart: "{{ .values.sub }}"},
	}
	want := []RecipeTemplateField{
		{Label: "omits[0].when", Text: "{{ .values.on }}", AuthoredKeys: []string{"omits", "0", "when"}},
		{Label: "omits[0].kind", Text: "Secret", AuthoredKeys: []string{"omits", "0", "kind"}},
		{Label: "omits[0].name", Text: "{{ .context.rendered_name }}-token", AuthoredKeys: []string{"omits", "0", "name"}},
		{Label: "omits[1].kind", Text: "ConfigMap", AuthoredKeys: []string{"omits", "1", "kind"}},
		{Label: "omits[1].name", Text: "settings", AuthoredKeys: []string{"omits", "1", "name"}},
		{Label: "omits[1].namespace", Text: "apps", AuthoredKeys: []string{"omits", "1", "namespace"}},
		{Label: "omits[1].subchart", Text: "{{ .values.sub }}", AuthoredKeys: []string{"omits", "1", "subchart"}},
	}
	same := func(got []RecipeTemplateField) bool {
		return slices.EqualFunc(got, want, func(a, b RecipeTemplateField) bool {
			return a.Label == b.Label && a.Text == b.Text && slices.Equal(a.AuthoredKeys, b.AuthoredKeys)
		})
	}

	recipe := &types.Recipe{}
	recipe.Spec.Omits = entries
	if got := RecipeTemplateFields(recipe); !same(got) {
		t.Errorf("a Recipe's enumeration:\ngot  %+v\nwant %+v", got, want)
	}
	fr := &types.FeatureRecipe{}
	fr.Spec.Omits = entries
	if got := AttachmentTemplateFields(fr); !same(got) {
		t.Errorf("an attachment's enumeration:\ngot  %+v\nwant %+v", got, want)
	}
	var boundary []RecipeTemplateField
	for _, f := range BoundaryScopeFRTemplateFields(fr) {
		boundary = append(boundary, RecipeTemplateField{Label: f.Label, Text: f.Text, AuthoredKeys: f.AuthoredKeys})
	}
	if !same(boundary) {
		t.Errorf("a boundary-scope FeatureRecipe's enumeration:\ngot  %+v\nwant %+v", boundary, want)
	}
	// The labels are the names the evaluation runs each field's template under, so
	// a failure evaluating one and a finding about it name it alike.
	for _, f := range want {
		if RecipeTemplateFieldReadsWithArtifactDelimiters(f.Label) || RecipeTemplateFieldIsPipelineStep(f.Label) {
			t.Errorf("%q is read as another family's field", f.Label)
		}
	}
}

// TestEveryRuleOverTheTemplateFieldsReachesAnOmitsEntry is the coverage proof the
// enumeration buys: one planted violation per rule invoked over the template-field
// enumerations, each written in an omits entry and driven through that rule's
// production invocation site, asserted against the rule's own condition and
// against the field the finding names.
func TestEveryRuleOverTheTemplateFieldsReachesAnOmitsEntry(t *testing.T) {
	t.Parallel()

	t.Run("a Recipe's omits entry reading a name its provider does not report", func(t *testing.T) {
		t.Parallel()
		recipe := renderedNameFieldRecipe("terraform", "valuesMapping", "a: 1")
		recipe.Spec.ValuesMapping.Inline = ""
		recipe.Spec.Omits = []types.OmitEntry{{Kind: "Secret", Name: "{{ .context.rendered_name }}-token"}}
		halt := declaredRenderedNameHalt(t, recipe, NodeRenderedNames{})
		if !errors.Is(halt, ErrRenderedNameUnavailable()) {
			t.Fatalf("the halt does not carry %v: %v", ErrRenderedNameUnavailable(), halt)
		}
		if !strings.Contains(halt.Error(), "omits[0].name") {
			t.Errorf("the halt does not name the omits field: %v", halt)
		}
	})

	t.Run("an attachment's omits entry reading a name its host's provider does not report", func(t *testing.T) {
		t.Parallel()
		fr := attachmentRenderedNameFR("helm", "valuesMapping", "a: 1")
		fr.Spec.ValuesMapping.Inline = ""
		fr.Spec.Omits = []types.OmitEntry{{Kind: "Secret", Name: "{{ .context.rendered_name }}-token"}}
		halt := declaredAttachmentRenderedNameHalt(t, fr, NodeRenderedNames{})
		if !errors.Is(halt, ErrTemplateUnknownRoot()) {
			t.Fatalf("the refusal does not carry %v: %v", ErrTemplateUnknownRoot(), halt)
		}
		if !strings.Contains(halt.Error(), "omits[0].name") {
			t.Errorf("the refusal does not name the omits field: %v", halt)
		}
	})

	t.Run("an attachment's omits entry reading its callers' render facts undeclared", func(t *testing.T) {
		t.Parallel()
		fr := callerFactsFR("valuesMapping", "a: 1", false)
		fr.Spec.ValuesMapping.Inline = ""
		fr.Spec.Omits = []types.OmitEntry{{Kind: "Service", Name: `{{ range .context.used_by }}{{ .render_facts }}{{ end }}`}}
		halt := callerFactsHalt(t, fr, nil)
		if !errors.Is(halt, ErrCallerRenderFactsNotDeclared()) {
			t.Fatalf("the refusal does not carry %v: %v", ErrCallerRenderFactsNotDeclared(), halt)
		}
		// An omits entry is a template producing the attachment's own output, not a
		// pipeline step's input or gate, so the step's own rule leaves it to this one.
		if errors.Is(halt, ErrPipelineStepReadsCallerRenderFacts()) {
			t.Errorf("an omits entry was read as a pipeline step's field: %v", halt)
		}
		if !strings.Contains(halt.Error(), "omits[0].name") {
			t.Errorf("the refusal does not name the omits field: %v", halt)
		}
		// The same entry under a FeatureRecipe that declares the read is admitted.
		fr.Spec.ReadsCallerRenderFacts = true
		if halt := callerFactsHalt(t, fr, nil); halt != nil {
			t.Errorf("the entry was refused under a FeatureRecipe declaring the read: %v", halt)
		}
	})

	for _, tc := range []struct {
		name   string
		text   string
		want   error
		other  error
		detail string
	}{
		{name: "a boundary-scope omits entry reading a per-component pillar", text: "{{ .context.instance_id }}-token",
			want: ErrFRTemplateInvalidField(), other: ErrTemplateUnknownRoot(), detail: ".context.instance_id"},
		{name: "a boundary-scope omits entry reading a root nothing defines", text: "{{ .nonesuch.value }}",
			want: ErrTemplateUnknownRoot(), other: ErrFRTemplateInvalidField(), detail: ".nonesuch.value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := templateFieldFR(executionScopeBoundary, "provides", "{{ .boundary.boundary_name }}")
			fr.Spec.Provides = types.OrderedMap[string]{}
			fr.Spec.Omits = []types.OmitEntry{{Kind: "Secret", Name: tc.text}}
			halt := declaredTemplateFieldHalt(t, fr)
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			if errors.Is(halt, tc.other) {
				t.Errorf("the halt also carries %v: %v", tc.other, halt)
			}
			for _, want := range []string{tc.detail, "omits[0].name"} {
				if !strings.Contains(halt.Error(), want) {
					t.Errorf("the halt does not name %q: %v", want, halt)
				}
			}
		})
	}
}
