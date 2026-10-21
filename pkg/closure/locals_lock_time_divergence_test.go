// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Two passes resolve a Recipe's locals block, and on the REFERENCE-ORDER axis
// they answer identically over every entry a `source:` coordinate depends on.
//
// The build's render refuses a reference a locals entry cannot legitimately read
// — a sibling declared below it, or a key the block never declares — before it
// evaluates anything, for every entry of the block. Source descent, which
// resolves the block so a `source:` coordinate can read it, evaluates only the
// entries a source field reads, directly or through another entry, and refuses
// the same references in those entries, under the same classification, over the
// same author-order prefix, reporting the same two conditions. An entry no source
// field reads is not looked at by source descent at all: its faults are the
// render's to refuse, at build. And its tolerance is still there and still real,
// bounded to what it exists for: an entry reading data no source coordinate can
// see — a render-time-only `.context.instance_id` consumed by a values mapping —
// is skipped there and the walk continues.
//
// 🔴 Which of the two runs is a question about the SURFACE being resolved, not
// about the phase. Source descent's body is reached from the lock-time entry
// point and from the build-time one alike, so a difference recorded here is a
// difference between two evaluators — never between acquiring a lockfile and
// building against it. The lock-versus-build reading is the easy mistake, and it
// is wrong in a way that matters: it invites the conclusion that a build accepts
// whatever a lockfile acquisition refuses, which is not so.
//
// # What this file pins
//
// It pins the AGREEMENT over the entries a coordinate reads, and it is worth as
// much as a divergence — arguably more, because an agreement between two
// implementations is exactly the property that decays silently. Nothing
// structural forces source descent to keep classifying references: its tolerance
// would swallow the fault the moment the classification stopped being consulted,
// and no compile error and no other test would say so. The per-row pairs below
// are what would say so.
//
// The agreement is stronger than "both refuse". Both refuse under ONE registered
// condition value: the assertions below match source descent's halt against the
// render package's own accessor, so a second condition minted for one of the two
// surfaces fails here rather than passing as a matching pair of look-alikes.
//
// It also pins the REACH: a row whose source reads no entry must be admitted by
// source descent whatever the block holds, so a pass that went back to
// evaluating every entry fails here rather than quietly halting pulls over
// entries no coordinate depends on.
//
// # Two divergences remain on this axis
//
// The first is the tolerance itself. A locals entry that only a rendered artifact
// can resolve is admitted by the render's pass and skipped by source descent — and
// where `source:` needed that entry, source descent halts naming the SOURCE field.
// The second is the reach: a fault in an entry no source field reads is refused by
// the render and not looked at by source descent. Keeping both in the corpus is
// what stops the agreement below from being satisfied by a table in which nothing
// could have differed.
//
// A second axis diverges the other way and the file beside this one pins it:
// source descent registers a smaller template-function set, so an entry the source
// reads calling a function only the render registers fails to PARSE there, and a
// parse failure is outside the tolerance. On that axis source descent halts where
// the render admits.

// localsDivergenceVerdict is what one path did with an authored locals block,
// reduced to the comparison the two paths can both answer.
type localsDivergenceVerdict string

const (
	// localsAdmitted is a path that reported nothing about the block.
	localsAdmitted localsDivergenceVerdict = "admitted"
	// localsRefusedOrdering and localsRefusedNaming are the two reference faults.
	// They are named for the FAULT rather than for a surface because both surfaces
	// report them, under one registered condition each — which is the property the
	// assertions below exist to hold.
	localsRefusedOrdering localsDivergenceVerdict = "refuses-ordering"
	localsRefusedNaming   localsDivergenceVerdict = "refuses-naming"
	// localsLockTime is source descent's own gate, which names the SOURCE field
	// rather than the locals entry: a different subject with a different remedy.
	localsLockTime localsDivergenceVerdict = "source-coordinate-unresolvable"
)

