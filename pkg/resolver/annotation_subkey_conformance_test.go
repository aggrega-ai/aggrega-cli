// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	stderrors "errors"
	"fmt"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// subkeyConformanceManifest declares one binding routing every subkey the cases
// below write, so a value that fails is failing the Feature's contract rather
// than falling out of the routing.
func subkeyConformanceManifest(t *testing.T) *types.Manifest {
	t.Helper()
	return loadManifest(t, `
apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: test
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/monitoring:
          feature: core/monitoring:1.0.0
          maps:
            values:
              - level
              - port
              - name
              - missing
`)
}

// subkeyConformanceCatalog holds the target Feature whose contract the values
// are measured against.
func subkeyConformanceCatalog(t *testing.T, schema string) CatalogLookup {
	t.Helper()
	return mockCatalogLookup(map[string]types.Feature{
		"core/monitoring:1.0.0": featureWithSchema(t, "core/monitoring", schema),
	})
}

// TestSubkeyConformance_FullValidationNotTypeOnly pins that an annotation subkey
// value is validated against the FULL JSON Schema at its path — enum, pattern,
// numeric range, nested shape — rather than against its type alone. Each
// violating value here carries the right Go type and breaks the declared
// constraint, so a type-only reading would pass every one of them.
//
// It drives each case through the declared rule's own entry point rather than
// through a predicate, so a row that passes says the rule was invoked for the
// entry and decided on it — not merely that a function would have decided
// correctly had anything called it.
func TestSubkeyConformance_FullValidationNotTypeOnly(t *testing.T) {
	t.Parallel()
	const schema = `values:
  level: { type: string, enum: [info, warn, error] }
  port: { type: integer, minimum: 1, maximum: 65535 }
  name: { type: string, pattern: "^[a-z]+$" }
`
	manifest := subkeyConformanceManifest(t)
	catalog := subkeyConformanceCatalog(t, schema)

	cases := []struct {
		subkey  string
		value   string
		wantErr bool
	}{
		{"level", "info", false}, // conforms
		{"level", "bogus", true}, // enum violation (type-only would pass)
		{"port", "8080", false},  // conforms
		{"port", "0", true},      // below minimum (type-only would pass)
		{"port", "70000", true},  // above maximum
		{"name", "api", false},   // conforms
		{"name", "API", true},    // pattern violation (type-only would pass)
		{"missing", "x", true},   // the binding routes it; the Feature declares none
	}
	for _, c := range cases {
		aggregate := aggregateAnnotating(t, fmt.Sprintf(
			"feature.myco/monitoring:\n  %s: %s\n", c.subkey, c.value))
		err := authoredAnnotationRefusal(t, manifest, aggregate, nil, catalog)
		if c.wantErr && err == nil {
			t.Errorf("subkey %q=%v: expected a conformance halt; got nil", c.subkey, c.value)
		}
		if !c.wantErr && err != nil {
			t.Errorf("subkey %q=%v: expected pass; got %v", c.subkey, c.value, err)
		}
		if c.wantErr && err != nil && !stderrors.Is(err, ErrAnnotationSubkeyNotConforming()) {
			t.Errorf("subkey %q=%v: halt should carry the subkey-conformance condition; got %v",
				c.subkey, c.value, err)
		}
	}
}

// TestSubkeyConformance_TerminalRefResolved pins that a terminal sub-schema
// expressed as a single-document $ref is resolved against the schema's $defs and
// validated in full, rather than soft-skipped for carrying no `type` of its own.
func TestSubkeyConformance_TerminalRefResolved(t *testing.T) {
	t.Parallel()
	const schema = `$defs:
  Tier:
    type: string
    enum: [gold, silver]
values:
  level: { $ref: "#/$defs/Tier" }
`
	manifest := subkeyConformanceManifest(t)
	catalog := subkeyConformanceCatalog(t, schema)

	conforming := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.myco/monitoring:\n  level: gold\n"), nil, catalog)
	if conforming != nil {
		t.Errorf("conforming $ref value should pass; got %v", conforming)
	}
	refused := authoredAnnotationRefusal(t, manifest,
		aggregateAnnotating(t, "feature.myco/monitoring:\n  level: bronze\n"), nil, catalog)
	if refused == nil {
		t.Error("a terminal $ref enum violation must halt")
	}
}

// TestSubkeyConformance_NoCatalogSkips pins that the rule ends where the target
// Feature's contract cannot be read at all, rather than reporting the absence as
// a value the Feature refuses. A build resolving against no catalog has no
// contract to measure against, and a rule that halted on one would refuse every
// annotation in the document.
func TestSubkeyConformance_NoCatalogSkips(t *testing.T) {
	t.Parallel()
	err := authoredAnnotationRefusal(t, subkeyConformanceManifest(t),
		aggregateAnnotating(t, "feature.myco/monitoring:\n  level: bogus\n"),
		nil, CatalogLookup{})
	if err != nil {
		t.Errorf("no catalog means no contract to measure against; got %v", err)
	}
}
