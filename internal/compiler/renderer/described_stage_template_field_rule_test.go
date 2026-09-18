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

// The proofs in this file cover the two rules declared over the inline template
// fields a boundary-declared attachment's FeatureRecipe writes.
//
// Every proof drives the PRODUCTION invocation site over the real presentation
// and the real enumeration, and each asserts against the halt's own identity
// rather than against "an error". A rule that is declared, registered and reached
// by nothing raises nothing, which is indistinguishable from a rule that ran and
// approved — so the calibration below measures the subject count before any
// verdict is read, and the admitted control beside each refusal shows the halt is
// a consequence of the planted fault rather than of the fixture.
//
// The execution-scope rows are not decoration. The axis enumerates every
// attachment a boundary declares, whatever scope its FeatureRecipe states, so an
// attachment that generates per component carries the SAME offending text through
// the same walk. Refusing it would reject legal authoring, and only a row driving
// that exact pairing can tell a rule that selects from one that does not.

const (
	templateFieldBoundaryPath = "prod"
	templateFieldInstance     = "shared-db"
	templateFieldType         = "core/database:1.0.0"
	templateFieldRecipeName   = "boundary-template-fr"
)

// templateFieldFR is the FeatureRecipe the boundary attachment binds: one
// authored template field written into the block the caller names, under the
// execution scope the caller states.
//
// The field is selected by its block rather than fixed, because the family this
// covers is the whole set of template-bearing fields and a proof driving only one
// of them would say nothing about the others.
func templateFieldFR(executionScope, block, text string) *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = templateFieldRecipeName
	fr.Spec.ExecutionScope = executionScope
	switch block {
	case "valuesMapping":
		fr.Spec.ValuesMapping.Inline = text
	case "injectMapping":
		fr.Spec.InjectMapping = text
	case "workload":
		fr.Spec.Workload.Inline = text
	case "artifacts":
		artifacts := types.OrderedMap[types.ArtifactEntry]{}
		artifacts.Set("cm.yaml", types.ArtifactEntry{Inline: text})
		fr.Spec.Artifacts = artifacts
	case "provides":
		fr.Spec.Provides = omLocals("dsn", text)
	case "locals":
		fr.Spec.Locals = omLocals("host", text)
	case "pipeline":
		fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: text}}
	default:
		panic("templateFieldFR: unknown block " + block)
	}
	return fr
}

// templateFieldWorld builds the render products the rules read: a graph carrying
// one boundary with one attachment, the deployment document the boundary axis is
// derived from, and the binding that resolves that attachment to the
// FeatureRecipe under test.
func templateFieldWorld(fr *types.FeatureRecipe) (*types.IRGraph, *resolver.ResolveResult, RecipeLookup) {
	boundaryAttachments := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryAttachments.Set(templateFieldInstance, types.IRBoundaryFeature{Type: templateFieldType})
	perBoundary := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	perBoundary.Set(templateFieldBoundaryPath, boundaryAttachments)

	graph := &types.IRGraph{Spec: types.IRSpec{BoundaryFeatures: perBoundary}}

	features := types.OrderedMap[types.BoundaryFeature]{}
	features.Set(templateFieldInstance, types.BoundaryFeature{Type: templateFieldType})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(templateFieldBoundaryPath, types.Boundary{Features: features})
	release := &types.Release{}
	release.Spec.Boundaries = boundaries

	lookup := RecipeLookup{
		FeatureRecipe: func(instanceName, _ string) (*types.FeatureRecipe, bool) {
			if instanceName == templateFieldInstance {
				return fr, fr != nil
			}
			return nil, false
		},
	}
	return graph, &resolver.ResolveResult{Release: release, ReleasePath: "release.yaml"}, lookup
}

