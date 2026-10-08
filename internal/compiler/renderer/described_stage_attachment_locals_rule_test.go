// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the two rules pairs declared over the text a
// FeatureRecipe wrote for each of its locals — the block an attachment binds on a
// node, and the block a boundary-scope attachment binds.
//
// Every proof here drives the PRODUCTION invocation site over the real
// presentation and the real enumeration, and each asserts against the halt's own
// condition rather than against "an error". A rule that is declared, registered
// and reached by nothing raises nothing, which is indistinguishable from a rule
// that ran and approved — so the calibration below measures the subject count
// before any verdict is read, and the admitted control beside each refusal shows
// the halt is a consequence of the planted fault rather than of the fixture.

const (
	attachmentLocalsNode       = "api"
	attachmentLocalsInstance   = "mon"
	attachmentLocalsType       = "core/monitoring:1.0.0"
	boundaryLocalsPath         = "prod"
	boundaryLocalsInstance     = "shared-db"
	boundaryLocalsType         = "core/database:1.0.0"
	attachmentLocalsRecipeName = "attachment-locals-fr"
	boundaryLocalsRecipeName   = "boundary-locals-fr"
)

// attachmentLocalsFR is the FeatureRecipe both attachment surfaces are driven
// with: one authored locals block under a given name.
func attachmentLocalsFR(name string, locals ...string) *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = name
	fr.Spec.Locals = omLocals(locals...)
	return fr
}

// attachmentLocalsWorld builds the render products the described stage's locals
// rules read: a graph carrying one node with one attachment and one boundary
// carrying one boundary-scope attachment, the Release document the boundary
// axis is derived from, and the binding that resolves each attachment to the
// FeatureRecipe under test.
//
// Both owners are present in one world rather than one each, so a rule reaching
// the wrong axis produces a visible cross-contamination — a boundary rule firing
// over the node-carried block, or the reverse — instead of silently finding
// nothing.
func attachmentLocalsWorld(
	nodeFR *types.FeatureRecipe, boundaryFR *types.FeatureRecipe,
) (*types.IRGraph, *resolver.ResolveResult, RecipeLookup) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set(attachmentLocalsNode, types.IRNode{})

	attachments := types.OrderedMap[types.IRFeatureEntry]{}
	attachments.Set(attachmentLocalsInstance, types.IRFeatureEntry{Type: attachmentLocalsType})
	perNode := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	perNode.Set(attachmentLocalsNode, attachments)

	boundaryAttachments := types.OrderedMap[types.IRBoundaryFeature]{}
	boundaryAttachments.Set(boundaryLocalsInstance, types.IRBoundaryFeature{Type: boundaryLocalsType})
	perBoundary := types.OrderedMap[types.OrderedMap[types.IRBoundaryFeature]]{}
	perBoundary.Set(boundaryLocalsPath, boundaryAttachments)

	graph := &types.IRGraph{Spec: types.IRSpec{
		Nodes:                nodes,
		FeatureValuesMapping: perNode,
		BoundaryFeatures:     perBoundary,
	}}

	features := types.OrderedMap[types.BoundaryFeature]{}
	features.Set(boundaryLocalsInstance, types.BoundaryFeature{Type: boundaryLocalsType})
	boundaries := types.OrderedMap[types.Boundary]{}
	boundaries.Set(boundaryLocalsPath, types.Boundary{Features: features})
	release := &types.Release{}
	release.Spec.Boundaries = boundaries

	lookup := RecipeLookup{
		FeatureRecipe: func(instanceName, _ string) (*types.FeatureRecipe, bool) {
			switch instanceName {
			case attachmentLocalsInstance:
				return nodeFR, nodeFR != nil
			case boundaryLocalsInstance:
				return boundaryFR, boundaryFR != nil
			default:
				return nil, false
			}
		},
	}
	return graph, &resolver.ResolveResult{Release: release, ReleasePath: "release.yaml"}, lookup
}

