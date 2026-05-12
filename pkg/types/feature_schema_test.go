// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFeature_SchemaField_RoundTripInlineContent pins the upward-
// compatibility property for Feature.Spec.Schema, mirroring the
// Component round-trip pin. A Feature with inline `schema:` content
// decodes into Spec.Schema and re-encodes preserving the pillar shape.
func TestFeature_SchemaField_RoundTripInlineContent(t *testing.T) {
	t.Parallel()

	in := `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/monitoring/scrape
  version: "1.0.0"
spec:
  appliesTo:
    component.aggrega.ai/type: server
  schema:
    values:
      scrape_port: { type: integer, minimum: 1024, maximum: 65535 }
      interval_seconds: { type: integer, minimum: 1 }
`
	var f Feature
	if err := yaml.Unmarshal([]byte(in), &f); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if f.Spec.Schema.Kind == 0 {
		t.Fatal("Spec.Schema not populated from `schema:` field")
	}

	out, err := yaml.Marshal(&f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	outStr := string(out)
	for _, frag := range []string{"scrape_port", "65535", "interval_seconds"} {
		if !strings.Contains(outStr, frag) {
			t.Errorf("round-trip lost fragment %q; output:\n%s", frag, outStr)
		}
	}
}

// TestFeature_SchemaField_OmitemptyEludesZeroValue verifies the Feature
// type analogue of the Component-side zero-value elision pin.
func TestFeature_SchemaField_OmitemptyEludesZeroValue(t *testing.T) {
	t.Parallel()

	f := Feature{
		APIVersion: "aggrega.ai/v1alpha1",
		Kind:       "Feature",
		Metadata: FeatureMetadata{
			Name:    "core/monitoring/scrape",
			Version: "1.0.0",
		},
	}
	out, err := yaml.Marshal(&f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(out), "schema:") {
		t.Errorf("expected no schema field in output (zero-value omitempty); got:\n%s", string(out))
	}
}
