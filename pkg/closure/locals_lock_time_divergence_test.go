// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Two paths resolve a Recipe's locals block, and they answer differently.
//
// The build's render refuses a reference a locals entry cannot legitimately read —
// a sibling declared below it, or a key the block never declares — before it
// evaluates anything. Source descent, which runs while a lockfile is acquired,
// evaluates the same block and applies a deliberate tolerance instead: an entry
// whose template cannot execute at lock time is SKIPPED and the walk continues,
// because such an entry ordinarily reads data that exists only at render time. Its
// gate is the source template's own evaluation, which fails naming the source field
// if `source:` needed a local that was skipped.
//
// # This file pins that arrangement so a change to it fails rather than passing
//
// The tolerance is documented and reasoned where it is implemented, and four
// shipped tests cover its own behaviour. What none of them state is the
// RELATIONSHIP between the two paths — and until this file, nothing did: a comment
// asserted that the closure iterates in author order and lets a later entry read an
// earlier one, which is true and is not the same claim. So the relationship was held
// by nothing, and a change on either side would have moved it silently.
//
// # The invariant is one-directional, and that is the part worth pinning
//
// Every difference runs the same way: the render HALTS where the closure ADMITS.
// No row has the closure halting where the render admits, and that direction is
// what makes the gap a later diagnostic rather than a wrong one — lock and pull
// write a pin that is correct for the expression they read, and a later build
// refuses the entry the pin never depended on. The reverse direction would be the
// serious defect: a lockfile refusing a document the compiler accepts.
//
// The table below is therefore not a wish. It records what the two paths do today,
// and the assertions after it are what a change has to contend with: the
// one-directional invariant, and a corpus that carries agreements and
// disagreements both, so no row can be satisfied by a path that stopped being
// asked.

// localsDivergenceVerdict is what one path did with an authored locals block,
// reduced to the comparison the two paths can both answer.
type localsDivergenceVerdict string

const (
	// localsAdmitted is a path that reported nothing about the block.
	localsAdmitted localsDivergenceVerdict = "admitted"
	// localsRenderOrdering and localsRenderNaming are the render's two refusals.
	localsRenderOrdering localsDivergenceVerdict = "render-refuses-ordering"
	localsRenderNaming   localsDivergenceVerdict = "render-refuses-naming"
	// localsLockTime is the closure's halt, which names the SOURCE field rather
	// than the locals entry: a different subject with a different remedy.
	localsLockTime localsDivergenceVerdict = "lock-time-source-unresolvable"
)

// localsDivergenceRow is one authored locals block plus the source expression it is
// paired with, and what each path does with the two together.
type localsDivergenceRow struct {
	name string
	// locals is flattened key/value pairs in author order.
	locals []string
	// sourceVersion is the `spec.source.version` expression. A literal keeps the
	// source out of the comparison; an expression reading a local brings the
	// closure's own gate into it.
	sourceVersion string

	wantRender  localsDivergenceVerdict
	wantClosure localsDivergenceVerdict
}

func localsDivergenceRows() []localsDivergenceRow {
	return []localsDivergenceRow{
		{
			// The plain forward reference. The render refuses it; the closure skips
			// the entry that could not execute and resolves its literal source.
			name:          "forward reference, literal source",
			locals:        []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			sourceVersion: "1.0.0",
			wantRender:    localsRenderOrdering,
			wantClosure:   localsAdmitted,
		},
		{
			name:          "unknown reference, literal source",
			locals:        []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			sourceVersion: "1.0.0",
			wantRender:    localsRenderNaming,
			wantClosure:   localsAdmitted,
		},
		{
			name:          "self reference, literal source",
			locals:        []string{"loop", "{{ .locals.loop }}-x"},
			sourceVersion: "1.0.0",
			wantRender:    localsRenderOrdering,
			wantClosure:   localsAdmitted,
		},
		{
			name: "chain of three reversed, literal source",
			locals: []string{
				"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
				"svc", "{{ .locals.stack }}-gateway",
				"stack", "edge",
			},
			sourceVersion: "1.0.0",
			wantRender:    localsRenderOrdering,
			wantClosure:   localsAdmitted,
		},
		{
			// The agreements. Without them the invariant below would hold over a
			// corpus in which the closure never admits anything at all.
			name:          "backward reference is legal on both paths",
			locals:        []string{"base", "svc", "derived", "{{ .locals.base }}.internal"},
			sourceVersion: "1.0.0",
			wantRender:    localsAdmitted,
			wantClosure:   localsAdmitted,
		},
		{
			name: "chain of three in author order is legal on both paths",
			locals: []string{
				"stack", "edge",
				"svc", "{{ .locals.stack }}-gateway",
				"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
			},
			sourceVersion: "1.0.0",
			wantRender:    localsAdmitted,
			wantClosure:   localsAdmitted,
		},
		{
			// And the rows where the closure's own gate fires: the source reads the
			// entry the render refuses, so the entry the closure skipped is one
			// `source:` needed. Both paths stop the user, under different conditions
			// naming different subjects — which is why the verdicts are distinct
			// values rather than a shared "halts".
			name:          "forward reference the source itself reads",
			locals:        []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			sourceVersion: "{{ .locals.derived }}",
			wantRender:    localsRenderOrdering,
			wantClosure:   localsLockTime,
		},
		{
			name:          "unknown reference the source itself reads",
			locals:        []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			sourceVersion: "{{ .locals.derived }}",
			wantRender:    localsRenderNaming,
			wantClosure:   localsLockTime,
		},
	}
}