// localsDivergenceRow is one authored locals block plus the source expression it is
// paired with, and what each path does with the two together.
type localsDivergenceRow struct {
	name string
	// locals is flattened key/value pairs in author order.
	locals []string
	// sourceVersion is the `spec.source.version` expression. A literal reads no
	// entry, so source descent evaluates none; an expression reading a local
	// brings that entry, the entries it reads, and source descent's own gate into
	// the comparison.
	sourceVersion string
	// sourceReadsTheBlock says whether sourceVersion reads any entry, which decides
	// which of the two assertions below a row is held to.
	sourceReadsTheBlock bool

	wantRender  localsDivergenceVerdict
	wantClosure localsDivergenceVerdict
}

func localsDivergenceRows() []localsDivergenceRow {
	return []localsDivergenceRow{
		{
			// The reach. Each of these blocks carries a reference fault, and the
			// literal source reads none of its entries, so source descent does not
			// look and the render is where the fault is refused.
			name:          "forward reference, literal source",
			locals:        []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			sourceVersion: "1.0.0",
			wantRender:    localsRefusedOrdering,
			wantClosure:   localsAdmitted,
		},
		{
			name:          "unknown reference, literal source",
			locals:        []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			sourceVersion: "1.0.0",
			wantRender:    localsRefusedNaming,
			wantClosure:   localsAdmitted,
		},
		{
			name:          "self reference, literal source",
			locals:        []string{"loop", "{{ .locals.loop }}-x"},
			sourceVersion: "1.0.0",
			wantRender:    localsRefusedOrdering,
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
			wantRender:    localsRefusedOrdering,
			wantClosure:   localsAdmitted,
		},
		{
			// The agreements on legal input. Without them the assertions below would
			// hold over a corpus in which neither path admits anything at all.
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
			// The agreements on a fault: wherever the source reads the offending
			// entry, both paths refuse it, under one condition, and the refusal is
			// the reference fault rather than source descent's own gate.
			name:                "forward reference the source itself reads",
			locals:              []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			sourceVersion:       "{{ .locals.derived }}",
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedOrdering,
			wantClosure:         localsRefusedOrdering,
		},
		{
			name:                "unknown reference the source itself reads",
			locals:              []string{"base", "svc", "derived", "{{ .locals.bass }}.internal"},
			sourceVersion:       "{{ .locals.derived }}",
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedNaming,
			wantClosure:         localsRefusedNaming,
		},
		{
			name:                "self reference the source itself reads",
			locals:              []string{"loop", "{{ .locals.loop }}-x"},
			sourceVersion:       "{{ .locals.loop }}",
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedOrdering,
			wantClosure:         localsRefusedOrdering,
		},
		{
			name: "chain of three reversed, the source reading its head",
			locals: []string{
				"fqdn", "{{ .locals.svc }}.{{ .locals.stack }}.internal",
				"svc", "{{ .locals.stack }}-gateway",
				"stack", "edge",
			},
			sourceVersion:       "{{ .locals.fqdn }}",
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedOrdering,
			wantClosure:         localsRefusedOrdering,
		},
		{
			// The fault sits in an entry the source reaches only through another,
			// so finding it depends on following what each read entry reads.
			name: "a forward reference reached through another entry",
			locals: []string{
				"b", "{{ .locals.c }}-x",
				"c", "v",
				"a", "{{ .locals.b }}",
			},
			sourceVersion:       "{{ .locals.a }}",
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedOrdering,
			wantClosure:         localsRefusedOrdering,
		},
		{
			// A read no static reading resolves to named entries evaluates every
			// entry, so a fault in one the field does not name is still refused.
			name:                "a forward reference beside a dynamic read of the block",
			locals:              []string{"derived", "{{ .locals.base }}.internal", "base", "svc"},
			sourceVersion:       `{{ index .locals "base" }}`,
			sourceReadsTheBlock: true,
			wantRender:          localsRefusedOrdering,
			wantClosure:         localsRefusedOrdering,
		},
		{
			name:                "backward reference the source itself reads",
			locals:              []string{"base", "svc", "derived", "{{ .locals.base }}.internal"},
			sourceVersion:       "{{ .locals.derived }}",
			sourceReadsTheBlock: true,
			wantRender:          localsAdmitted,
			wantClosure:         localsAdmitted,
		},
		{
			// The tolerance's own case: an entry only a rendered artifact can
			// resolve. Source descent does not read it here, and resolves its
			// literal source, so a block carrying one still pins.
			name:          "a render-time-only entry, literal source",
			locals:        []string{"only_at_render", "{{ .context.instance_id }}-secret"},
			sourceVersion: "1.0.0",
			wantRender:    localsAdmitted,
			wantClosure:   localsAdmitted,
		},
		{
			name: "a later entry reads a render-time-only sibling, literal source",
			locals: []string{
				"only_at_render", "{{ .context.instance_id }}-secret",
				"derived", "{{ .locals.only_at_render }}-v2",
			},
			sourceVersion: "1.0.0",
			wantRender:    localsAdmitted,
			wantClosure:   localsAdmitted,
		},
		{
			// THE SURVIVING DISAGREEMENT of the tolerance, and the reason it is a
			// tolerance rather than a hole: the coordinate needed the entry source
			// descent could not resolve, so its own gate fires and names the source
			// field. The render's locals pass admits the same block.
			name:                "a render-time-only entry the source itself reads",
			locals:              []string{"only_at_render", "{{ .context.instance_id }}-secret"},
			sourceVersion:       "{{ .locals.only_at_render }}",
			sourceReadsTheBlock: true,
			wantRender:          localsAdmitted,
			wantClosure:         localsLockTime,
		},
	}
}

