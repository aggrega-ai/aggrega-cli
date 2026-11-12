// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"testing"
)

// aggregateWithHoistedValue renders an Aggregate whose single component carries
// one authored value, written exactly as the caller supplies it. The caller
// decides the quoting, which is the property under test.
func aggregateWithHoistedValue(authored string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: quoting
  version: "1.0.0"
spec:
  parameters:
    size:
      type: integer
      default: 1
    enabled:
      type: boolean
      default: false
  components:
    app:
      type: core/server:1.0.0
      values:
        knob: ` + authored + `
`)
}

// hoistedValue reads the single authored value back out of a loaded Aggregate.
func hoistedValue(t *testing.T, data []byte, params map[string]any) any {
	t.Helper()
	interpolated, err := InterpolateParameters(data, params)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	agg, err := LoadAggregateFromBytes(interpolated, "aggregate.yaml")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	sub, ok := agg.Spec.Subsystems.Get("default")
	if !ok {
		t.Fatal("expected the synthesized default subsystem")
	}
	ci, ok := sub.Components.Get("app")
	if !ok {
		t.Fatal("expected component 'app'")
	}
	v, ok := ci.Values.Get("knob")
	if !ok {
		t.Fatal("expected authored value 'knob'")
	}
	return v
}

// TestHoistedParameterQuotingDecidesType pins the authoring contract: an author
// leaving a parameter reference bare gets the fulfilled value's own type, and
// one wrapping it in quotes gets a string. The rule is YAML's, not this
// package's — the reference is substituted into the document's bytes, and the
// parse that follows resolves the result the way it resolves any scalar.
func TestHoistedParameterQuotingDecidesType(t *testing.T) {
	tests := []struct {
		name     string
		authored string
		params   map[string]any
		want     any
	}{
		{
			name:     "bare reference to an integer yields an integer",
			authored: `{{ .parameters.size }}`,
			params:   map[string]any{"size": 256},
			want:     256,
		},
		{
			name:     "quoted reference to an integer yields a string",
			authored: `"{{ .parameters.size }}"`,
			params:   map[string]any{"size": 256},
			want:     "256",
		},
		{
			name:     "single-quoted reference to an integer yields a string",
			authored: `'{{ .parameters.size }}'`,
			params:   map[string]any{"size": 256},
			want:     "256",
		},
		{
			name:     "bare reference to a boolean yields a boolean",
			authored: `{{ .parameters.enabled }}`,
			params:   map[string]any{"enabled": true},
			want:     true,
		},
		{
			name:     "quoted reference to a boolean yields a string",
			authored: `"{{ .parameters.enabled }}"`,
			params:   map[string]any{"enabled": true},
			want:     "true",
		},
		{
			name:     "a bare reference surrounded by text yields a string",
			authored: `v{{ .parameters.size }}`,
			params:   map[string]any{"size": 256},
			want:     "v256",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hoistedValue(t, aggregateWithHoistedValue(tt.authored), tt.params)
			if got != tt.want {
				t.Fatalf("value = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}

// TestLoadAggregateReadsStructureBeforeSubstitution pins the load order the
// contract above rests on. A bare reference is well-formed YAML that reads as a
// flow mapping rather than a scalar, so a load of the document as authored —
// before any reference is substituted — has to keep working: the identity and
// the type references it reports are what selects the entities whose schemas
// then judge the substituted values. A load that refused here would make the
// bare form unauthorable no matter what the substitution produced.
func TestLoadAggregateReadsStructureBeforeSubstitution(t *testing.T) {
	agg, err := LoadAggregateFromBytes(aggregateWithHoistedValue(`{{ .parameters.size }}`), "aggregate.yaml")
	if err != nil {
		t.Fatalf("load as authored: %v", err)
	}
	if agg.Metadata.Name != "quoting" {
		t.Errorf("metadata.name = %q, want %q", agg.Metadata.Name, "quoting")
	}
	sub, ok := agg.Spec.Subsystems.Get("default")
	if !ok {
		t.Fatal("expected the synthesized default subsystem")
	}
	ci, ok := sub.Components.Get("app")
	if !ok {
		t.Fatal("expected component 'app'")
	}
	if ci.Type != "core/server:1.0.0" {
		t.Errorf("component type = %q, want %q", ci.Type, "core/server:1.0.0")
	}
	if _, ok := agg.Spec.Parameters.Get("size"); !ok {
		t.Error("expected the declared parameter 'size' to survive the structural read")
	}
}
