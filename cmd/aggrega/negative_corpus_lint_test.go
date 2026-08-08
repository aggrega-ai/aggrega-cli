// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Negative-authoring corpus — lint surface. Each catalog entity kind has
// a base-valid document and a set of single-fault violations (unknown
// key, retired/typo'd key, wrong shape, missing required). Every
// violation is driven through the REAL whole-catalog lint path
// (validateCatalogWide → catalog.LoadAllWithCacheDir → the envelope gate),
// never a standalone schema harness, and asserts the envelope sentinel
// via errors.Is.
//
// The positive control (TestNegativeCorpusLint_BaseValid) proves every
// base document lints CLEAN before any fault is injected — so each
// violation row fails on its OWN single fault, not on an unintended
// second defect in the base. A green row with a dirty base would pin
// nothing; the positive control is what makes the corpus trustworthy.
package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// lintBases is the base-valid document per catalog entity kind. Each
// lints clean on its own (no external $ref, no recipe binding required
// by whole-catalog lint). Violations are produced by replacing a base
// with a single-fault variant in the row table below.
var lintBases = map[string]string{
	"Component": `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
`,
	"Feature": `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/data-masking
  version: "1.0.0"
spec:
  appliesTo: component.aggrega.ai/type=server
  schema:
    values:
      level: { type: string }
`,
	"Recipe": `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb
  version: "1.0.0"
spec:
  capability: compose
  implements: core/datastore/mariadb
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: x:1
`,
	"FeatureRecipe": `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: dotenv-secret
  version: "1.0.0"
spec:
  capability: compose
  implements: core/secret-value
  injectMapping: |
    env:
      X: "y"
`,
	"SchemaLibrary": `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec:
  schemas:
    Base:
      type: object
      properties:
        x: { type: string }
`,
	"MappingLibrary": `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: "1.0.0"
spec:
  mappings:
    Frag: "key: value"
`,
}

// lintRow is one single-fault violation: kind names the base to mutate,
// doc is the full violating document, and want is the sentinel the real
// lint path must surface.
type lintRow struct {
	name string
	kind string
	doc  string
	want error
}

func lintCorpusRows() []lintRow {
	return []lintRow{
		// Unknown key at spec (the silent-drop class made loud).
		{"component-unknown-spec-key", "Component", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  bogusKey: nope
  schema:
    values:
      port: { type: integer }
`, envelope.ErrEntityEnvelope()},
		// Retired key (secretNames) — the exact hole the gate was built to close.
		{"component-retired-secretNames", "Component", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  secretNames: [old_key]
  schema:
    values:
      port: { type: integer }
`, envelope.ErrEntityEnvelope()},
		// Missing required metadata field (version).
		{"component-missing-version", "Component", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer }
`, envelope.ErrEntityEnvelope()},
		// *From: family — banned by spec-level closure, rejected as an
		// unknown key (no dedicated *From sentinel exists; the mechanism
		// IS unknown-key rejection).
		{"component-valueFrom-family", "Component", `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  valueFrom: somewhere
  schema:
    values:
      port: { type: integer }
`, envelope.ErrEntityEnvelope()},
		// Wrong envelope shape: spec authored as a list, not a map.
		{"feature-wrong-spec-shape", "Feature", `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/data-masking
  version: "1.0.0"
spec:
  - not
  - a
  - map
`, envelope.ErrEntityEnvelope()},
		// Typo'd key on a Recipe spec.
		{"recipe-typo-key", "Recipe", `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb
  version: "1.0.0"
spec:
  capability: compose
  implments: core/datastore/mariadb
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: x:1
`, envelope.ErrEntityEnvelope()},
		// SchemaLibrary missing required (schemas).
		{"schemalibrary-missing-schemas", "SchemaLibrary", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: "1.0.0"
spec: {}
`, envelope.ErrEntityEnvelope()},
		// MappingLibrary unknown key.
		{"mappinglibrary-unknown-key", "MappingLibrary", `apiVersion: aggrega.ai/v1alpha1
kind: MappingLibrary
metadata:
  name: BaseMappings
  version: "1.0.0"
spec:
  mappings:
    Frag: "key: value"
  bogus: x
`, envelope.ErrEntityEnvelope()},
	}
}

// TestNegativeCorpusLint_BaseValid is the positive control: every base
// document lints CLEAN. A violation row is trustworthy only if its base
// is valid except for the single injected fault — this proves the bases.
func TestNegativeCorpusLint_BaseValid(t *testing.T) {
	t.Parallel()
	for kind, doc := range lintBases {
		kind, doc := kind, doc
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "entity.yaml"), []byte(doc), 0o644); err != nil {
				t.Fatalf("write base: %v", err)
			}
			if err := validateCatalogWide("", "", []string{dir}, os.Stderr, os.Stdout); err != nil {
				t.Fatalf("base %s should lint clean (positive control); got: %v", kind, err)
			}
		})
	}
}

// TestNegativeCorpusLint_Violations drives each single-fault document
// through the real whole-catalog lint path and asserts the envelope
// sentinel. errors.Is, never message-substring — a named sentinel exists.
func TestNegativeCorpusLint_Violations(t *testing.T) {
	t.Parallel()
	for _, r := range lintCorpusRows() {
		r := r
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "entity.yaml"), []byte(r.doc), 0o644); err != nil {
				t.Fatalf("write violation: %v", err)
			}
			err := validateCatalogWide("", "", []string{dir}, os.Stderr, os.Stdout)
			if err == nil {
				t.Fatalf("%s: expected the lint path to reject; got nil", r.name)
			}
			if !errors.Is(err, r.want) {
				t.Fatalf("%s: want errors.Is(err, %v); got %v", r.name, r.want, err)
			}
		})
	}
}
