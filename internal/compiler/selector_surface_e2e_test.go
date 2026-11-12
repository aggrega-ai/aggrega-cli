// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof of the Feature appliesTo selector
// surface through a real build. The positive build attaches one Feature per
// selector operator class to a component whose labels satisfy each
// constraint and asserts every attachment survives into the IR. The three
// negative builds prove the build halts: an unsatisfied appliesTo, a
// malformed appliesTo selector, and a usesSchema socket-selector violation on
// a wired uses-edge.
//
// All three halts are enforced by rules declared over the finalised graph and
// invoked once per coordinate that graph holds, so what these proofs drive is
// the real product rather than any one reading of it.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const selectorSurfaceCatalog = "testdata/selector-surface/catalog/base"

// TestSelectorSurface_OperatorVocabularyAttaches proves the positive path:
// six Features, each exercising a distinct selector operator class, attach to
// a component (web) whose labels satisfy each constraint. The build is clean,
// and every attachment is present in the IR FeatureValuesMapping under its
// node — the appliesTo gate let all six through because each holds.
func TestSelectorSurface_OperatorVocabularyAttaches(t *testing.T) {
	t.Parallel()

	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/selector-surface/release-attach.yaml",
		CatalogPaths: []string{selectorSurfaceCatalog},
	})
	if err != nil {
		t.Fatalf("expected a clean build for the satisfied-appliesTo attachments, got: %v", err)
	}

	fvm, ok := result.IRGraph.Spec.FeatureValuesMapping.Get("web")
	if !ok {
		t.Fatal("expected feature_values_mapping for node web")
	}

	// Each assertion names the attachment alias, the feature type, and the
	// selector operator class the attachment proves satisfiable end-to-end.
	cases := []struct {
		instance string // attachment alias on the component
		wantType string // resolved feature type
		operator string // selector operator class proven by this attachment
	}{
		{"eq", "core/feat-equality", "equality (key=value)"},
		{"zone-in", "core/feat-in", "set membership (key in (a, b))"},
		{"zone-notin", "core/feat-notin", "set exclusion (key notin (a, b))"},
		{"has-category", "core/feat-exists", "existence (bare key)"},
		{"no-deprecated", "core/feat-absent", "absence (!key)"},
		{"either", "core/feat-anyof", "appliesToAnyOf OR (string arm + flat-map arm)"},
	}

	for _, c := range cases {
		entry, ok := fvm.Get(c.instance)
		if !ok {
			t.Errorf("operator %s: expected attachment %q on node web, missing", c.operator, c.instance)
			continue
		}
		if entry.Type != c.wantType {
			t.Errorf("operator %s: attachment %q type = %q, want %q", c.operator, c.instance, entry.Type, c.wantType)
		}
	}
}

// TestSelectorSurface_ConstraintNotSatisfiedHalts proves an attachment whose
// target's labels do not satisfy the Feature's appliesTo halts the build with
// ErrSelectorViolation, naming the attached Feature type and the applicability
// that Feature declares.
//
// The halt is positioned rather than only reported: the document the attachment
// was authored in is what the fault points at, where the identifier of the node
// carrying the attachment was what it pointed at before the enforcement moved
// onto the declaration.
func TestSelectorSurface_ConstraintNotSatisfiedHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/selector-surface/release-constraint.yaml",
		CatalogPaths: []string{selectorSurfaceCatalog},
	})
	if err == nil {
		t.Fatal("expected a build halt for the unsatisfied appliesTo, got nil")
	}
	if !errors.Is(err, analyzer.ErrSelectorViolation()) {
		t.Fatalf("expected ErrSelectorViolation, got: %v", err)
	}
	for _, want := range []string{
		"core/feat-equality",
		"component.aggrega.ai/type=server",
		"testdata/selector-surface/aggregate-constraint.yaml",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the halt to name %q, got: %v", want, err)
		}
	}
}

// TestSelectorSurface_MalformedSelectorHalts proves a Feature whose appliesTo
// fails to parse halts the build carrying the reason it could not be evaluated,
// as against a constraint that parsed and came out false.
//
// The reason is what tells the two apart, so it is asserted rather than the
// sentinel alone — both classes carry ErrSelectorViolation.
func TestSelectorSurface_MalformedSelectorHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/selector-surface/release-malformed.yaml",
		CatalogPaths: []string{selectorSurfaceCatalog},
	})
	if err == nil {
		t.Fatal("expected a build halt for the malformed appliesTo selector, got nil")
	}
	if !errors.Is(err, analyzer.ErrSelectorViolation()) {
		t.Fatalf("expected ErrSelectorViolation, got: %v", err)
	}
	for _, want := range []string{
		"core/feat-malformed",
		"tier.aggrega.ai/zone in (frontend",
		"expected (values) after in",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected the halt to name %q, got: %v", want, err)
		}
	}
}

// TestSelectorSurface_UsesSchemaSelectorViolation proves a wired uses-edge
// whose target does not satisfy the consuming component's usesSchema socket
// selector halts the build with ErrSelectorViolation.
func TestSelectorSurface_UsesSchemaSelectorViolation(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/selector-surface/release-usesschema.yaml",
		CatalogPaths: []string{selectorSurfaceCatalog},
	})
	if err == nil {
		t.Fatal("expected a build halt for the usesSchema socket-selector violation, got nil")
	}
	if !errors.Is(err, analyzer.ErrSelectorViolation()) {
		t.Fatalf("expected ErrSelectorViolation, got: %v", err)
	}
}
