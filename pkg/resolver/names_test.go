package resolver

import (
	"errors"
	"testing"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

func TestValidateInstanceNames_ValidNames(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"web-server": {Type: "core/server:1.0.0"},
				"my_worker":  {Type: "core/worker:1.0.0"},
				"api123":     {Type: "core/server:1.0.0"},
			},
			Aggregates: map[string]types.SubAggregate{
				"auth-module": {Type: "auth/module:1.0.0"},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors for valid names, got %d: %v", len(errs), errs[0].Error())
	}
}

func TestValidateInstanceNames_DotRejected(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"nested.component": {Type: "core/server:1.0.0"},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) == 0 {
		t.Fatal("expected error for dot in instance name")
	}
	if !errors.Is(errs[0], ErrInvalidInstanceName) {
		t.Errorf("expected ErrInvalidInstanceName, got %v", errs[0].Err)
	}
}

func TestValidateInstanceNames_SpaceRejected(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"bad name": {Type: "core/server:1.0.0"},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) == 0 {
		t.Fatal("expected error for space in instance name")
	}
}

func TestValidateInstanceNames_SpecialCharsRejected(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"bad@name": {Type: "core/server:1.0.0"},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) == 0 {
		t.Fatal("expected error for special char in instance name")
	}
}

func TestValidateInstanceNames_NestedSubsystem(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Subsystems: map[string]types.Subsystem{
				"valid-subsystem": {
					Components: map[string]types.ComponentInstance{
						"bad.comp": {Type: "core/server:1.0.0"},
					},
				},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) == 0 {
		t.Fatal("expected error for dot in nested component name")
	}
}

func TestValidateInstanceNames_FeatureNames(t *testing.T) {
	agg := &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec: types.AggregateSpec{
			Components: map[string]types.ComponentInstance{
				"web": {
					Type: "core/server:1.0.0",
					Features: map[string]types.FeatureInstance{
						"bad.feature": {Type: "ops/logging:1.0.0"},
					},
				},
			},
		},
	}

	errs := ValidateInstanceNames(agg)
	if len(errs) == 0 {
		t.Fatal("expected error for dot in feature instance name")
	}
}

func TestValidateTargetRef_Valid(t *testing.T) {
	tests := []string{
		"component-name",
		"nested-agg.component",
		"deep.nested.agg.comp",
		"a1.b2.c3",
	}
	for _, ref := range tests {
		if err := ValidateTargetRef(ref, "test", ".field"); err != nil {
			t.Errorf("expected valid target ref %q, got error: %v", ref, err)
		}
	}
}

func TestValidateTargetRef_Invalid(t *testing.T) {
	tests := []string{
		".leading-dot",
		"trailing-dot.",
		"double..dot",
		"bad name",
		"bad@char.comp",
	}
	for _, ref := range tests {
		if err := ValidateTargetRef(ref, "test", ".field"); err == nil {
			t.Errorf("expected invalid target ref %q to fail", ref)
		}
	}
}

func TestValidateInstanceNames_Nil(t *testing.T) {
	errs := ValidateInstanceNames(nil)
	if len(errs) != 0 {
		t.Fatalf("expected no errors for nil aggregate, got %d", len(errs))
	}
}