// declaredTemplateFieldHalt drives both template-field rules through the
// production invocation site over one authored world, and returns the halt.
//
// The calibration is taken before the verdict is read, and it walks the axes the
// TRAVERSAL walks rather than the inner accessor alone: an inner accessor
// answering correctly for a coordinate the outer axis never yields produces no
// subject at all, and a walk over no subject raises nothing — which reads exactly
// like a walk that ran and approved.
func declaredTemplateFieldHalt(t *testing.T, fr *types.FeatureRecipe) error {
	t.Helper()
	graph, resolved, lookup := templateFieldWorld(fr)
	recipes := map[string]*types.Recipe{}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(graph, localsAxis{}, recipes, lookup, resolved)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound FeatureRecipe")
	}

	scopes := axes.Boundaries()
	if len(scopes) != 1 {
		t.Fatalf("the boundary axis presents %d boundary(s); want exactly the one the deployment declares",
			len(scopes))
	}
	total := 0
	for _, scope := range scopes {
		attached := axes.BoundaryAttachments(scope.Value)
		if len(attached) == 0 {
			t.Fatal("the boundary presents no attachment, so no field-borne subject exists to judge")
		}
		for _, attachment := range attached {
			total += len(axes.BoundaryAttachmentTemplateFields(scope.Value, attachment))
		}
	}
	if want := len(BoundaryScopeFRTemplateFields(fr)); total != want {
		t.Fatalf("the field axis presents %d template field(s) for a FeatureRecipe writing %d; "+
			"the verdict below would certify nothing", total, want)
	}
	if total == 0 {
		t.Fatal("the field axis presents no template field; the verdict below would certify nothing")
	}

	return runAuthoredTemplateFieldRule(context.Background(), view, axes)
}

