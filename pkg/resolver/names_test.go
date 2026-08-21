// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func TestValidateInstanceNames_ValidNames(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web-server", types.ComponentInstance{Type: "core/server:1.0.0"})
	components.Set("my_worker", types.ComponentInstance{Type: "core/worker:1.0.0"})
	components.Set("api123", types.ComponentInstance{Type: "core/server:1.0.0"})
	aggregates := types.OrderedMap[types.SubAggregate]{}
	aggregates.Set("auth-module", types.SubAggregate{Type: "auth/module:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
			Aggregates: aggregates,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) > 0 {
		t.Fatalf("expected no errors for valid names, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestValidateInstanceNames_DotRejected(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("nested.component", types.ComponentInstance{Type: "core/server:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) == 0 {
		t.Fatal("expected error for dot in instance name")
	}
	if !errors.Is(errs[0], ErrInvalidInstanceName()) {
		t.Errorf("expected ErrInvalidInstanceName, got %v", errs[0].Err)
	}
}

func TestValidateInstanceNames_SpaceRejected(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bad name", types.ComponentInstance{Type: "core/server:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) == 0 {
		t.Fatal("expected error for space in instance name")
	}
}

func TestValidateInstanceNames_SpecialCharsRejected(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bad@name", types.ComponentInstance{Type: "core/server:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) == 0 {
		t.Fatal("expected error for special char in instance name")
	}
}

// TestValidateInstanceNames_SynthesizedDefaultElidesPath is the A2 elision
// red/green: an invalid name on a top-level component (folded into the
// synthesized default) renders the root-shaped locator the author typed, never
// under the generated `default` scope.
func TestValidateInstanceNames_SynthesizedDefaultElidesPath(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bad name", types.ComponentInstance{Type: "core/server:1.0.0"})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	}
	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 name error; got %d: %v", len(errs), errs)
	}
	if errs[0].Field != `.spec.components["bad name"]` {
		t.Errorf("synthesized-default component name error must render root-shaped, never under default; got %q", errs[0].Field)
	}
}

// TestValidateInstanceNames_DeclaredDefaultStaysQualified is the counter-case: a
// subsystem a user *declares* with the name "default" is not synthesized, so an
// invalid name inside it renders the qualified locator — proving the elision
// keys on the provenance flag, not the path string.
func TestValidateInstanceNames_DeclaredDefaultStaysQualified(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bad name", types.ComponentInstance{Type: "core/server:1.0.0"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("default", types.Subsystem{Components: components})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Subsystems: subsystems},
	}
	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) != 1 {
		t.Fatalf("want exactly 1 name error; got %d: %v", len(errs), errs)
	}
	if errs[0].Field != `.spec.subsystems.default.components["bad name"]` {
		t.Errorf("a declared subsystem named default must render qualified; got %q", errs[0].Field)
	}
}

func TestValidateInstanceNames_NestedSubsystem(t *testing.T) {
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("bad.comp", types.ComponentInstance{Type: "core/server:1.0.0"})
	subsystems := types.OrderedMap[types.Subsystem]{}
	subsystems.Set("valid-subsystem", types.Subsystem{Components: components})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: subsystems,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) == 0 {
		t.Fatal("expected error for dot in nested component name")
	}
}

func TestValidateInstanceNames_FeatureNames(t *testing.T) {
	features := types.OrderedMap[types.FeatureInstance]{}
	features.Set("bad.feature", types.FeatureInstance{Type: "core/logging:1.0.0"})
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("web", types.ComponentInstance{Type: "core/server:1.0.0", Features: features})
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: components,
		},
	}

	errs := ValidateInstanceNames(normalized(t, agg))
	if len(errs) == 0 {
		t.Fatal("expected error for dot in feature instance name")
	}
}

func TestValidateInstanceNames_Nil(t *testing.T) {
	errs := ValidateInstanceNames(normalized(t, nil))
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil aggregate, got %d", len(errs))
	}
}
