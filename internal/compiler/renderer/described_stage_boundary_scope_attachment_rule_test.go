// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The proofs in this file cover the rule declared over whether an attachment
// binding a FeatureRecipe that generates once for a boundary has a boundary to
// generate for.
//
// Every proof drives the PRODUCTION invocation site over the real presentation,
// and each asserts against the halt's own identity rather than against "an
// error". A rule reached by nothing raises nothing, which is indistinguishable
// from a rule that ran and approved — so each table calibrates the subject count
// before any verdict is read, and each refusal is paired with an admission over
// the same world so the halt is attributable to the planted fault.
//
// The ancestor rows are the discriminating ones. An attachment written at a
// boundary appears in the merged set of every component that boundary holds,
// including the components of the scopes nested inside it, so the ORDINARY shape
// of a boundary attachment is a component carrying it. A rule phrased "a
// component may not carry a boundary-scope attachment" refuses that shape, which
// builds today and renders byte-identically to the boundary-only form. Only a row
// that declares the attachment at a strict ancestor can tell the two rules apart.

const (
	boundaryScopeNode       = "api"
	boundaryScopeInstance   = "bridge"
	boundaryScopeType       = "core/bridge:1.0.0"
	boundaryScopeRecipeName = "bridge-fr"
)

// boundaryScopeGraphFacts answers the two facts this rule reads and nothing
// else.
//
// The embedded view carries no value, so a read of any other fact panics rather
// than answering a zero — which is what keeps this fixture a statement about
// which facts the rule consults. A rule that started reading a third fact would
// fail here loudly instead of being served a plausible empty value.
type boundaryScopeGraphFacts struct {
	constraint.GraphView

	// placement is the boundary the node was placed in, empty for a node the
	// deployment placed in no boundary at all.
	placement string

	// declaring is the set of boundary paths that declare an attachment under
	// the instance name, spelled exactly as the placement path's segments join.
	declaring map[string]bool
}

func (f boundaryScopeGraphFacts) NodeBoundaryPlacement(_ string) (string, bool) {
	return f.placement, f.placement != ""
}

func (f boundaryScopeGraphFacts) BoundaryAttachmentDeclaration(
	boundary string, attachment string,
) (constraint.AttachmentDeclaration, bool) {
	if attachment != boundaryScopeInstance || !f.declaring[boundary] {
		return constraint.AttachmentDeclaration{}, false
	}
	return constraint.AttachmentDeclaration{FeatureType: boundaryScopeType}, true
}

// boundaryScopeFR is the FeatureRecipe the attachment binds, under the execution
// scope the caller states.
func boundaryScopeFR(executionScope string) *types.FeatureRecipe {
	fr := &types.FeatureRecipe{}
	fr.Metadata.Name = boundaryScopeRecipeName
	fr.Spec.ExecutionScope = executionScope
	return fr
}

// boundaryScopeAttachmentHalt drives the rule through its production invocation
// site over one authored world, and returns the halt.
//
// The calibration is taken before the verdict is read, and it walks the axes the
// TRAVERSAL walks: an inner accessor answering correctly for a coordinate the
// outer axis never yields produces no subject, and a walk over no subject raises
// nothing — which reads exactly like a walk that ran and approved.
func boundaryScopeAttachmentHalt(
	t *testing.T, fr *types.FeatureRecipe, placement string, declaring ...string,
) error {
	t.Helper()

	nodeAttachments := types.OrderedMap[types.IRFeatureEntry]{}
	nodeAttachments.Set(boundaryScopeInstance, types.IRFeatureEntry{Type: boundaryScopeType})
	perNode := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	perNode.Set(boundaryScopeNode, nodeAttachments)
	graph := &types.IRGraph{Spec: types.IRSpec{FeatureValuesMapping: perNode}}

	lookup := RecipeLookup{
		FeatureRecipe: func(instanceName, _ string) (*types.FeatureRecipe, bool) {
			if instanceName == boundaryScopeInstance {
				return fr, fr != nil
			}
			return nil, false
		},
	}

	declares := map[string]bool{}
	for _, path := range declaring {
		declares[path] = true
	}
	facts := boundaryScopeGraphFacts{placement: placement, declaring: declares}

	view := newDescribedView(
		context.Background(), graph, facts,
		map[string]*types.Recipe{}, lookup, nil, newStepDescriptorMemo(), nil, nil,
		nil, nil,
	)
	if view == nil {
		t.Fatal("the presentation was not built; there is nothing for the rule to read")
	}

	axes := localsAxis{nodes: []string{boundaryScopeNode}, attachments: []string{boundaryScopeInstance}}
	subjects := 0
	for _, node := range axes.Nodes() {
		subjects += len(axes.Attachments(node.ID))
	}
	if subjects != 1 {
		t.Fatalf("the axes present %d attachment subject(s); want exactly the one the fixture carries — "+
			"the verdict below would certify nothing", subjects)
	}

	return runBoundaryScopeAttachmentRule(context.Background(), view, axes)
}

