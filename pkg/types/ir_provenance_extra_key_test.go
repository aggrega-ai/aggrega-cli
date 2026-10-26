// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// irProvenanceRetiredKey is a key the IR schema's provenance object does not
// declare and that graphs written by earlier compilers carry: a list of source
// identifiers that was always empty.
const irProvenanceRetiredKey = "source_identifiers"

// TestIRProvenanceAdmitsUndeclaredKey holds the IR schema to one property of the
// provenance object: it admits keys it does not declare, so a graph that still
// carries the retired source_identifiers list validates against the current
// schema.
//
// Three readings make that a claim about the schema rather than about the
// validator. The key is first shown to be undeclared, so acceptance cannot come
// from a declaration. The same key placed on the metadata object, which the schema
// closes, is then refused at that position, so the validator does report an
// undeclared key where the schema forbids one. Only then is acceptance at the
// provenance object read as the schema admitting it.
func TestIRProvenanceAdmitsUndeclaredKey(t *testing.T) {
	t.Parallel()

	provenance := irProvenanceDefinition(t)
	if props, _ := provenance["properties"].(map[string]any); props == nil {
		t.Fatalf("the IR schema's provenance definition declares no properties object; the walk no longer reaches it")
	} else if _, declared := props[irProvenanceRetiredKey]; declared {
		t.Fatalf("the IR schema's provenance definition declares %q, so a graph carrying it validates by "+
			"declaration and this test proves nothing about undeclared keys", irProvenanceRetiredKey)
	}
	if required, _ := provenance["required"].([]any); required != nil {
		for _, name := range required {
			if name == irProvenanceRetiredKey {
				t.Fatalf("the IR schema's provenance definition requires %q, which it no longer declares",
					irProvenanceRetiredKey)
			}
		}
	}

	if findings := irProvenanceValidate(t, irZeroScaffold(), nil); len(findings) > 0 {
		t.Fatalf("the scaffold must conform before any verdict can be read from it; got %d finding(s): %s",
			len(findings), irZeroFormatFindings(findings))
	}

	t.Run("refused-where-the-schema-closes-the-object", func(t *testing.T) {
		t.Parallel()
		findings := irProvenanceValidate(t, irZeroScaffold(), func(doc map[string]any) {
			doc["metadata"].(map[string]any)[irProvenanceRetiredKey] = []any{"aggregate.yaml"}
		})
		if len(findings) == 0 {
			t.Fatalf("an undeclared key on the closed metadata object was accepted; the validator is not " +
				"reporting undeclared keys, so acceptance at the provenance object would prove nothing")
		}
		for _, f := range findings {
			if f.FieldPath != "metadata" && !strings.HasPrefix(f.FieldPath, "metadata.") {
				t.Errorf("finding outside the metadata object, so the fixture carries a second fault: %s",
					irZeroFormatFindings([]jsonvalidator.Finding{f}))
			}
		}
	})

	for _, tc := range []struct {
		name  string
		value []any
	}{
		{name: "empty-list", value: []any{}},
		{name: "populated-list", value: []any{"aggregate.yaml"}},
	} {
		tc := tc
		t.Run("admitted-on-provenance/"+tc.name, func(t *testing.T) {
			t.Parallel()
			findings := irProvenanceValidate(t, irZeroScaffold(), func(doc map[string]any) {
				spec := doc["spec"].(map[string]any)
				spec["provenance"].(map[string]any)[irProvenanceRetiredKey] = tc.value
			})
			if len(findings) > 0 {
				t.Errorf("a graph whose provenance carries %q was refused; the provenance object must admit "+
					"keys it does not declare. Findings: %s", irProvenanceRetiredKey, irZeroFormatFindings(findings))
			}
		})
	}
}

// irProvenanceValidate marshals a graph, lets mutate edit the decoded document,
// and validates the result through the IR envelope entry point.
func irProvenanceValidate(t *testing.T, g *types.IRGraph, mutate func(map[string]any)) []jsonvalidator.Finding {
	t.Helper()
	raw, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("marshal IR document: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("re-decode IR document: %v", err)
	}
	if mutate != nil {
		mutate(doc)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, doc)
	if err != nil {
		t.Fatalf("IR schema engine error: %v", err)
	}
	return findings
}

// irProvenanceDefinition returns the IR schema's provenance definition, read from
// the embedded schema document.
func irProvenanceDefinition(t *testing.T) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(schemas.IRSchemaV1alpha1), &doc); err != nil {
		t.Fatalf("decode embedded IR schema: %v", err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	def, _ := defs["IRProvenance"].(map[string]any)
	if def == nil {
		t.Fatalf("the embedded IR schema has no $defs.IRProvenance; the walk no longer reaches the provenance definition")
	}
	return def
}
