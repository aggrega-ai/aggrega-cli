// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The second axis on which the two locals evaluators differ, and it runs the
// OPPOSITE way to the first.
//
// The file beside this one pins the ordering axis: where an entry sits relative
// to the sibling it reads. There, the two passes agree on every entry the source
// reads, and the render refuses what source descent never looks at. This file
// pins the template-function axis, where source descent is the stricter of the two.
//
// # The mechanism
//
// The two evaluators register different function sets. Rendering an artifact
// registers the broad set — the general-purpose template library with Aggrega's
// own helpers layered over it. Resolving a `source:` coordinate registers
// Aggrega's helpers ALONE, deliberately, so an authored name means one thing on
// a surface that evaluates a coordinate rather than rendering output, and no
// function reading the clock, the environment, a random source or the network
// takes part in deciding what is pinned.
//
// An unknown function is a PARSE failure in a Go template, not an execution
// failure, and source descent's tolerance covers execution failures only: a
// parse failure is kept loud. So an entry the source reads, calling a function
// only the render registers, is admitted by the render and HALTS source descent.
//
// # The reach is what the source reads
//
// Source descent evaluates only the entries a source field reads, directly or
// through another entry. So the divergence is a property of the entries a
// coordinate depends on, and an entry no field reads diverges from nothing: it is
// not evaluated here at all. Each row is therefore driven twice — with the source
// reading the entry and with a literal source — and the second drive must be
// admitted whatever the entry calls.
//
// # Why the direction matters more than the rows
//
// It is easy to read that halt as "a lockfile acquisition refuses what a build
// accepts", which would be the serious shape: an artifact that cannot be
// acquired for a document the compiler is happy with. 🔴 It is NOT that, and the
// assertions below are what keep the record straight. Source descent's body is
// reached from the build-time entry point and the lock-time one alike — ONE body
// serving both — so on this axis that body halts at either entry for the same
// input. What differs is which SURFACE is being resolved, never which phase is
// running.
//
// That is why the build-time column exists. Two columns would have pinned the
// divergence; the third is what pins the absence of a phase asymmetry, and it is
// the claim a reader is most likely to get wrong.

// localsHelperAxisRow is one authored locals entry and what each surface does
// with it, when the source reads it.
type localsHelperAxisRow struct {
	name string
	// expr is the single locals entry's authored template text.
	expr string

	// wantRender is the rendered-output pass's verdict.
	wantRender localsDivergenceVerdict
	// wantSourceCoord is the source-coordinate pass's verdict with the source
	// reading the entry, which both entry points into it must produce.
	wantSourceCoord localsDivergenceVerdict
}

func localsHelperAxisRows() []localsHelperAxisRow {
	return []localsHelperAxisRow{
		{
			// The disagreements. Both functions belong to the broad library the
			// render registers and to neither Aggrega helper set, so both fail
			// to parse where only Aggrega's helpers are registered.
			name:            "a broad-library function supplying a fallback",
			expr:            `{{ default "1.0.0" .values.tag }}`,
			wantRender:      localsAdmitted,
			wantSourceCoord: localsLockTime,
		},
		{
			name:            "a broad-library function doing arithmetic",
			expr:            `{{ add1 3 }}`,
			wantRender:      localsAdmitted,
			wantSourceCoord: localsLockTime,
		},
		{
			// The agreements. Without them the divergence below would hold over
			// a corpus in which the source-coordinate pass refuses everything,
			// and the assertion would be satisfied by a pass that had stopped
			// being asked anything it could answer.
			name:            "the Aggrega helper both surfaces register",
			expr:            `{{ optional .values "tag" "1.0.0" }}`,
			wantRender:      localsAdmitted,
			wantSourceCoord: localsAdmitted,
		},
		{
			name:            "a template built-in both surfaces have",
			expr:            `{{ printf "%s.%s" "1" "0" }}`,
			wantRender:      localsAdmitted,
			wantSourceCoord: localsAdmitted,
		},
	}
}

// sourceReadingTheEntry is the version expression that makes the source read the
// one entry every row's block carries.
const sourceReadingTheEntry = "{{ .locals.derived }}"

// sourceCoordinateVerdictAtBuild drives the source-coordinate locals pass
// through the BUILD-time entry point the render orchestrator reaches it by.
//
// It exists so the two entry points can be compared. Driving only the lock-time
// one would leave "the build-time entry halts here too" as prose, which is exactly
// the claim this file is here to make executable.
func sourceCoordinateVerdictAtBuild(t *testing.T, expr, sourceVersion string) localsDivergenceVerdict {
	t.Helper()
	const uri = "helper-axis-recipe"
	r := makeRecipe(uri, helmSource(sourceVersion))
	r.Spec.Locals = orderedLocals("derived", expr)

	_, err := closure.EvaluateSourceSpec(map[string]any{}, map[string]any{}, r)
	switch {
	case err == nil:
		return localsAdmitted
	case errors.Is(err, closure.ErrSourceTemplateAtLockTime()):
		return localsLockTime
	default:
		t.Fatalf("the build-time source entry halted on a condition this comparison does not model: %v", err)
		return ""
	}
}