// declaredAttachmentLocalsHalt drives every locals rule the described stage
// declares through the production invocation site over one authored world, and
// returns the halt.
//
// The calibration is taken before the verdict is read, on both axes: a walk that
// reached no subject raises nothing and reads exactly like a walk that ran and
// approved, so the assertion that each axis presents the locals its FeatureRecipe
// declares is what makes the verdict mean anything at all.
func declaredAttachmentLocalsHalt(
	t *testing.T, nodeFR *types.FeatureRecipe, boundaryFR *types.FeatureRecipe,
) error {
	t.Helper()
	graph, resolved, lookup := attachmentLocalsWorld(nodeFR, boundaryFR)
	recipes := map[string]*types.Recipe{}

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		recipes, lookup, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built; there is nothing for the rules to read")
	}
	axes := newDescribedStageCoordinates(
		graph,
		localsAxis{
			nodes:       []string{attachmentLocalsNode},
			attachments: []string{attachmentLocalsInstance},
		},
		recipes, lookup, resolved,
	)
	if axes == nil {
		t.Fatal("the enumeration refused to build over a graph and a bound FeatureRecipe")
	}

	// The calibration walks the axes the TRAVERSAL walks, outer coordinate first,
	// and not the inner accessor alone. An inner accessor answering correctly for a
	// coordinate the outer axis never yields produces no subject at all, and a
	// walk over no subject raises nothing — which reads exactly like a walk that
	// ran and approved.
	if nodeFR != nil {
		attached := axes.Attachments(attachmentLocalsNode)
		if len(attached) == 0 {
			t.Fatal("the node presents no attachment, so no attachment-borne subject exists to judge")
		}
		total := 0
		for _, attachment := range attached {
			total += len(axes.AttachmentLocalsKeys(attachmentLocalsNode, attachment))
		}
		if want := nodeFR.Spec.Locals.Len(); total != want {
			t.Fatalf("the attachment axis presents %d local(s) for a FeatureRecipe declaring %d; "+
				"the verdict below would certify nothing", total, want)
		}
	}
	if boundaryFR != nil {
		scopes := axes.Boundaries()
		if len(scopes) != 1 {
			t.Fatalf("the boundary axis presents %d boundary(s); want exactly the one the Release declares",
				len(scopes))
		}
		total := 0
		for _, scope := range scopes {
			for _, attachment := range axes.BoundaryAttachments(scope.Value) {
				total += len(axes.BoundaryAttachmentLocalsKeys(scope.Value, attachment))
			}
		}
		if want := boundaryFR.Spec.Locals.Len(); total != want {
			t.Fatalf("the boundary-attachment axis presents %d local(s) for a FeatureRecipe declaring %d; "+
				"the verdict below would certify nothing", total, want)
		}
	}

	return runAuthoredLocalsRule(context.Background(), view, axes)
}

// legalAttachmentLocals is a block every rule admits: one entry reading a sibling
// declared ABOVE it, which is the whole of what author order permits.
func legalAttachmentLocals() []string {
	return []string{"base", "svc", "derived", "{{ .locals.base }}.internal"}
}

