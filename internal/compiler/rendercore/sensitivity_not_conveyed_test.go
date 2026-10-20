// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestUnconveyedValueLeafDeclarations_EveryCell walks the whole grid the
// reading is gated on rather than each axis in isolation. The condition has two
// binary gates plus an emptiness check, so naming the axes independently would
// specify the grid's margins and leave its interior unstated — and a defect can
// sit in a cell no per-axis assertion reaches while every one of them passes.
//
// Two cells are unreachable from authoring and are asserted anyway, with the
// reason recorded per row. A values mapping requires a source, so no shipped
// Recipe reaches the sourceless-with-the-gate-off rows — but the silence there
// is produced by this reading rather than by that load-time rule, and a reading
// that leaned on the rule would start advising the day the rule moved.
func TestUnconveyedValueLeafDeclarations_EveryCell(t *testing.T) {
	t.Parallel()

	leaves := map[string]bool{"dbPassword": true}

	for _, tc := range []struct {
		name     string
		sourced  bool
		declared DeclaredSensitivity
		want     []string
		why      string
	}{
		{
			name:     "sourced, join gated off, something declared",
			sourced:  true,
			declared: DeclaredSensitivity{ValueLeaves: leaves},
			want:     []string{"values.dbPassword"},
			why: "the one shape the author holds a lever over: the mapping is theirs, " +
				"and removing it restores the join",
		},
		{
			name:     "sourced, join on, something declared",
			sourced:  true,
			declared: DeclaredSensitivity{ValueLeaves: leaves, ValuesAreDeclaredLeaves: true},
			want:     nil,
			why:      "the declaration is conveyed, so advising would be false",
		},
		{
			name:     "not sourced, join on, something declared",
			sourced:  false,
			declared: DeclaredSensitivity{ValueLeaves: leaves, ValuesAreDeclaredLeaves: true},
			want:     nil,
			why: "an artifacts placement or a workload-only render: the dispatch carries " +
				"no values map, so the declaration is unconveyable permanently and there " +
				"is no action to name",
		},
		{
			name:     "not sourced, join gated off, something declared",
			sourced:  false,
			declared: DeclaredSensitivity{ValueLeaves: leaves},
			want:     nil,
			why: "unreachable from authoring — a values mapping requires a source, and a " +
				"node binding no Recipe leaves the gate at its zero value. The silence is " +
				"this reading's own rather than the load-time rule's, which is what keeps " +
				"an advisory naming an empty Recipe out of the stream",
		},
		{
			name:     "sourced, join gated off, nothing declared",
			sourced:  true,
			declared: DeclaredSensitivity{},
			want:     nil,
			why:      "nothing was declared, so nothing was spent on nothing",
		},
		{
			name:    "sourced, join gated off, slots declared and no values leaf",
			sourced: true,
			declared: DeclaredSensitivity{
				Slots: map[string]bool{"credentials": true},
			},
			want: nil,
			why: "the slot axis is a different declaration and travels on the workload " +
				"join; reporting it here would name a declaration this reading is not about",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := UnconveyedValueLeafDeclarations(tc.declared, tc.sourced)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v — %s", got, tc.want, tc.why)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v — %s", got, tc.want, tc.why)
				}
			}
		})
	}
}

// TestUnconveyedValueLeafDeclarations_OrderIsDeterministic pins the ordering
// the advisory stream's byte-stability rests on. The source is a map, so the
// only way this can be deterministic is by sorting — and the fixture is
// deliberately such that its map keys' insertion order is not its sorted order,
// so a reading that returned them as the map yields them fails rather than
// passing by luck on a small map.
func TestUnconveyedValueLeafDeclarations_OrderIsDeterministic(t *testing.T) {
	t.Parallel()

	declared := DeclaredSensitivity{ValueLeaves: map[string]bool{
		"zebra":              true,
		"alpha":              true,
		"middle.nested.leaf": true,
		"beta":               true,
	}}
	want := []string{"values.alpha", "values.beta", "values.middle.nested.leaf", "values.zebra"}

	// Repeated, because one agreeing run over a randomised map iteration is not
	// evidence: a reading that did not sort agrees with the sorted answer some
	// of the time.
	for run := 0; run < 32; run++ {
		got := UnconveyedValueLeafDeclarations(declared, true)
		if len(got) != len(want) {
			t.Fatalf("run %d: got %v, want %v", run, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("run %d: got %v, want %v — the order is the advisory stream's byte-stability",
					run, got, want)
			}
		}
	}
}

