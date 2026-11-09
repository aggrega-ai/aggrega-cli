// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the cross-document $ref lookup perspective on the catalog's
// SchemaLibraries map. The compactform compiler resolves cross-document
// $ref URIs (e.g. "BaseComponents#/schemas/BaseComponentParameter") by
// consulting Catalog.SchemaLibraries[<library-name>] directly. These tests
// pin the contract that backs that resolution: a library loaded into the
// catalog is reachable by its PascalCase metadata.name; an unregistered
// name resolves to a zero-value + ok=false the compactform expander treats
// as ErrUnresolvedSchemaLibraryRef.
//
// The cross-document-lookup contract is small (one map read) but load-
// bearing — without it the compactform expander has no way to find a
// referenced library at compile time and the cross-document $ref runtime
// gap remains open.

package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSchemaLibraryLookup_ByMetadataName(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libYAML := []byte(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: 1.0.0
spec:
  schemas:
    BaseComponentParameter:
      type: object
      properties:
        displayName:
          type: string
        description:
          type: string
      required: [displayName]
      additionalProperties: false
    Probe:
      type: object
      properties:
        port:
          type: integer
        path:
          type: string
      additionalProperties: false
`)
	if err := os.WriteFile(filepath.Join(dir, "base-components.yaml"), libYAML, 0o644); err != nil {
		t.Fatalf("write library fixture: %v", err)
	}

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got := len(c.SchemaLibraries); got != 1 {
		t.Fatalf("SchemaLibraries length = %d, want 1", got)
	}

	lib, ok := c.SchemaLibraries["BaseComponents"]
	if !ok {
		t.Fatalf("BaseComponents not registered in SchemaLibraries; keys = %v", schemaLibraryKeys(c))
	}
	if lib.Metadata.Name != "BaseComponents" {
		t.Errorf("Metadata.Name = %q, want BaseComponents", lib.Metadata.Name)
	}
	if lib.Metadata.Version != "1.0.0" {
		t.Errorf("Metadata.Version = %q, want 1.0.0", lib.Metadata.Version)
	}
	if got := lib.Spec.Schemas.Len(); got != 2 {
		t.Errorf("Spec.Schemas length = %d, want 2", got)
	}
	if _, ok := lib.Spec.Schemas.Get("BaseComponentParameter"); !ok {
		t.Error("BaseComponentParameter entry not found in library")
	}
	if _, ok := lib.Spec.Schemas.Get("Probe"); !ok {
		t.Error("Probe entry not found in library")
	}
}

func TestSchemaLibraryLookup_UnregisteredNameReturnsZeroValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libYAML := []byte(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: WebPrimitives
  version: 1.0.0
spec:
  schemas:
    URL:
      type: string
      pattern: "^https?://"
`)
	if err := os.WriteFile(filepath.Join(dir, "web-primitives.yaml"), libYAML, 0o644); err != nil {
		t.Fatalf("write library fixture: %v", err)
	}

	c, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	_, ok := c.SchemaLibraries["UnregisteredLibrary"]
	if ok {
		t.Error("lookup of unregistered library name returned ok=true; want false")
	}
}

func schemaLibraryKeys(c *Catalog) []string {
	keys := make([]string, 0, len(c.SchemaLibraries))
	for k := range c.SchemaLibraries {
		keys = append(keys, k)
	}
	return keys
}
