// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules declared over the path templates a
// deployment's boundaries write.
//
// Every proof drives the PRODUCTION invocation site over the real presentation
// and the real enumeration, and each asserts against the halt's own identity
// rather than against "an error". A rule that is declared, registered and reached
// by nothing raises nothing, which is indistinguishable from a rule that ran and
// approved — so the calibration below measures the subject count before any
// verdict is read, and the admitted control beside each refusal shows the halt is
// a consequence of the planted fault rather than of the fixture.
//
// The per-field rows are not decoration, and they are what makes this carrier
// different from the one beside it. The two fields are checked against two
// DIFFERENT admitted sets over one context type, so a rule reading one set for
// both would refuse legal authoring on one field or admit an offending reference
// on the other. Only a row driving the same text through both fields can tell a
// rule that selects per field from one that does not.

const (
	pathTemplateOuterScope = "prod"
	pathTemplateInnerScope = "edge"
	pathTemplateScopePath  = pathTemplateOuterScope + "/" + pathTemplateInnerScope
	pathTemplateFile       = "release.yaml"

	// pathTemplateScopeLocator is the inner scope's site as the deployment
	// document spells it: the container's key repeated once per level of the
	// nesting, which is what a single spliced segment would lose.
	pathTemplateScopeLocator = ".spec.boundaries." + pathTemplateOuterScope +
		".boundaries." + pathTemplateInnerScope
)

// pathTemplateWorld builds the render products the rules read: a deployment whose
// scopes nest, with the path templates the caller states written on the inner one.
//
// The scopes nest because the locator repeats the container's key for each level,
// and a single-level fixture would pass a traversal that dropped the repetition.
func pathTemplateWorld(directory, subDirectory string) (*types.IRGraph, *resolver.ResolveResult) {
	inner := types.Boundary{Directory: directory, SubDirectory: subDirectory}
	nested := types.OrderedMap[types.Boundary]{}
	nested.Set(pathTemplateInnerScope, inner)

	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(pathTemplateOuterScope, types.Boundary{Boundaries: nested})

	release := &types.Release{}
	release.Spec.Boundaries = boundaries

	return &types.IRGraph{}, &resolver.ResolveResult{Release: release, ReleasePath: pathTemplateFile}
}

// declaredPathTemplateHalt drives both path-template rules through the production
// invocation site over one authored world, and returns the halt.
//
// The calibration is taken before the verdict is read, and it walks the axes the
// TRAVERSAL walks rather than the inner accessor alone: an inner accessor
// answering correctly for a coordinate the outer axis never yields produces no
// subject at all, and a walk over no subject raises nothing — which reads exactly
// like a walk that ran and approved.
//
// It also asserts the presentation answers the text the axis enumerated. The axis
// and the presentation are two readings of one document, and a presentation that
// answered empty would leave every rule deciding about a template nobody wrote.
func declaredPathTemplateHalt(t *testing.T, directory, subDirectory string) error {
	t.Helper()
	graph, resolved := pathTemplateWorld(directory, subDirectory)
	recipes := map[string]*types.Recipe{}
	lookup := RecipeLookup{}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, resolved.Release, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the presentation was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{}, recipes, lookup, resolved)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a deployment document")
	}

	scopes := axes.Boundaries()
	if len(scopes) != 2 {
		t.Fatalf("the boundary axis presents %d scope(s); want the two the deployment nests", len(scopes))
	}
	total := 0
	for _, scope := range scopes {
		for _, field := range axes.BoundaryPathTemplateFields(scope.Value) {
			total++
			if text := view.BoundaryAuthoredPathTemplateText(scope.Value, field); text == "" {
				t.Fatalf("the axis presents %q on %q and the presentation answers it empty; "+
					"the verdict below would decide about text nobody wrote", field, scope.Value)
			}
		}
	}
	want := len(BoundaryPathTemplateFields(types.Boundary{Directory: directory, SubDirectory: subDirectory}))
	if total != want {
		t.Fatalf("the field axis presents %d path template(s) for a deployment writing %d; "+
			"the verdict below would certify nothing", total, want)
	}
	if total == 0 {
		t.Fatal("the field axis presents no path template; the verdict below would certify nothing")
	}

	return runAuthoredPathTemplateRule(context.Background(), view, axes)
}