// renderVerdictForOneEntry is renderVerdict over a single authored entry.
//
// The ordering-axis corpus hands its blocks over as flattened pairs because the
// ORDER is its subject; here the subject is one entry's function calls, so the
// block is one entry and the shape says so.
func renderVerdictForOneEntry(t *testing.T, expr string) localsDivergenceVerdict {
	t.Helper()
	r := &types.Recipe{}
	r.Metadata.Name = "helper-axis-recipe"
	r.Spec.Locals = orderedLocals("derived", expr)
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

// TestTheTemplateFunctionAxisDivergesInTheOppositeDirection is the pin.
//
// It fails in six separate ways, and each is a change worth stopping on: a row
// whose recorded verdict moves on either surface; the two entry points into the
// source-coordinate pass disagreeing, which would make the divergence a phase
// asymmetry it currently is not; an entry no source field reads being evaluated
// after all; the reversed direction ceasing to occur; and a corpus that stopped
// carrying agreements.
func TestTheTemplateFunctionAxisDivergesInTheOppositeDirection(t *testing.T) {
	t.Parallel()

	var agreements, reversed int
	for _, row := range localsHelperAxisRows() {
		gotRender := renderVerdictForOneEntry(t, row.expr)
		gotLockEntry := closureVerdict(t, []string{"derived", row.expr}, sourceReadingTheEntry)
		gotBuildEntry := sourceCoordinateVerdictAtBuild(t, row.expr, sourceReadingTheEntry)

		if gotRender != row.wantRender {
			t.Errorf("%s: the render reported %q, the pin records %q", row.name, gotRender, row.wantRender)
		}
		if gotLockEntry != row.wantSourceCoord {
			t.Errorf("%s: the source-coordinate pass reported %q at the lock-time entry, the pin records %q",
				row.name, gotLockEntry, row.wantSourceCoord)
		}
		if gotBuildEntry != row.wantSourceCoord {
			t.Errorf("%s: the source-coordinate pass reported %q at the build-time entry, the pin records %q",
				row.name, gotBuildEntry, row.wantSourceCoord)
		}

		// THE PHASE-SYMMETRY ASSERTION. One body serves both entry points, so
		// they cannot answer differently. If they ever do, this axis has become
		// the thing a reader keeps mistaking it for — a lockfile acquisition
		// refusing a document a build accepts — and the prose above, the pin
		// beside this file and the record all stop being true together.
		if gotLockEntry != gotBuildEntry {
			t.Errorf("%s: the source-coordinate pass answered %q at the lock-time entry and %q at the "+
				"build-time entry. One body serves both, so a difference here means they have been split: "+
				"this axis would now be a phase asymmetry, and every surface that says it is not needs "+
				"correcting with it", row.name, gotLockEntry, gotBuildEntry)
		}

		// THE REACH ASSERTION. With a literal source no field reads the entry, so
		// the pass does not evaluate it, and no function it calls can halt the
		// coordinate — at either entry point.
		if got := closureVerdict(t, []string{"derived", row.expr}, "1.0.0"); got != localsAdmitted {
			t.Errorf("%s: with no source field reading the entry, the lock-time entry reported %q; an entry "+
				"no coordinate depends on is not evaluated there, so it must be admitted", row.name, got)
		}
		if got := sourceCoordinateVerdictAtBuild(t, row.expr, "1.0.0"); got != localsAdmitted {
			t.Errorf("%s: with no source field reading the entry, the build-time entry reported %q; an entry "+
				"no coordinate depends on is not evaluated there, so it must be admitted", row.name, got)
		}

		if gotRender == gotLockEntry {
			agreements++
		}
		// The reversed direction: the render ADMITS and the source-coordinate
		// pass HALTS — the opposite of what the ordering axis does.
		if gotRender == localsAdmitted && gotLockEntry != localsAdmitted {
			reversed++
		}
		t.Logf("ROW %-48s | render=%-12s source-coord: lock=%-30s build=%s",
			row.name, gotRender, gotLockEntry, gotBuildEntry)
	}

	if reversed == 0 {
		t.Error("no row has the render admitting what the source-coordinate pass refuses; " +
			"this axis no longer diverges in the direction opposite to the ordering axis, and the " +
			"prose stating that it does — here, in the pin beside this file, and in the record — is stale")
	}
	if agreements == 0 {
		t.Error("no row has the two surfaces agreeing; the corpus cannot show that agreement is " +
			"possible, so the divergence above could be satisfied by a surface that refuses everything")
	}
	t.Logf("%d row(s) agree, %d run the reversed direction", agreements, reversed)
}