// TestTheBoundaryScopeAttachmentRuleIsReachedAndItsObjectionHalts is the
// invocation proof: one planted fault per fixture, driven through the production
// site, asserted against the halt's own identity and against the values it names.
func TestTheBoundaryScopeAttachmentRuleIsReachedAndItsObjectionHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		placement string
		declaring []string
	}{
		{
			name:      "no boundary declares the attachment at all",
			placement: "cluster/services-net",
		},
		{
			// A scope NESTED INSIDE the component's own is not an ancestor of it,
			// so a declaration there reaches nothing the component can generate
			// from. Without this row the walk could return true for any boundary
			// carrying the name and still pass every other row.
			name:      "only a scope nested inside the component's own declares it",
			placement: "cluster/services-net",
			declaring: []string{"cluster/services-net/inner"},
		},
		{
			// A sibling scope at the same depth is likewise not an ancestor.
			name:      "only a sibling scope declares it",
			placement: "cluster/services-net",
			declaring: []string{"cluster/data-net"},
		},
		{
			// A path the component's own path is a PREFIX OF is not an ancestor
			// either, which is what separates a segment walk from a string-prefix
			// test.
			name:      "only a scope whose path extends the component's declares it",
			placement: "cluster",
			declaring: []string{"cluster/services-net"},
		},
		{
			name:      "the deployment placed the component in no boundary",
			placement: "",
			declaring: []string{"cluster"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := boundaryScopeAttachmentHalt(
				t, boundaryScopeFR(executionScopeBoundary), tc.placement, tc.declaring...,
			)
			if halt == nil {
				t.Fatalf("the rule admitted an attachment that generates once for a boundary with no "+
					"boundary declaring it (placement %q, declared at %v)", tc.placement, tc.declaring)
			}
			if !errors.Is(halt, ErrBoundaryScopeAttachmentNotDeclaredAtABoundary()) {
				t.Fatalf("the halt does not carry %v: %v",
					ErrBoundaryScopeAttachmentNotDeclaredAtABoundary(), halt)
			}
			if !strings.Contains(halt.Error(), boundaryScopeInstance) {
				t.Errorf("the halt does not name the attachment %q, so an author cannot tell which one "+
					"offends: %v", boundaryScopeInstance, halt)
			}
			if tc.placement != "" && !strings.Contains(halt.Error(), tc.placement) {
				t.Errorf("the halt does not name the boundary %q the component sits in, which is the "+
					"site the declaration is written at: %v", tc.placement, halt)
			}
			t.Logf("HALT %s -> %v", tc.name, halt)
		})
	}
}

