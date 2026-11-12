// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package selector

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// applicabilityCase is one row of the applicability table: a pair of
// declared surfaces, the labels they are evaluated against, and what the
// composition reports.
type applicabilityCase struct {
	name    string
	applies string
	anyOf   []types.SelectorTerm
	labels  types.OrderedMap[string]
	want    bool
	wantErr bool
}

// applicabilityCases covers each way the composition can hold and each way
// it can fail, with both surfaces present and with each present alone.
//
// The rows that matter most are the two that separate the composition from
// either surface evaluated on its own: appliesTo holds and appliesToAnyOf
// does not, and the reverse. Both must come back false, and one of them
// comes back true under either single-surface implementation.
func applicabilityCases() []applicabilityCase {
	edge := makeLabels("tier", "edge", "env", "prod")
	return []applicabilityCase{
		{
			name:   "neither surface declared admits every label set",
			labels: edge,
			want:   true,
		},
		{
			name:    "appliesTo alone, holding",
			applies: "tier=edge",
			labels:  edge,
			want:    true,
		},
		{
			name:    "appliesTo alone, failing",
			applies: "tier=data",
			labels:  edge,
			want:    false,
		},
		{
			name:    "appliesTo requirements hold together",
			applies: "tier=edge,env=staging",
			labels:  edge,
			want:    false,
		},
		{
			name:   "appliesToAnyOf alone, one item holding",
			anyOf:  []types.SelectorTerm{types.StringTerm("tier=data"), types.MapTerm(makeLabels("tier", "edge"))},
			labels: edge,
			want:   true,
		},
		{
			name:   "appliesToAnyOf alone, no item holding",
			anyOf:  []types.SelectorTerm{types.StringTerm("tier=data"), types.MapTerm(makeLabels("env", "staging"))},
			labels: edge,
			want:   false,
		},
		{
			// The row an implementation evaluating only appliesTo reports
			// as true.
			name:    "appliesTo holds and appliesToAnyOf does not",
			applies: "tier=edge",
			anyOf:   []types.SelectorTerm{types.StringTerm("env=staging")},
			labels:  edge,
			want:    false,
		},
		{
			// The row an implementation evaluating only appliesToAnyOf
			// reports as true.
			name:    "appliesToAnyOf holds and appliesTo does not",
			applies: "tier=data",
			anyOf:   []types.SelectorTerm{types.StringTerm("tier=edge")},
			labels:  edge,
			want:    false,
		},
		{
			name:    "both surfaces hold",
			applies: "tier=edge",
			anyOf:   []types.SelectorTerm{types.StringTerm("env=prod")},
			labels:  edge,
			want:    true,
		},
		{
			name:    "an unparseable appliesTo does not admit",
			applies: "tier in (edge",
			labels:  edge,
			want:    false,
			wantErr: true,
		},
		{
			name:    "an unparseable appliesToAnyOf item does not admit",
			anyOf:   []types.SelectorTerm{types.StringTerm("tier in (edge")},
			labels:  edge,
			want:    false,
			wantErr: true,
		},
		{
			name:    "an unparseable appliesTo does not admit even when appliesToAnyOf holds",
			applies: "tier in (edge",
			anyOf:   []types.SelectorTerm{types.StringTerm("tier=edge")},
			labels:  edge,
			want:    false,
			wantErr: true,
		},
		{
			// Written to exclude edge nodes; read as an absence test on a key
			// named "tier=edge" it would admit every node, this one included.
			name:    "an appliesTo whose absence test carries a value does not admit",
			applies: "!tier=edge",
			labels:  edge,
			want:    false,
			wantErr: true,
		},
		{
			name:    "an appliesToAnyOf item made of separators does not admit",
			anyOf:   []types.SelectorTerm{types.StringTerm(" , ")},
			labels:  edge,
			want:    false,
			wantErr: true,
		},
	}
}