// TestTheTemplateFieldRulesAreReachedAndTheirObjectionsHalt is the invocation
// proof: one planted fault per fixture, driven through the production site,
// asserted against the halt's own identity.
//
// # One fault per fixture, and the identity is the assertion
//
// Each row plants exactly ONE offending field and names the identity that field
// commits. Asserting "an error" would keep passing once some other gate rejected
// the fixture first, and the two identities have separate remedies — a
// per-component pillar is rewritten against the boundary pillars, a reference
// that exists nowhere is corrected — so a row that could not tell them apart
// would not be watching the distinction the two rules exist to draw.
//
// # Every field family is driven, because the family is what is being proved
//
// The rows walk each of the blocks a FeatureRecipe writes a template into. A
// proof driving one of them would certify one field rather than the enumeration,
// and it is the enumeration that makes coverage assertable here.
func TestTheTemplateFieldRulesAreReachedAndTheirObjectionsHalt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		block string
		text  string
		want  error
		other error
		// detail is a phrase the halt has to carry, so a rule reporting the right
		// identity about the wrong reference still fails.
		detail string
		// field is the label the halt has to name. It is asserted separately from
		// the locator because the locator cannot always carry it: the dialect
		// stops at an authored key containing its own separator, so an artifact
		// entry keyed by a filename positions at the block and no further. Where
		// the position stops short the detail is the only thing that says which
		// field inside the block offends, and a rule that dropped it would report
		// a real fault an author cannot locate.
		field string
	}{
		{
			name:   "a per-component pillar in valuesMapping",
			block:  "valuesMapping",
			text:   "image: {{ .workload.image }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".workload.image",
			field:  "valuesMapping",
		},
		{
			name:   "a per-component pillar in injectMapping",
			block:  "injectMapping",
			text:   "PORT: {{ .inject.PORT }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".inject.PORT",
			field:  "injectMapping",
		},
		{
			name:   "a per-component pillar in an inline workload",
			block:  "workload",
			text:   "name: {{ .attachment_name }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".attachment_name",
			field:  "workload",
		},
		{
			name:   "a per-component pillar in an inline artifact",
			block:  "artifacts",
			text:   "id: {{ .context.instance_id }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".context.instance_id",
			field:  "artifacts[cm.yaml]",
		},
		{
			name:   "a per-component pillar in a provides entry",
			block:  "provides",
			text:   "{{ .context.instance_id }}.svc",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".context.instance_id",
			field:  "provides[dsn]",
		},
		{
			name:   "a per-component pillar in a locals entry",
			block:  "locals",
			text:   "{{ .inject_raw }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".inject_raw",
			field:  "locals[host]",
		},
		{
			name:   "a per-component pillar in a pipeline step's with",
			block:  "pipeline",
			text:   "target: {{ .workload }}",
			want:   ErrFRTemplateInvalidField(),
			other:  ErrTemplateUnknownRoot(),
			detail: ".workload",
			field:  "pipeline[patch].with",
		},
		{
			// The second identity: a root the render context does not define at
			// all. No rewriting against this surface's pillars fixes it, which is
			// why it is not the identity above.
			name:   "a root the context does not define at all",
			block:  "provides",
			text:   "{{ .nonesuch.value }}",
			want:   ErrTemplateUnknownRoot(),
			other:  ErrFRTemplateInvalidField(),
			detail: ".nonesuch.value",
			field:  "provides[dsn]",
		},
		{
			// The same identity reached at the other nesting: a mistyped key
			// beneath a CLOSED pillar. Without this row the key level of the
			// admission would be unexercised, and a key typo would reach the
			// emitted artifact as an empty value.
			name:   "a mistyped key beneath a closed pillar",
			block:  "locals",
			text:   "{{ .boundary.boundary_nam }}",
			want:   ErrTemplateUnknownRoot(),
			other:  ErrFRTemplateInvalidField(),
			detail: ".boundary.boundary_nam",
			field:  "locals[host]",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := templateFieldFR(executionScopeBoundary, tc.block, tc.text)
			halt := declaredTemplateFieldHalt(t, fr)
			if halt == nil {
				t.Fatalf("the rules admitted a field carrying a planted %v", tc.want)
			}
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			// The other identity must NOT be reported: the two are separate
			// findings with separate remedies, and one field commits at most one.
			if errors.Is(halt, tc.other) {
				t.Errorf("the halt also carries %v, which this field does not commit: %v", tc.other, halt)
			}
			if !strings.Contains(halt.Error(), tc.detail) {
				t.Errorf("the halt does not name %q, so it reports the right identity about an unnamed "+
					"reference: %v", tc.detail, halt)
			}
			if !strings.Contains(halt.Error(), tc.field) {
				t.Errorf("the halt does not name the field %q, so an author whose position stops at "+
					"the block cannot tell which field offends: %v", tc.field, halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}

	// The control: the same world with a field reading a pillar this surface
	// populates is admitted, so every refusal above is attributable to the
	// planted fault and not to the fixture.
	t.Run("a field reading a boundary pillar is admitted", func(t *testing.T) {
		t.Parallel()
		fr := templateFieldFR(executionScopeBoundary, "provides", "{{ .boundary.boundary_name }}.svc")
		if halt := declaredTemplateFieldHalt(t, fr); halt != nil {
			t.Fatalf("the rules refused a field whose only reference names a pillar this surface "+
				"populates: %v", halt)
		}
	})
}

// TestTheTemplateFieldRulesEndOnAnAttachmentThatGeneratesPerComponent is the
// no-verdict-move proof, and it is the row the axis alone cannot supply.
//
// Declaring an attachment ON a boundary and declaring a FeatureRecipe that runs
// ONCE PER boundary are two separate decisions. The axis enumerates every
// attachment a boundary declares, so an attachment that generates per component
// reaches these rules carrying the same text — and its templates are evaluated
// against the full per-component context, where the pillars the rules refuse are
// exactly the ones they are supposed to read.
//
// So each row here drives text the rules DO refuse under the boundary scope, and
// asserts it is admitted under the other. A rule that skipped the scope reading
// would pass every row of the test above and fail every row here, which is what
// makes this the discriminating proof rather than a repetition.
func TestTheTemplateFieldRulesEndOnAnAttachmentThatGeneratesPerComponent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		scope string
	}{
		{name: "a FeatureRecipe stating the component scope", scope: "component"},
		{name: "a FeatureRecipe stating no scope at all", scope: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, block := range []string{"valuesMapping", "provides", "locals", "pipeline"} {
				fr := templateFieldFR(tc.scope, block, "{{ .context.instance_id }}")
				if halt := declaredTemplateFieldHalt(t, fr); halt != nil {
					t.Errorf("the rules refused %s on an attachment that generates per component, "+
						"where that pillar is exactly what its template is supposed to read: %v", block, halt)
				}
			}
		})
	}

	// The positive control for this table: the identical world under the boundary
	// scope IS refused, so the admissions above are the scope reading and not a
	// fixture that refuses nothing.
	t.Run("the same world under the boundary scope is refused", func(t *testing.T) {
		t.Parallel()
		fr := templateFieldFR(executionScopeBoundary, "provides", "{{ .context.instance_id }}")
		halt := declaredTemplateFieldHalt(t, fr)
		if halt == nil {
			t.Fatal("the rules admitted a per-component pillar under the boundary scope; the " +
				"admissions above prove nothing about the scope reading")
		}
		if !errors.Is(halt, ErrFRTemplateInvalidField()) {
			t.Fatalf("the halt does not carry %v: %v", ErrFRTemplateInvalidField(), halt)
		}
	})
}

