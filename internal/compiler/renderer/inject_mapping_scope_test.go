// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file hold what an injectMapping may read, at the two sites
// that judge one: the rule over a boundary-scope FeatureRecipe's inline template
// fields, which checks the mapping before anything is evaluated, and the
// evaluation itself, which runs the mapping once per consuming component whatever
// the recipe's execution scope.
//
// A mapping runs on the consuming component's context, so it reads .context and
// the attachment's own pillars, and never the boundary view — nothing sets it
// there. Each proof drives text both ways round and asserts the halt's own
// identity, so a site that stopped selecting the mapping's scope fails a row here
// rather than refusing or admitting everything alike.

// TestTheBoundaryScopeInjectMappingIsAdmittedAgainstTheConsumerScope is the
// admission proof, driven through the production invocation site of the rules
// over a boundary-declared attachment's template fields.
//
// The injectMapping rows read exactly what the boundary scope refuses and refuse
// exactly what it admits; the valuesMapping rows beside them carry the same text
// and keep the boundary scope's verdicts, so the move is the injectMapping field's
// alone.
func TestTheBoundaryScopeInjectMappingIsAdmittedAgainstTheConsumerScope(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		block string
		text  string
		// want is the identity the halt carries; nil is an admitted field.
		want error
		// other is the identity the halt must not carry.
		other error
		// detail is a phrase the halt has to carry.
		detail []string
		// absent is a phrase the halt must not carry: the boundary view is not a
		// root the mapping's scope lists.
		absent string
	}{
		{name: "the consumer's identity in injectMapping", block: "injectMapping",
			text: `env: {SEEN_BY: "{{ .context.instance_id }}"}`},
		// The attachment's own delivery pipe is a pillar the boundary scope refuses.
		// It is a closed pillar, so the read names one of the mechanisms it defines.
		{name: "the attachment's delivery pipe in injectMapping", block: "injectMapping",
			text: `env: {PORT: "{{ .inject.env.PORT }}"}`},
		{
			// And a key beneath it that the pipe does not define is still refused:
			// the mapping's scope admits the pillar, not every spelling under it.
			name: "a key the delivery pipe does not define in injectMapping", block: "injectMapping",
			text:   `env: {PORT: "{{ .inject.PORT }}"}`,
			want:   ErrTemplateUnknownRoot(),
			other:  ErrFRTemplateInvalidField(),
			detail: []string{".inject.PORT", "injectMapping", ".inject.env"},
		},
		{name: "the attachment instance in injectMapping", block: "injectMapping",
			text: `env: {ATTACHMENT: "{{ .attachment_name }}"}`},
		{name: "the consumer's connections in injectMapping", block: "injectMapping",
			text: `{{ range $alias, $edge := .context.uses }}{{ $alias }}{{ end }}`},
		{
			name: "the boundary view in injectMapping", block: "injectMapping",
			text:   `env: {PATH: "{{ .boundary.path }}"}`,
			want:   ErrTemplateUnknownRoot(),
			other:  ErrFRTemplateInvalidField(),
			detail: []string{".boundary.path", "injectMapping", ".context", ".attachment_name"},
			// The root list closes the quoted set and the boundary view is the
			// context type's last root, so a list still carrying it ends this way.
			absent: `, .boundary"`,
		},
		{
			name: "a guard on the boundary view in injectMapping", block: "injectMapping",
			text:   `env: {HAS: "{{ if .boundary }}yes{{ else }}no{{ end }}"}`,
			want:   ErrTemplateUnknownRoot(),
			other:  ErrFRTemplateInvalidField(),
			detail: []string{".boundary", "injectMapping"},
		},
		{
			// The control: the consumer's identity in a field evaluated once for the
			// boundary is still the per-component pillar it was.
			name: "the consumer's identity in valuesMapping", block: "valuesMapping",
			text:   `x-seen: "{{ .context.instance_id }}"`,
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: []string{".context.instance_id", "valuesMapping"},
		},
		{
			// And the boundary view there is still what such a field reads.
			name: "the boundary view in valuesMapping", block: "valuesMapping",
			text: `x-path: "{{ .boundary.path }}"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := declaredTemplateFieldHalt(t, templateFieldFR(executionScopeBoundary, tc.block, tc.text))
			if tc.want == nil {
				if halt != nil {
					t.Fatalf("the rules refused a %s reading what its scope sets: %v", tc.block, halt)
				}
				return
			}
			if halt == nil {
				t.Fatalf("the rules admitted a %s carrying a planted %v", tc.block, tc.want)
			}
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			if errors.Is(halt, tc.other) {
				t.Errorf("the halt also carries %v, which this field does not commit: %v", tc.other, halt)
			}
			text := haltChainText(halt)
			for _, phrase := range tc.detail {
				if !strings.Contains(text, phrase) {
					t.Errorf("the halt does not carry %q: %s", phrase, text)
				}
			}
			if tc.absent != "" && strings.Contains(text, tc.absent) {
				t.Errorf("the halt lists %q among the roots the field may read: %s", tc.absent, text)
			}
			t.Logf("HALT %s -> %s", tc.name, text)
		})
	}
}

// haltChainText is the text of every link of a halt's cause chain, one link per
// line, read the way a reader of the printed diagnostic reads every cause beneath
// its heading. A wrap names the pass that failed and the cause beneath it names the
// reference, so a phrase asserted on the heading alone would be asserted on the one
// link that cannot carry it. A link joining several findings is walked into each.
func haltChainText(err error) string {
	var b strings.Builder
	var walk func(error)
	walk = func(e error) {
		for e != nil {
			b.WriteString(e.Error())
			b.WriteString("\n")
			if joined, ok := e.(interface{ Unwrap() []error }); ok {
				for _, sub := range joined.Unwrap() {
					walk(sub)
				}
				return
			}
			e = errors.Unwrap(e)
		}
	}
	walk(err)
	return b.String()
}

// TestEveryInjectMappingIsJudgedByTheScopeItsEvaluationReads is the one-scope
// proof: the admission of a boundary-scope FeatureRecipe's injectMapping and the
// evaluation of every injectMapping read the same declaration.
//
// # Identity, not agreement
//
// Two root lists that agree today are what this exists to rule out, so agreement
// is not what is asserted. The scope the admission selects for the mapping must
// hold the very narrowing the evaluation's declaration holds — the same backing
// list — which a second list spelling the same root does not.
//
// # And the two sites refuse alike
//
// The evaluation is driven for a recipe of each execution scope, since it runs
// the mapping per consuming component whatever the recipe states: the boundary
// view is refused with the identity and the root list the admission reports, and
// the consumer's identity is delivered.
func TestEveryInjectMappingIsJudgedByTheScopeItsEvaluationReads(t *testing.T) {
	t.Parallel()

	admission := boundaryScopeFRFieldScope(featureRecipeFieldInjectMapping)
	if len(admission.exclude) == 0 || len(injectMappingScope.exclude) == 0 {
		t.Fatal("the injectMapping scope declares no narrowing; there is nothing to compare")
	}
	if &admission.exclude[0] != &injectMappingScope.exclude[0] {
		t.Fatal("the admission judges the injectMapping against a narrowing of its own rather than " +
			"the declaration its evaluation reads")
	}

	// Every other field keeps the boundary scope, and the enumeration labels the
	// mapping with the spelling the selection recognises.
	fr := templateFieldFR(executionScopeBoundary, "injectMapping", "{{ .values.a }}")
	fr.Spec.ValuesMapping.Inline = "{{ .values.b }}"
	sawMapping := false
	for _, field := range BoundaryScopeFRTemplateFields(fr) {
		scope := boundaryScopeFRFieldScope(field.Label)
		want := boundaryScopeFRScope.exclude
		if field.Label == featureRecipeFieldInjectMapping {
			sawMapping = true
			want = injectMappingScope.exclude
		}
		if &scope.exclude[0] != &want[0] {
			t.Errorf("field %q is judged against a scope other than the one it is evaluated in", field.Label)
		}
	}
	if !sawMapping {
		t.Fatal("the enumeration presents no field under the label the scope selection recognises")
	}

	const boundaryRead = `{"env": {"PATH": "{{ .boundary.path }}"}}`
	refused := classifyBoundaryScopeFRTemplate(featureRecipeFieldInjectMapping, boundaryRead)
	if refused.Fault != boundaryScopeFRTemplateUnknownRoot || refused.Violation == nil {
		t.Fatalf("the admission classified the boundary view in the injectMapping as fault %d; want an "+
			"undefined reference", refused.Fault)
	}
	_, admittedPhrase := templateAdmittedSetPhrase(refused.Violation)

	for _, scope := range []string{executionScopeBoundary, "component", ""} {
		t.Run("evaluation, execution scope "+scope, func(t *testing.T) {
			t.Parallel()

			ctx, refs := phaseALocalsFixture(types.OrderedMap[string]{}, boundaryRead)
			refs[0].FeatureRecipe.Spec.ExecutionScope = scope
			_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
			if err == nil {
				t.Fatal("the evaluation admitted a mapping reading the boundary view")
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Fatalf("the evaluation's refusal does not carry %v: %v", ErrTemplateUnknownRoot(), err)
			}
			if text := haltChainText(err); !strings.Contains(text, admittedPhrase) {
				t.Errorf("the evaluation lists roots other than the admission's %q: %s", admittedPhrase, text)
			}

			// The control: the consumer's identity is delivered on the same fixture.
			ctx, refs = phaseALocalsFixture(types.OrderedMap[string]{},
				`{"env": {"SEEN_BY": "{{ .context.instance_id }}"}}`)
			refs[0].FeatureRecipe.Spec.ExecutionScope = scope
			if _, err := ExecutePhaseA(ctx, refs, hostWithheld{}); err != nil {
				t.Fatalf("the evaluation refused the consumer's identity: %v", err)
			}
			if got, _ := ctx.Context.Inject.Env.Get("SEEN_BY"); got != "checkout-api" {
				t.Errorf("SEEN_BY = %q, want the consuming component %q", got, "checkout-api")
			}
		})
	}
}

// TestExecutePhaseA_AComponentScopeInjectMappingReadingTheBoundaryViewIsRefused
// holds the refusal at the component scope, where no rule runs before the node
// walk and the evaluation is the only site that judges a mapping.
//
// Both spellings are driven: a read through the view, which would otherwise fail
// while the template runs, and a guard on it, which would otherwise build and read
// nothing.
func TestExecutePhaseA_AComponentScopeInjectMappingReadingTheBoundaryViewIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, mapping string }{
		{"a read through the view", `{"env": {"PATH": "{{ .boundary.path }}"}}`},
		{"a guard on the view", `{"env": {"HAS": "{{ if .boundary }}yes{{ else }}no{{ end }}"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, refs := phaseALocalsFixture(types.OrderedMap[string]{}, tc.mapping)
			_, err := ExecutePhaseA(ctx, refs, hostWithheld{})
			if err == nil {
				t.Fatal("a component-scope mapping reading the boundary view was admitted")
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Fatalf("the refusal does not carry %v: %v", ErrTemplateUnknownRoot(), err)
			}
			text := haltChainText(err)
			for _, phrase := range []string{".boundary", "injectMapping", "the render context defines .context"} {
				if !strings.Contains(text, phrase) {
					t.Errorf("the refusal does not carry %q: %s", phrase, text)
				}
			}
			if _, delivered := ctx.Context.Inject.Env.Get("HAS"); delivered {
				t.Error("the refused mapping still delivered onto the pipe")
			}
			t.Logf("HALT %s -> %s", tc.name, text)
		})
	}
}