// TestMatchApplicabilityRequiresBothSurfaces pins the composition: both
// declared surfaces hold, an omitted surface is vacuously satisfied, and an
// unparseable declaration does not admit the label set.
func TestMatchApplicabilityRequiresBothSurfaces(t *testing.T) {
	t.Parallel()
	for _, tt := range applicabilityCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MatchApplicability(tt.applies, tt.anyOf, tt.labels)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("MatchApplicability(%q, %v) returned no error, want one", tt.applies, tt.anyOf)
				}
			} else if err != nil {
				t.Fatalf("MatchApplicability(%q, %v) error = %v", tt.applies, tt.anyOf, err)
			}
			if got != tt.want {
				t.Errorf("MatchApplicability(%q, %v) = %v, want %v", tt.applies, tt.anyOf, got, tt.want)
			}
		})
	}
}

// TestMatchApplicabilityMatchesTheCompositionEvaluatedDuringABuild is a
// cross-check, and its two sides are a DELIBERATE near-duplicate: the
// right-hand expression here is written out longhand precisely because a
// cross-check that shares an implementation with the thing it checks cannot
// disagree with it, and disagreement is the whole signal.
//
// What it pins is the composition the entry point implements — both surfaces
// conjoined, with an unparseable declaration refusing rather than admitting,
// and the first error reported in argument order. Changing the entry point
// without changing the longhand above, or the reverse, makes this fail.
//
// The rule deciding an attachment during a build calls this entry point
// rather than composing the surfaces itself, so what a build evaluates is
// what is pinned here and there is no second implementation to compare
// against. That is why the longhand side is written out in this file: it is
// the only independent statement of the composition left, and it is kept
// deliberately rather than folded into the entry point it checks.
func TestMatchApplicabilityMatchesTheCompositionEvaluatedDuringABuild(t *testing.T) {
	t.Parallel()
	for _, tt := range applicabilityCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			allHold, allErr := MatchString(tt.applies, tt.labels)
			anyHolds, anyErr := MatchAnyOf(tt.anyOf, tt.labels)
			wantErr := allErr
			if wantErr == nil {
				wantErr = anyErr
			}
			wantOK := wantErr == nil && allHold && anyHolds

			gotOK, gotErr := MatchApplicability(tt.applies, tt.anyOf, tt.labels)
			if gotOK != wantOK {
				t.Errorf("MatchApplicability = %v, longhand composition = %v", gotOK, wantOK)
			}
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("MatchApplicability error = %v, longhand composition error = %v", gotErr, wantErr)
			}
			if gotErr != nil && !strings.Contains(gotErr.Error(), wantErr.Error()) {
				t.Errorf("MatchApplicability error = %q, want the longhand composition's %q", gotErr, wantErr)
			}
		})
	}
}

// TestMatchApplicabilityReportsTheFirstUnevaluableSurfaceInArgumentOrder
// pins which error surfaces when both declarations are unparseable: the
// string form's, because it is the first argument. The order is part of the
// contract rather than an accident of evaluation, so a reordering of the
// body is a visible change.
func TestMatchApplicabilityReportsTheFirstUnevaluableSurfaceInArgumentOrder(t *testing.T) {
	t.Parallel()
	labels := makeLabels("tier", "edge")
	_, err := MatchApplicability(
		"appliesTo in (broken",
		[]types.SelectorTerm{types.StringTerm("anyOf in (broken")},
		labels,
	)
	if err == nil {
		t.Fatal("two unparseable surfaces returned no error")
	}
	if !strings.Contains(err.Error(), "appliesTo") {
		t.Errorf("error = %q, want the appliesTo surface's failure", err)
	}
}

// TestMatchApplicabilityDoesNotPanicOnAZeroLabelSet pins the negative-input
// contract: a nil label set produces a boolean and an error, never a panic.
// The assertion is explicit because a recovered panic does not fail a Go
// test on its own.
func TestMatchApplicabilityDoesNotPanicOnAZeroLabelSet(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("MatchApplicability panicked on a zero label set: %v", r)
		}
	}()
	var nilLabels types.OrderedMap[string]
	if got, err := MatchApplicability("tier=edge", nil, nilLabels); err != nil || got {
		t.Errorf("MatchApplicability over a zero label set = (%v, %v), want (false, nil)", got, err)
	}
	if got, err := MatchApplicability("", nil, nilLabels); err != nil || !got {
		t.Errorf("an undeclared applicability over a zero label set = (%v, %v), want (true, nil)", got, err)
	}
}