// renderVerdict drives the build's own locals evaluation over one authored block.
func renderVerdict(t *testing.T, locals []string) localsDivergenceVerdict {
	t.Helper()
	r := &types.Recipe{}
	r.Metadata.Name = "divergence-recipe"
	r.Spec.Locals = orderedLocals(locals...)
	_, err := renderer.EvaluateRecipeLocals(r, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{})
	switch {
	case err == nil:
		return localsAdmitted
	case errors.Is(err, renderer.ErrLocalsForwardReference()):
		return localsRenderOrdering
	case errors.Is(err, renderer.ErrLocalsUnknownReference()):
		return localsRenderNaming
	default:
		t.Fatalf("the render halted on a condition this comparison does not model: %v", err)
		return ""
	}
}

// closureVerdict drives source descent over the same authored block, through the
// exported entry the pull path reaches it by rather than through any internal
// helper — so what is pinned is the behaviour a lockfile acquisition actually has.
func closureVerdict(t *testing.T, locals []string, sourceVersion string) localsDivergenceVerdict {
	t.Helper()
	const uri = "divergence-recipe:1.0.0"
	r := makeRecipe(uri, helmSource(sourceVersion))
	r.Spec.Locals = orderedLocals(locals...)
	loader := &fakeRecipeLoader{recipes: map[string]*types.Recipe{uri: r}}
	cl := closure.Closure{Entities: []closure.ResolvedRef{{URI: uri, KindHint: "recipe"}}}

	_, err := closure.DescendRecipeSources(
		usedFromClosure(cl), cl, loader, &fakeSourceResolver{}, "/ws", ".aggrega/cache",
	)
	switch {
	case err == nil:
		return localsAdmitted
	case errors.Is(err, closure.ErrSourceTemplateAtLockTime()):
		return localsLockTime
	default:
		t.Fatalf("source descent halted on a condition this comparison does not model: %v", err)
		return ""
	}
}

// orderedLocals builds an authored locals block from flattened key/value pairs,
// keeping the order they are written in — which is the whole subject of the
// comparison, so it may not be sorted.
func orderedLocals(pairs ...string) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i], pairs[i+1])
	}
	return out
}

// TestTheLockTimeLocalsToleranceDivergesFromTheBuildInOneDirectionOnly is the pin.
//
// It fails in three separate ways, and each is a change worth stopping on: a row
// whose recorded pair moves, the one-directional invariant breaking, and a corpus
// that stopped carrying both agreements and disagreements.
func TestTheLockTimeLocalsToleranceDivergesFromTheBuildInOneDirectionOnly(t *testing.T) {
	t.Parallel()

	var agreements, disagreements int
	var closureAdmissions int
	for _, row := range localsDivergenceRows() {
		gotRender := renderVerdict(t, row.locals)
		gotClosure := closureVerdict(t, row.locals, row.sourceVersion)

		if gotRender != row.wantRender {
			t.Errorf("%s: the render reported %q, the pin records %q", row.name, gotRender, row.wantRender)
		}
		if gotClosure != row.wantClosure {
			t.Errorf("%s: source descent reported %q, the pin records %q", row.name, gotClosure, row.wantClosure)
		}

		// THE INVARIANT. A row where source descent refuses a block the render
		// admits is the direction that would be a defect: a lockfile refusing a
		// document the compiler accepts.
		if gotRender == localsAdmitted && gotClosure != localsAdmitted {
			t.Errorf("%s: source descent refused %q what the render ADMITS — the divergence has reversed direction, "+
				"which means lock time now refuses a document a build accepts", row.name, gotClosure)
		}

		if gotRender == gotClosure {
			agreements++
		} else {
			disagreements++
		}
		if gotClosure == localsAdmitted {
			closureAdmissions++
		}
		t.Logf("ROW %-46s | render=%-24s closure=%s", row.name, gotRender, gotClosure)
	}

	// The corpus has to carry both, or the invariant above is satisfied by a table
	// nothing exercises: a corpus where the closure never admits cannot show the
	// permissive direction, and one where it always admits cannot show its own gate.
	if agreements == 0 {
		t.Error("no row has the two paths agreeing; the comparison cannot show that agreement is possible")
	}
	if disagreements == 0 {
		t.Error("no row has the two paths differing; the divergence this pins is not exercised")
	}
	if closureAdmissions == 0 {
		t.Error("source descent refused every row; the tolerance this pins is not exercised")
	}
	t.Logf("%d row(s) agree, %d differ, source descent admitted %d", agreements, disagreements, closureAdmissions)
}

// TestTheLockTimePinIsCalibrated is the needle under the pin: the comparison must
// be able to report a difference at all.
//
// The two paths are asked about a block the render refuses and the closure admits,
// and the verdicts must not be equal. Without this, a pin whose two sides had
// collapsed into one reading would pass every row.
func TestTheLockTimePinIsCalibrated(t *testing.T) {
	t.Parallel()
	locals := []string{"derived", "{{ .locals.base }}.internal", "base", "svc"}
	gotRender := renderVerdict(t, locals)
	gotClosure := closureVerdict(t, locals, "1.0.0")
	if gotRender == gotClosure {
		t.Fatalf("both paths reported %q over an input the two are known to differ on; "+
			"the comparison cannot report a difference", gotRender)
	}
	t.Logf("calibration: render=%q closure=%q -> DIFFER as expected", gotRender, gotClosure)
}