// TestTheAttachmentLocalsRulesAreReachedAndTheirObjectionsHalt is the invocation
// proof for the node-carried owner: one planted fault per fixture, driven
// through the production site, asserted against the halt's own condition.
//
// # One fault per fixture, and the identity is the assertion
//
// Each row plants exactly ONE offending entry and names the condition that entry
// carries. Asserting "an error" would keep passing once some other gate rejected
// the fixture first, and the two conditions have separate remedies — an ordering
// fault is fixed by moving a definition, a naming fault by correcting a key — so a
// row that could not tell them apart would not be watching the distinction the two
// rules exist to draw.
//
// # The control is what makes a halt attributable
//
// The admitted row drives the same world with a legal block. Without it, a halt on
// the offending rows could equally be a fixture that refuses everything.
func TestTheAttachmentLocalsRulesAreReachedAndTheirObjectionsHalt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		locals []string
		want   error
		// detail is a phrase the halt has to carry, so a rule reporting the right
		// condition about the wrong key still fails.
		detail string
	}{
		{
			name:   "a forward reference is the ordering condition",
			locals: []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			want:   ErrLocalsForwardReference(),
			detail: "base",
		},
		{
			name:   "an entry reading itself is the same condition",
			locals: []string{"loop", "{{ .locals.loop }}-x"},
			want:   ErrLocalsForwardReference(),
			detail: "loop",
		},
		{
			name:   "a key the block never declares is the naming condition",
			locals: []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			want:   ErrLocalsUnknownReference(),
			detail: "bass",
		},
		{
			// The class this owner reaches that the node's own block cannot: a
			// base Recipe's locals are merged over an attachment's AFTER that block
			// resolves, so a name belonging to the node's block does not exist at
			// the point an attachment local runs, and the finding says so rather
			// than suggesting a reorder.
			name:   "a base-Recipe key is undeclared on this block",
			locals: []string{"derived", "{{ .locals.node_only }}-x"},
			want:   ErrLocalsUnknownReference(),
			detail: "node_only",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := attachmentLocalsFR(attachmentLocalsRecipeName, tc.locals...)
			halt := declaredAttachmentLocalsHalt(t, fr, nil)
			if halt == nil {
				t.Fatalf("the rules admitted a block carrying a planted %v", tc.want)
			}
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			// The other condition must NOT be reported: the two are separate
			// identities with separate remedies, and one entry carries at most one.
			other := ErrLocalsUnknownReference()
			if errors.Is(halt, ErrLocalsUnknownReference()) && !errors.Is(tc.want, ErrLocalsUnknownReference()) {
				t.Errorf("the halt also carries %v, which this entry does not commit", other)
			}
			if !strings.Contains(halt.Error(), tc.detail) {
				t.Errorf("the halt does not name %q, so it reports the right condition about an unnamed key: %v",
					tc.detail, halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}

	// The control: the same world with a legal block is admitted, so the refusals
	// above are attributable to the planted fault and not to the fixture.
	t.Run("a legal block is admitted", func(t *testing.T) {
		t.Parallel()
		fr := attachmentLocalsFR(attachmentLocalsRecipeName, legalAttachmentLocals()...)
		if halt := declaredAttachmentLocalsHalt(t, fr, nil); halt != nil {
			t.Fatalf("the rules refused a block whose only reference reads a sibling above it: %v", halt)
		}
	})
}

// TestTheBoundaryAttachmentLocalsRulesAreReachedAndTheirObjectionsHalt is the same
// invocation proof for the boundary-rooted owner.
//
// The world it is driven with holds a node and a node-carried attachment as well,
// and the boundary FeatureRecipe is the only one carrying a fault — so a rule
// reaching the wrong axis reports nothing here rather than passing by accident.
func TestTheBoundaryAttachmentLocalsRulesAreReachedAndTheirObjectionsHalt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		locals []string
		want   error
		detail string
	}{
		{
			name:   "a forward reference is the ordering condition",
			locals: []string{"endpoint", "{{ .locals.host }}:5432", "host", "db"},
			want:   ErrLocalsForwardReference(),
			detail: "host",
		},
		{
			name:   "a key the block never declares is the naming condition",
			locals: []string{"host", "db", "endpoint", "{{ .locals.hosts }}:5432"},
			want:   ErrLocalsUnknownReference(),
			detail: "hosts",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			boundaryFR := attachmentLocalsFR(boundaryLocalsRecipeName, tc.locals...)
			// The node-carried attachment carries a legal block, so anything raised
			// here is the boundary axis's own finding.
			nodeFR := attachmentLocalsFR(attachmentLocalsRecipeName, legalAttachmentLocals()...)
			halt := declaredAttachmentLocalsHalt(t, nodeFR, boundaryFR)
			if halt == nil {
				t.Fatalf("the rules admitted a block carrying a planted %v", tc.want)
			}
			if !errors.Is(halt, tc.want) {
				t.Fatalf("the halt does not carry %v: %v", tc.want, halt)
			}
			if !strings.Contains(halt.Error(), tc.detail) {
				t.Errorf("the halt does not name %q: %v", tc.detail, halt)
			}
			if !strings.Contains(halt.Error(), boundaryLocalsRecipeName) {
				t.Errorf("the halt is not positioned at the FeatureRecipe the block was authored in "+
					"(%q): %v", boundaryLocalsRecipeName, halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}

	t.Run("a legal block is admitted", func(t *testing.T) {
		t.Parallel()
		boundaryFR := attachmentLocalsFR(boundaryLocalsRecipeName, "host", "db", "endpoint", "{{ .locals.host }}:5432")
		nodeFR := attachmentLocalsFR(attachmentLocalsRecipeName, legalAttachmentLocals()...)
		if halt := declaredAttachmentLocalsHalt(t, nodeFR, boundaryFR); halt != nil {
			t.Fatalf("the rules refused a block whose only reference reads a sibling above it: %v", halt)
		}
	})
}

// TestTheAttachmentLocalsRulesDecideWhatTheEvaluationDecides is the agreement
// measurement for the node-carried owner, and it is what licenses declaring the
// rule ahead of the evaluation rather than replacing it.
//
// The evaluation side is [EvaluateFeatureRecipeLocals], the surface that resolves
// an attachment's own block for rendered output. The declared side is the
// production invocation site. Each row carries at most one offending entry, so the
// evaluation's first-offender reading and the declared rules' whole-set reading
// name the same condition.
func TestTheAttachmentLocalsRulesDecideWhatTheEvaluationDecides(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		locals []string
	}{
		{"a forward reference", []string{"derived", "{{ .locals.base }}.internal", "base", "svc"}},
		{"a self reference", []string{"loop", "{{ .locals.loop }}-x"}},
		{"an undeclared key", []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"}},
		{"a backward reference is legal", legalAttachmentLocals()},
		{"a chain of three in author order resolves", []string{
			"stack", "edge",
			"svc", "{{ .locals.stack }}-gateway",
			"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
		}},
		{"the same chain reversed", []string{
			"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
			"svc", "{{ .locals.stack }}-gateway",
			"stack", "edge",
		}},
		{"a bare pillar reference is admitted", []string{"guard", "{{ if .locals }}yes{{ end }}"}},
		{"the pillar's name inside a string literal is not a reference",
			[]string{"k", `{{ printf "%s" ".locals.nope" }}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, evaluated := EvaluateFeatureRecipeLocals(
				attachmentLocalsFR(attachmentLocalsRecipeName, tc.locals...),
				attachmentLocalsInstance, attachmentLocalsNode, "",
				types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})

			declared := declaredAttachmentLocalsHalt(
				t, attachmentLocalsFR(attachmentLocalsRecipeName, tc.locals...), nil,
			)

			evaluatedClass := localsFaultClass(evaluated)
			declaredClass := localsFaultClass(declared)
			if evaluatedClass != declaredClass {
				t.Errorf("the two paths disagree: evaluation=%s declared=%s\n  evaluation: %v\n  declared:   %v",
					evaluatedClass, declaredClass, evaluated, declared)
			}
			t.Logf("CELL %-45s | evaluation=%-9s declared=%-9s", tc.name, evaluatedClass, declaredClass)
		})
	}
}

// TestTheAttachmentLocalsOwnersAreSeparateAxes proves the two new owners are
// reached independently rather than one standing in for the other.
//
// It is the assertion the coordinate exists for: a fault authored on one owner's
// FeatureRecipe must be reported when that owner is walked and must be invisible
// when only the other one is. A single widened axis would report both from either
// side, and every declaration assertion would stay green.
func TestTheAttachmentLocalsOwnersAreSeparateAxes(t *testing.T) {
	t.Parallel()

	offending := []string{"derived", "{{ .locals.base }}.internal", "base", "svc"}
	legal := legalAttachmentLocals()

	// The fault on the node-carried block only.
	nodeOnly := declaredAttachmentLocalsHalt(t,
		attachmentLocalsFR(attachmentLocalsRecipeName, offending...),
		attachmentLocalsFR(boundaryLocalsRecipeName, legal...),
	)
	if nodeOnly == nil || !errors.Is(nodeOnly, ErrLocalsForwardReference()) {
		t.Fatalf("the node-carried fault was not reported: %v", nodeOnly)
	}
	if strings.Contains(nodeOnly.Error(), boundaryLocalsRecipeName) {
		t.Errorf("the node-carried finding names the boundary attachment's FeatureRecipe: %v", nodeOnly)
	}

	// The fault on the boundary-rooted block only.
	boundaryOnly := declaredAttachmentLocalsHalt(t,
		attachmentLocalsFR(attachmentLocalsRecipeName, legal...),
		attachmentLocalsFR(boundaryLocalsRecipeName, offending...),
	)
	if boundaryOnly == nil || !errors.Is(boundaryOnly, ErrLocalsForwardReference()) {
		t.Fatalf("the boundary-rooted fault was not reported: %v", boundaryOnly)
	}
	if strings.Contains(boundaryOnly.Error(), attachmentLocalsRecipeName) {
		t.Errorf("the boundary-rooted finding names the node attachment's FeatureRecipe: %v", boundaryOnly)
	}

	// Both at once: two findings, each positioned at its own document. The
	// evaluation-time path beneath aborts at its first offender, so reporting both
	// is the breadth the declaration adds.
	both := declaredAttachmentLocalsHalt(t,
		attachmentLocalsFR(attachmentLocalsRecipeName, offending...),
		attachmentLocalsFR(boundaryLocalsRecipeName, offending...),
	)
	if both == nil {
		t.Fatal("two planted faults produced no halt at all")
	}
	leaves := haltLeafMessages(both)
	if len(leaves) != 2 {
		t.Fatalf("two planted faults on two owners produced %d diagnostic(s); want one per owner:\n%v",
			len(leaves), leaves)
	}
	joined := strings.Join(leaves, "\n")
	for _, want := range []string{attachmentLocalsRecipeName, boundaryLocalsRecipeName} {
		if !strings.Contains(joined, want) {
			t.Errorf("the pair of diagnostics does not name %q:\n%s", want, joined)
		}
	}
	t.Logf("both owners reported:\n%s", joined)
}

// TestTheAttachmentLocalsRulesReadTheFactsTheRenderSupplies proves the two facts
// each owner's rules read are answered by the presentation the render builds,
// over the FeatureRecipe the render bound — so a rule invoked over the axis cannot
// be asked about an entry whose text nothing would supply.
//
// It also pins the bound the attachment fact carries: the answer is the
// attachment's OWN block. A presentation folding the node's resolved locals in
// would report a reference as legitimate that the evaluation refuses.
func TestTheAttachmentLocalsRulesReadTheFactsTheRenderSupplies(t *testing.T) {
	t.Parallel()

	nodeFR := attachmentLocalsFR(attachmentLocalsRecipeName, "base", "svc", "derived", "{{ .locals.base }}")
	boundaryFR := attachmentLocalsFR(boundaryLocalsRecipeName, "host", "db")
	graph, _, lookup := attachmentLocalsWorld(nodeFR, boundaryFR)

	view := newDescribedView(
		context.Background(), graph, stubGraphFacts{},
		map[string]*types.Recipe{}, lookup, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil, nil, nil,
	)
	if view == nil {
		t.Fatal("the view was not built")
	}

	keys := view.AttachmentDeclaredLocalsKeys(attachmentLocalsNode, attachmentLocalsInstance)
	if got, want := strings.Join(keys, ","), strings.Join(nodeFR.Spec.Locals.Keys(), ","); got != want {
		t.Errorf("the attachment key order is %q; want the FeatureRecipe's own author order %q", got, want)
	}
	for _, key := range keys {
		want, _ := nodeFR.Spec.Locals.Get(key)
		if got := view.AttachmentAuthoredLocals(attachmentLocalsNode, attachmentLocalsInstance, key); got != want {
			t.Errorf("the attachment text for %q is %q; want %q", key, got, want)
		}
	}

	bKeys := view.BoundaryAttachmentDeclaredLocalsKeys(boundaryLocalsPath, boundaryLocalsInstance)
	if got, want := strings.Join(bKeys, ","), strings.Join(boundaryFR.Spec.Locals.Keys(), ","); got != want {
		t.Errorf("the boundary-attachment key order is %q; want %q", got, want)
	}
	for _, key := range bKeys {
		want, _ := boundaryFR.Spec.Locals.Get(key)
		got := view.BoundaryAttachmentAuthoredLocals(boundaryLocalsPath, boundaryLocalsInstance, key)
		if got != want {
			t.Errorf("the boundary-attachment text for %q is %q; want %q", key, got, want)
		}
	}

	// An attachment nothing bound, and a key the block does not declare: both
	// answer empty rather than answering for some other document.
	if got := view.AttachmentDeclaredLocalsKeys(attachmentLocalsNode, "no-such-attachment"); got != nil {
		t.Errorf("an unbound attachment answers %v; want no keys", got)
	}
	if got := view.AttachmentAuthoredLocals(attachmentLocalsNode, attachmentLocalsInstance, "absent"); got != "" {
		t.Errorf("a key the block does not declare answers %q; want empty", got)
	}
	if got := view.BoundaryAttachmentDeclaredLocalsKeys("no-such-boundary", boundaryLocalsInstance); got != nil {
		t.Errorf("an unknown boundary answers %v; want no keys", got)
	}

	// The two owners do not answer for each other: the boundary attachment's
	// block is not reachable through the node-carried fact, and the reverse.
	if got := view.AttachmentDeclaredLocalsKeys(attachmentLocalsNode, boundaryLocalsInstance); len(got) != 0 {
		t.Errorf("the node-carried fact answers for the boundary attachment: %v", got)
	}
	if got := view.BoundaryAttachmentDeclaredLocalsKeys(boundaryLocalsPath, attachmentLocalsInstance); len(got) != 0 {
		t.Errorf("the boundary fact answers for the node-carried attachment: %v", got)
	}

	_ = constraint.DescribedView(view)
}