// TestTheTemplateFieldEnumerationCarriesEveryInlineBlock holds the shared
// enumeration to the field set it is the one home of.
//
// It is asserted as a SET DIFFERENCE naming what is missing rather than as a
// count, because two sides short by one still agree on a count. The enumeration
// is what both the render's own refusal and the declared rules walk, so a block
// dropped from it goes unchecked on both surfaces at once with nothing reporting
// the omission — which is the failure an enumerated axis exists to prevent.
func TestTheTemplateFieldEnumerationCarriesEveryInlineBlock(t *testing.T) {
	t.Parallel()

	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = templateFieldRecipeName
	fr.Spec.ValuesMapping.Inline = "{{ .values.a }}"
	fr.Spec.InjectMapping = "{{ .values.b }}"
	fr.Spec.Workload.Inline = "{{ .values.c }}"
	artifacts := types.OrderedMap[types.ArtifactEntry]{}
	artifacts.Set("cm.yaml", types.ArtifactEntry{Inline: "{{ .values.d }}"})
	fr.Spec.Artifacts = artifacts
	fr.Spec.Provides = omLocals("dsn", "{{ .values.e }}")
	fr.Spec.Locals = omLocals("host", "{{ .values.f }}")
	fr.Spec.Pipeline = []types.PipelineStep{{ID: "patch", With: "{{ .values.g }}"}}

	// The label and the authored keys are both asserted, because they answer two
	// different questions and a correct label over wrong keys positions a finding
	// at a path the file does not contain.
	want := map[string][]string{
		"valuesMapping":        {"valuesMapping"},
		"injectMapping":        {"injectMapping"},
		"workload":             {"workload"},
		"artifacts[cm.yaml]":   {"artifacts", "cm.yaml"},
		"provides[dsn]":        {"provides", "dsn"},
		"locals[host]":         {"locals", "host"},
		"pipeline[patch].with": {"pipeline", "patch", "with"},
	}

	got := map[string][]string{}
	for _, field := range BoundaryScopeFRTemplateFields(fr) {
		got[field.Label] = field.AuthoredKeys
	}
	for label, keys := range want {
		haveKeys, present := got[label]
		if !present {
			t.Errorf("the enumeration omits %q, so a template written there is checked by nothing", label)
			continue
		}
		if strings.Join(haveKeys, ".") != strings.Join(keys, ".") {
			t.Errorf("%q is located at .spec.%s; want .spec.%s",
				label, strings.Join(haveKeys, "."), strings.Join(keys, "."))
		}
	}
	for label := range got {
		if _, expected := want[label]; !expected {
			t.Errorf("the enumeration carries %q, which this proof does not account for; add a row "+
				"rather than widening the assertion", label)
		}
	}

	// A field whose content is imported rather than written inline carries no
	// authored text, and the bound is asserted rather than left implied: such a
	// field is read when the render reaches it, so enumerating it here would
	// present a rule with nothing to judge.
	imported := &types.FeatureRecipe{}
	imported.Metadata.Name = templateFieldRecipeName
	if fields := BoundaryScopeFRTemplateFields(imported); len(fields) != 0 {
		t.Errorf("a FeatureRecipe writing no inline template presents %d field(s); want none", len(fields))
	}
}