// TestThePathTemplateRulesAreReachedAndTheirObjectionsHalt is the invocation
// proof: one planted fault per fixture, driven through the production site,
// asserted against the halt's own identity.
//
// # One fault per fixture, and the identity is the assertion
//
// Each row plants exactly ONE offending template and names the identity it
// commits. Asserting "an error" would keep passing once some other gate rejected
// the fixture first, and the two identities have separate remedies — a variable
// this surface does not offer is rewritten against the ones it does, a mistyped
// key beneath an offered variable is corrected — so a row that could not tell
// them apart would not be watching the distinction the two rules exist to draw.
//
// # Both fields are driven, because the field axis is what is being proved
//
// A proof driving one field would certify one surface rather than the
// enumeration, and it is the enumeration that makes coverage assertable here.
func TestThePathTemplateRulesAreReachedAndTheirObjectionsHalt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		directory    string
		subDirectory string
		want         error
		other        error
		// detail is a phrase the halt has to carry, so a rule reporting the right
		// identity about the wrong reference still fails.
		detail string
		// field is the authored key the halt has to name. A scope writes more than
		// one path template, so a halt naming only the scope leaves an operator
		// unable to tell which of them offends.
		field string
	}{
		{
			name:         "a variable the context does not define, in directory",
			directory:    "{{ .region }}",
			subDirectory: "{{ .capability }}",
			want:         ErrPathTemplateInvalidField(),
			other:        ErrTemplateUnknownRoot(),
			detail:       ".region",
			field:        "directory",
		},
		{
			name:         "a variable the context does not define, in subDirectory",
			directory:    "regions/{{ .boundary_name }}",
			subDirectory: "{{ .region }}",
			want:         ErrPathTemplateInvalidField(),
			other:        ErrTemplateUnknownRoot(),
			detail:       ".region",
			field:        "subDirectory",
		},
		{
			// The row the whole per-field selection exists for. This variable IS
			// defined by the context type and IS admitted on the field below, and
			// it is refused here because a whole-scope template is evaluated once
			// for the scope — so admitting one component's id would make the
			// emitted path depend on which component was placed first.
			name:         "the component's own id in directory, which is evaluated once for the scope",
			directory:    "{{ .component_id }}",
			subDirectory: "{{ .capability }}",
			want:         ErrPathTemplateInvalidField(),
			other:        ErrTemplateUnknownRoot(),
			detail:       ".component_id",
			field:        "directory",
		},
		{
			name:         "a mistyped key beneath a closed variable, in directory",
			directory:    "{{ .manifest.recipe_nmae }}",
			subDirectory: "{{ .capability }}",
			want:         ErrTemplateUnknownRoot(),
			other:        ErrPathTemplateInvalidField(),
			detail:       ".manifest.recipe_nmae",
			field:        "directory",
		},
		{
			name:         "a mistyped key beneath a closed variable, in subDirectory",
			directory:    "regions/{{ .boundary_name }}",
			subDirectory: "{{ .manifest.recipe_nmae }}",
			want:         ErrTemplateUnknownRoot(),
			other:        ErrPathTemplateInvalidField(),
			detail:       ".manifest.recipe_nmae",
			field:        "subDirectory",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := declaredPathTemplateHalt(t, tc.directory, tc.subDirectory)
			if halt == nil {
				t.Fatalf("the rules admitted a path template carrying a planted %v", tc.want)
			}
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			// The other identity must NOT be reported: the two are separate
			// findings with separate remedies, and one template commits at most one.
			if errors.Is(halt, tc.other) {
				t.Errorf("the halt also carries %v, which this template does not commit: %v", tc.other, halt)
			}
			if !strings.Contains(halt.Error(), tc.detail) {
				t.Errorf("the halt does not name %q, so it reports the right identity about an unnamed "+
					"reference: %v", tc.detail, halt)
			}
			if !strings.Contains(halt.Error(), tc.field) {
				t.Errorf("the halt does not name the field %q, so an operator cannot tell which of the "+
					"scope's templates offends: %v", tc.field, halt)
			}
			// The position is asserted as the locator the deployment document
			// actually spells, with the container's key repeated for the nesting.
			// A traversal splicing the scope path in as one segment would produce
			// .spec.boundaries.prod/edge.<field> — a key no file contains — and
			// would pass an assertion looking for the path in the message.
			if want := pathTemplateScopeLocator + "." + tc.field; !strings.Contains(halt.Error(), want) {
				t.Errorf("the halt does not position at %q, so an operator is sent to the document and "+
					"no further: %v", want, halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}

	// The control: the same world with both templates reading variables this
	// surface offers is admitted, so every refusal above is attributable to the
	// planted fault and not to the fixture.
	t.Run("templates reading offered variables are admitted", func(t *testing.T) {
		t.Parallel()
		halt := declaredPathTemplateHalt(t,
			"regions/{{ .boundary_name }}", "{{ .capability }}/{{ .manifest.recipe_name }}")
		if halt != nil {
			t.Fatalf("the rules refused templates whose every reference names a variable this surface "+
				"offers: %v", halt)
		}
	})
}

// TestThePathTemplateRulesSelectTheAdmittedSetPerField is the discriminating
// proof, and it is the row the axis alone cannot supply.
//
// The component's own id is defined by the one context type both fields are
// evaluated against, so a rule holding a single admitted set for the carrier
// either refuses it on both fields or admits it on both. It is genuinely refused
// on one and genuinely admitted on the other, so this pair separates a rule that
// selects per field from one that does not — which every row of the table above
// would pass either way.
func TestThePathTemplateRulesSelectTheAdmittedSetPerField(t *testing.T) {
	t.Parallel()

	const perComponent = "{{ .component_id }}"

	t.Run("admitted on subDirectory, which is evaluated once per component", func(t *testing.T) {
		t.Parallel()
		if halt := declaredPathTemplateHalt(t, "regions/{{ .boundary_name }}", perComponent); halt != nil {
			t.Fatalf("the rules refused the component's own id on the field evaluated once for each "+
				"component, where it names exactly the component being placed: %v", halt)
		}
	})

	// The positive control for this pair: the identical text on the other field IS
	// refused, so the admission above is the per-field selection and not a fixture
	// that refuses nothing.
	t.Run("refused on directory, which is evaluated once for the scope", func(t *testing.T) {
		t.Parallel()
		halt := declaredPathTemplateHalt(t, perComponent, "{{ .capability }}")
		if halt == nil {
			t.Fatal("the rules admitted the component's own id on the field evaluated once for the " +
				"scope; the admission above proves nothing about the per-field selection")
		}
		if !errors.Is(halt, ErrPathTemplateInvalidField()) {
			t.Fatalf("the halt does not carry %v: %v", ErrPathTemplateInvalidField(), halt)
		}
	})
}

// TestThePathTemplateRulesReportEveryOffendingField holds the declaration to the
// breadth it exists to add over the evaluation beneath it.
//
// The evaluation stops at the first template it reaches. These rules are invoked
// once per field, so a scope offending in both of them tells the operator about
// both from one build.
//
// The breadth is asserted by IDENTITY rather than by reading the joined message,
// and that is not a workaround. Several findings are joined under an envelope
// whose own text names what they are about, so a message read would be asserting
// the envelope's wording; and each field here is planted with a DIFFERENT fault,
// so a halt carrying both identities can only have come from two objections over
// two fields. A rule stopping at the first would carry exactly one.
func TestThePathTemplateRulesReportEveryOffendingField(t *testing.T) {
	t.Parallel()

	halt := declaredPathTemplateHalt(t, "{{ .region }}", "{{ .manifest.recipe_nmae }}")
	if halt == nil {
		t.Fatal("the rules admitted two templates each carrying a planted fault")
	}
	for _, want := range []error{ErrPathTemplateInvalidField(), ErrTemplateUnknownRoot()} {
		if !errors.Is(halt, want) {
			t.Errorf("the halt does not carry %v, so the declaration reports one offending field "+
				"rather than every one: %v", want, halt)
		}
	}
}

// TestThePathTemplateEnumerationCarriesWhatTheScopeWrote holds the shared
// enumeration to the field set it is the one home of.
//
// It is asserted as a set difference naming what is missing rather than as a
// count, because two sides short by one still agree on a count. The enumeration is
// what both the render's own refusal and the declared rules are driven from, so a
// field dropped from it goes unchecked on both surfaces at once with nothing
// reporting the omission.
func TestThePathTemplateEnumerationCarriesWhatTheScopeWrote(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"directory":    "regions/{{ .boundary_name }}",
		"subDirectory": "{{ .capability }}",
	}
	got := map[string]string{}
	for _, field := range BoundaryPathTemplateFields(types.Boundary{
		Directory:    want["directory"],
		SubDirectory: want["subDirectory"],
	}) {
		got[field.Field] = field.Text
	}
	for field, text := range want {
		haveText, present := got[field]
		if !present {
			t.Errorf("the enumeration omits %q, so a template written there is checked by nothing", field)
			continue
		}
		if haveText != text {
			t.Errorf("%q carries %q; want %q", field, haveText, text)
		}
	}
	for field := range got {
		if _, expected := want[field]; !expected {
			t.Errorf("the enumeration carries %q, which this proof does not account for; add a row "+
				"rather than widening the assertion", field)
		}
	}

	// A field the operator left empty carries nothing, and the bound is asserted
	// rather than left implied: the render evaluates a default this package
	// supplies in its place, so enumerating it would present a rule with text
	// nobody authored.
	if fields := BoundaryPathTemplateFields(types.Boundary{}); len(fields) != 0 {
		t.Errorf("a scope writing no path template presents %d field(s); want none", len(fields))
	}
	if fields := BoundaryPathTemplateFields(types.Boundary{SubDirectory: "{{ .capability }}"}); len(fields) != 1 {
		t.Errorf("a scope writing one path template presents %d field(s); want one", len(fields))
	}
}

// TestThePathTemplateRuleAndTheEvaluationShareOneDecision holds the two
// enforcement paths to the one classification they route through.
//
// The evaluation beneath the declaration is retained deliberately — it reaches a
// per-component override of the same field, which no boundary key enumerates, and
// it runs where a render holds no deployment document for the axis to walk. Two
// paths for one rule is dis-unification unless they share the decision, and the
// agreement is exactly what would hide a later divergence. So this asserts they
// report the SAME identity for the same text rather than that they both refuse.
func TestThePathTemplateRuleAndTheEvaluationShareOneDecision(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field string
		text  string
		want  error
	}{
		{
			name:  "a variable the context does not define",
			field: pathTemplateDirectoryField,
			text:  "{{ .region }}",
			want:  ErrPathTemplateInvalidField(),
		},
		{
			name:  "the component's own id on the whole-scope field",
			field: pathTemplateDirectoryField,
			text:  "{{ .component_id }}",
			want:  ErrPathTemplateInvalidField(),
		},
		{
			name:  "a mistyped key beneath a closed variable",
			field: pathTemplateDirectoryField,
			text:  "{{ .manifest.recipe_nmae }}",
			want:  ErrTemplateUnknownRoot(),
		},
		{
			name:  "the same mistyped key on the per-component field",
			field: pathTemplateSubDirectoryField,
			text:  "{{ .manifest.recipe_nmae }}",
			want:  ErrTemplateUnknownRoot(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			directory, subDirectory := "regions/{{ .boundary_name }}", "{{ .capability }}"
			if tc.field == pathTemplateDirectoryField {
				directory = tc.text
			} else {
				subDirectory = tc.text
			}
			declared := declaredPathTemplateHalt(t, directory, subDirectory)
			if declared == nil {
				t.Fatalf("the declared rules admitted text the evaluation refuses with %v", tc.want)
			}
			if !errors.Is(declared, tc.want) {
				t.Fatalf("the declared halt does not carry %v: %v", tc.want, declared)
			}

			_, evaluated := EvalPathTemplate("t", tc.field, pathTemplateScopePath, tc.text,
				PathTemplateContext{Manifest: PathTemplateManifest{RecipeName: "web"}})
			if evaluated == nil {
				t.Fatalf("the evaluation admitted text the declared rules refuse with %v", tc.want)
			}
			if !errors.Is(evaluated, tc.want) {
				t.Fatalf("the evaluation reports a different identity for the same text: want %v, got %v",
					tc.want, evaluated)
			}
		})
	}
}