// renderVerdict drives the build's own locals evaluation over one authored block.
func renderVerdict(t *testing.T, locals []string) localsDivergenceVerdict {
	t.Helper()
	r := &types.Recipe{}
	r.Metadata.Name = "divergence-recipe"
	r.Spec.Locals = orderedLocals(locals...)
	_, err := renderer.EvaluateRecipeLocals(r, "api", types.OrderedMap[any]{}, types.OrderedMap[any]{}, redact.Keys{})
	switch {
	case err == nil:
		return localsAdmitted
	case errors.Is(err, renderer.ErrLocalsForwardReference()):
		return localsRefusedOrdering
	case errors.Is(err, renderer.ErrLocalsUnknownReference()):
		return localsRefusedNaming
	default:
		t.Fatalf("the render halted on a condition this comparison does not model: %v", err)
		return ""
	}
}

// closureVerdict drives source descent over the same authored block, through the
// exported entry the pull path reaches it by rather than through any internal
// helper — so what is pinned is the behaviour a lockfile acquisition actually has.
//
// The two reference faults are matched against the RENDER package's accessors,
// deliberately. That is the assertion that the two surfaces report one condition
// rather than two that happen to read alike: a condition minted separately for
// this surface would leave every reference row falling through to the fatal arm
// below instead of matching.
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
	case errors.Is(err, renderer.ErrLocalsForwardReference()):
		return localsRefusedOrdering
	case errors.Is(err, renderer.ErrLocalsUnknownReference()):
		return localsRefusedNaming
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
// It fails in six separate ways, and each is a change worth stopping on: a row
// whose recorded pair moves; a reference fault in an entry the source reads,
// reported by one surface and not by the other; such a fault reported under a
// condition the other surface's accessor does not match; source descent reporting
// anything for a block its source reads no entry of; a corpus that stopped
// carrying both agreements and the surviving disagreements; and a corpus in which
// the tolerance is no longer exercised at all.
func TestTheLockTimeLocalsToleranceDivergesFromTheBuildInOneDirectionOnly(t *testing.T) {
	t.Parallel()

	var agreements, disagreements int
	var closureAdmissions, closureOwnGate, sharedRefusals int
	for _, row := range localsDivergenceRows() {
		gotRender := renderVerdict(t, row.locals)
		gotClosure := closureVerdict(t, row.locals, row.sourceVersion)

		if gotRender != row.wantRender {
			t.Errorf("%s: the render reported %q, the pin records %q", row.name, gotRender, row.wantRender)
		}
		if gotClosure != row.wantClosure {
			t.Errorf("%s: source descent reported %q, the pin records %q", row.name, gotClosure, row.wantClosure)
		}

		if row.sourceReadsTheBlock {
			// THE INVARIANT, over the entries a coordinate reads. A reference fault
			// is decidable from the declared key order and the authored text alone,
			// so neither surface has anything the other lacks to decide it — which
			// is why the two must answer it identically wherever source descent
			// looks, and why a row where only one of them reports one is a
			// divergence rather than a difference of emphasis. Both directions are
			// named because both are possible: a tolerance can swallow the fault,
			// and a narrowing can overshoot and refuse a block the render accepts.
			if isReferenceFault(gotRender) || isReferenceFault(gotClosure) {
				if gotRender != gotClosure {
					t.Errorf("%s: the render reported %q and source descent reported %q for a REFERENCE fault "+
						"in an entry the source reads. That fault is decided from the declared key order and "+
						"the authored text, so the two passes have the same facts and must reach the same "+
						"verdict: either the tolerance has swallowed it, or the narrowing has overshot and "+
						"refuses input the render admits", row.name, gotRender, gotClosure)
				}
				if gotRender == gotClosure {
					sharedRefusals++
				}
			}
		} else if gotClosure != localsAdmitted {
			// THE REACH. A source reading no entry gives source descent nothing to
			// evaluate, so nothing in the block can halt it there.
			t.Errorf("%s: source descent reported %q for a block its source reads no entry of; an entry no "+
				"coordinate depends on is not evaluated there, and its faults are the render's to refuse",
				row.name, gotClosure)
		}

		if gotRender == gotClosure {
			agreements++
		} else {
			disagreements++
		}
		if gotClosure == localsAdmitted {
			closureAdmissions++
		}
		if gotClosure == localsLockTime {
			closureOwnGate++
		}
		t.Logf("ROW %-56s | render=%-18s closure=%s", row.name, gotRender, gotClosure)
	}

	// The corpus has to carry all of these, or the assertions above are satisfied by
	// a table nothing exercises: a corpus with no agreement cannot show that
	// agreement is possible, one with no disagreement cannot show the comparison can
	// report a difference, one with no shared refusal has stopped asking source
	// descent to classify anything, and one where source descent never admits has
	// lost the tolerance this file is about.
	if agreements == 0 {
		t.Error("no row has the two paths agreeing; the comparison cannot show that agreement is possible")
	}
	if disagreements == 0 {
		t.Error("no row has the two paths differing; the comparison cannot show it is able to report one")
	}
	if sharedRefusals == 0 {
		t.Error("no row has both paths refusing a reference fault in an entry the source reads; the " +
			"classification source descent runs over the entries it evaluates is not exercised")
	}
	if closureAdmissions == 0 {
		t.Error("source descent refused every row; the tolerance this pins is not exercised")
	}
	if closureOwnGate == 0 {
		t.Error("no row reaches source descent's own gate; the halt that covers a coordinate " +
			"needing a skipped entry is not exercised")
	}
	t.Logf("%d row(s) agree, %d differ; %d shared refusal(s); source descent admitted %d and reached its own gate %d",
		agreements, disagreements, sharedRefusals, closureAdmissions, closureOwnGate)
}

// isReferenceFault reports whether a verdict is one of the two faults BOTH passes
// decide, as against source descent's own coordinate gate, which only it has.
func isReferenceFault(v localsDivergenceVerdict) bool {
	return v == localsRefusedOrdering || v == localsRefusedNaming
}

// TestTheLockTimePinIsCalibrated is the needle under the pin: the comparison must
// be able to report a difference at all.
//
// The two paths are asked about the one block on this axis they are known to
// answer differently — an entry only a rendered artifact can resolve, which the
// coordinate then needs — and the verdicts must not be equal. Without this, a pin
// whose two sides had collapsed into one reading would pass every row, including
// every row that now records an agreement.
func TestTheLockTimePinIsCalibrated(t *testing.T) {
	t.Parallel()
	locals := []string{"only_at_render", "{{ .context.instance_id }}-secret"}
	gotRender := renderVerdict(t, locals)
	gotClosure := closureVerdict(t, locals, "{{ .locals.only_at_render }}")
	if gotRender == gotClosure {
		t.Fatalf("both paths reported %q over an input the two are known to differ on; "+
			"the comparison cannot report a difference", gotRender)
	}
	t.Logf("calibration: render=%q closure=%q -> DIFFER as expected", gotRender, gotClosure)
}