// TestUnconveyedValueLeafDeclarations_CoordinateIsSpelledFromTheSharedRoot
// holds the coordinate against the same pillar-root constant every other
// surface that writes or parses a coordinate reads, so the advisory's
// SourceRef and the marking's declaration coordinate cannot come to be spelled
// differently.
func TestUnconveyedValueLeafDeclarations_CoordinateIsSpelledFromTheSharedRoot(t *testing.T) {
	t.Parallel()

	got := UnconveyedValueLeafDeclarations(
		DeclaredSensitivity{ValueLeaves: map[string]bool{"leaf": true}}, true)
	if len(got) != 1 {
		t.Fatalf("got %v, want one coordinate", got)
	}
	if want := types.PillarRootValues + "leaf"; got[0] != want {
		t.Errorf("coordinate %q, want %q", got[0], want)
	}
	// The control on the assertion above: a bare leaf name is what the coordinate
	// must NOT be, because one declaring block is not the only one a reader of
	// this axis meets and a bare name leaves them unable to tell which.
	if got[0] == "leaf" {
		t.Error("the coordinate lost its pillar root")
	}
}

// TestSensitivityNotConveyedDiagnostic_ShapeAndClass pins what the constructor
// puts on the wire: the class's own code, warn severity, and the authored
// coordinate as the element the finding concerns — so a consumer that renders
// no text can act on it without parsing a sentence.
func TestSensitivityNotConveyedDiagnostic_ShapeAndClass(t *testing.T) {
	t.Parallel()

	d := SensitivityNotConveyedDiagnostic("some-recipe", "values.dbPassword")
	if d.Code != SensitivityNotConveyedCode {
		t.Errorf("code %q, want %q", d.Code, SensitivityNotConveyedCode)
	}
	if d.Severity != abi.SeverityWarn {
		t.Errorf("severity %v, want warn — this reports an absence, so there is nothing to halt over", d.Severity)
	}
	if d.SourceRef != "values.dbPassword" {
		t.Errorf("SourceRef %q, want the authored coordinate", d.SourceRef)
	}
	// The sentence says what the target does receive as well as what it does not:
	// no per-key marking, and the mapping's whole output marked where the mapping
	// reads the declared value.
	for _, want := range []string{"values.dbPassword", "some-recipe", "valuesMapping", "no per-key sensitivity marking", "whole output is marked"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q does not name %s", d.Message, want)
		}
	}
	// It must not be reachable by the switch that silences the unsupported-field
	// class: that one reports a target failing to express an authored field, and
	// this reports a judgement about the author's own declaration. An operator
	// silences the two for unrelated reasons.
	if SensitivityNotConveyedCode == UnsupportedFieldCode {
		t.Fatalf("the two classes must not share a code; both are %q", SensitivityNotConveyedCode)
	}
	if SensitivityNotConveyedCode == SensitiveByValueCode {
		t.Fatalf("the two sensitivity advisories must not share a code; both are %q", SensitivityNotConveyedCode)
	}
}

// TestUnconveyedValueLeafDeclarations_IsTheComplementOfTheValuesJoin is the
// assertion that keeps the advisory and the marking from drifting into two
// answers about one condition. Both read the same DeclaredSensitivity, so for a
// sourced dispatch carrying the declared leaf as a resolved key, exactly one of
// the two speaks: either the join recovers the key, or the reading names the
// declaration as unconveyed. Never both, and never neither.
func TestUnconveyedValueLeafDeclarations_IsTheComplementOfTheValuesJoin(t *testing.T) {
	t.Parallel()

	values := types.OrderedMap[any]{}
	values.Set("dbPassword", "s3cr3t")

	for _, gateOn := range []bool{true, false} {
		declared := DeclaredSensitivity{
			ValueLeaves:             map[string]bool{"dbPassword": true},
			ValuesAreDeclaredLeaves: gateOn,
		}
		marked := SensitiveRenderedKeys(declared, values, nil)
		advised := UnconveyedValueLeafDeclarations(declared, true)

		if len(marked) > 0 && len(advised) > 0 {
			t.Errorf("gate=%v: the key is marked %v AND advised as unconveyed %v; the advisory "+
				"would be contradicted by the same build's own payload", gateOn, marked, advised)
		}
		if len(marked) == 0 && len(advised) == 0 {
			t.Errorf("gate=%v: the key is neither marked nor advised, so a declaration was spent "+
				"on nothing in silence — which is the condition this advisory exists to end", gateOn)
		}
	}
}