// TestTheBoundaryScopeAttachmentRuleWalksTheAncestors is the no-verdict-move
// proof, and it is the row the refusals above cannot supply.
//
// An attachment written at a boundary reaches every component that boundary
// holds, so a component carrying one is the ordinary shape rather than a fault.
// Each row here places the declaration at a different ancestor of the component's
// own path — including the component's own scope, which is the boundary-only form
// — and asserts the rule admits it. A rule that refused a component-carried
// attachment outright would pass every row of the table above and fail every row
// here.
func TestTheBoundaryScopeAttachmentRuleWalksTheAncestors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		placement string
		declaring []string
	}{
		{
			name:      "the component's own scope declares it",
			placement: "cluster/services-net",
			declaring: []string{"cluster/services-net"},
		},
		{
			name:      "the enclosing scope declares it",
			placement: "cluster/services-net",
			declaring: []string{"cluster"},
		},
		{
			name:      "a scope three levels up declares it",
			placement: "a/b/c/d",
			declaring: []string{"a"},
		},
		{
			name:      "the component sits at the root scope, which declares it",
			placement: "cluster",
			declaring: []string{"cluster"},
		},
		{
			name:      "an unrelated scope declares it as well as an ancestor",
			placement: "cluster/services-net",
			declaring: []string{"cluster", "elsewhere"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if halt := boundaryScopeAttachmentHalt(
				t, boundaryScopeFR(executionScopeBoundary), tc.placement, tc.declaring...,
			); halt != nil {
				t.Fatalf("the rule refused an attachment a scope enclosing the component declares, "+
					"which builds today: %v", halt)
			}
		})
	}

	// The positive control for this table: the identical world with the
	// declaration removed IS refused, so the admissions above are the ancestor
	// walk and not a fixture that refuses nothing.
	t.Run("the same world with no declaring ancestor is refused", func(t *testing.T) {
		t.Parallel()
		halt := boundaryScopeAttachmentHalt(t, boundaryScopeFR(executionScopeBoundary), "cluster/services-net")
		if halt == nil {
			t.Fatal("the rule admitted an attachment no scope declares; the admissions above prove " +
				"nothing about the walk")
		}
		if !errors.Is(halt, ErrBoundaryScopeAttachmentNotDeclaredAtABoundary()) {
			t.Fatalf("the halt does not carry %v: %v",
				ErrBoundaryScopeAttachmentNotDeclaredAtABoundary(), halt)
		}
	})
}

// TestTheBoundaryScopeAttachmentRuleEndsOnAnAttachmentThatGeneratesPerComponent
// is the second no-verdict-move proof: the execution scope is read, not assumed.
//
// An attachment whose FeatureRecipe generates per component is written against a
// component as a matter of course and needs no boundary to declare it. Each row
// here drives the world the rule DOES refuse under the boundary scope and asserts
// it is admitted under the others.
func TestTheBoundaryScopeAttachmentRuleEndsOnAnAttachmentThatGeneratesPerComponent(t *testing.T) {
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
			if halt := boundaryScopeAttachmentHalt(t, boundaryScopeFR(tc.scope), "cluster/services-net"); halt != nil {
				t.Fatalf("the rule refused an attachment that generates per component, which needs no "+
					"boundary to declare it: %v", halt)
			}
		})
	}

	// An attachment binding no FeatureRecipe at all reports no scope, which is
	// the per-component reading — so it is admitted here rather than refused for
	// a scope its author never declared.
	t.Run("an attachment binding no FeatureRecipe", func(t *testing.T) {
		t.Parallel()
		if halt := boundaryScopeAttachmentHalt(t, nil, "cluster/services-net"); halt != nil {
			t.Fatalf("the rule refused an attachment whose FeatureRecipe the build never bound, "+
				"naming a scope nobody declared: %v", halt)
		}
	})

	// The positive control: the identical world under the boundary scope IS
	// refused, so the admissions above are the scope reading.
	t.Run("the same world under the boundary scope is refused", func(t *testing.T) {
		t.Parallel()
		if halt := boundaryScopeAttachmentHalt(
			t, boundaryScopeFR(executionScopeBoundary), "cluster/services-net",
		); halt == nil {
			t.Fatal("the rule admitted a boundary-scope attachment with no declaring scope; the " +
				"admissions above prove nothing about the scope reading")
		}
	})
}
