// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// writeSchemaLibrary lays down one SchemaLibrary whose spec.schemas: block is
// body, indented as it sits under that key, and returns the catalog directory.
func writeSchemaLibrary(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	src := "apiVersion: aggrega.ai/v1beta1\n" +
		"kind: SchemaLibrary\n" +
		"metadata:\n" +
		"  name: " + name + "\n" +
		"  version: 1.0.0\n" +
		"spec:\n" +
		"  schemas:\n" +
		body
	if err := os.WriteFile(filepath.Join(dir, "library.yaml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return dir
}

// TestLoad_SchemaLibrary_UnsupportedFormatRefusedAtLoad pins that a SchemaLibrary
// entry naming a format outside the supported set is refused when the catalog
// loads, although no Component references it, under the condition a declared
// contract no validator can be built from raises, naming the library, the entry,
// the name and the supported set.
func TestLoad_SchemaLibrary_UnsupportedFormatRefusedAtLoad(t *testing.T) {
	t.Parallel()
	dir := writeSchemaLibrary(t, "Secrets", ""+
		"    Creds:\n"+
		"      type: object\n"+
		"      properties:\n"+
		"        password: { type: string, format: password }\n")
	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected the catalog load to refuse format: password; got nil")
	}
	if !errors.Is(err, resolver.ErrSchemaContractCompileFailure()) {
		t.Errorf("want errors.Is(err, ErrSchemaContractCompileFailure()); got %v", err)
	}
	// Read through the cause chain rather than the outermost frame. A catalog
	// load renders one frame per level by design, so the outermost says only
	// that a load failed; the frame naming the fault is beneath it, and the
	// flattened form is what a single-line context shows.
	chain := cerrors.CauseChain(err)
	for _, s := range []string{`SchemaLibrary "Secrets"`, `schema "Creds"`, `format "password" is not supported`,
		strings.Join(stringformat.Names(), ", ")} {
		if !strings.Contains(chain, s) {
			t.Errorf("the refusal does not carry %q:\n  %s", s, chain)
		}
	}
}

// TestLoad_SchemaLibrary_UnsupportedFormatNamedAtTheEntryThatAuthorsIt pins that
// an entry referencing another entry of its library is not refused for a name the
// referenced entry authors: the refusal names the entry that wrote it, even when
// the referencing entry comes first.
func TestLoad_SchemaLibrary_UnsupportedFormatNamedAtTheEntryThatAuthorsIt(t *testing.T) {
	t.Parallel()
	dir := writeSchemaLibrary(t, "Secrets", ""+
		"    Consumer:\n"+
		"      type: object\n"+
		"      properties:\n"+
		"        creds: { $ref: \"Secrets#/schemas/Creds\" }\n"+
		"    Creds:\n"+
		"      type: object\n"+
		"      properties:\n"+
		"        password: { type: string, format: password }\n")
	_, err := LoadAll("", dir)
	if err == nil {
		t.Fatal("expected the catalog load to refuse; got nil")
	}
	// The whole cause chain, as above: the entry is named in a frame beneath the
	// load's own, and no frame may name the referencing entry.
	chain := cerrors.CauseChain(err)
	if !strings.Contains(chain, `schema "Creds"`) || strings.Contains(chain, `schema "Consumer"`) {
		t.Errorf("the refusal should name the authoring entry Creds and not Consumer:\n  %s", chain)
	}
}

// TestLoad_SchemaLibrary_SupportedFormatsLoad is the positive control: every
// supported name loads.
func TestLoad_SchemaLibrary_SupportedFormatsLoad(t *testing.T) {
	t.Parallel()
	var body strings.Builder
	for i, name := range stringformat.Names() {
		body.WriteString("    Entry" + string(rune('A'+i)) + ":\n")
		body.WriteString("      type: object\n      properties:\n")
		body.WriteString("        field: { type: string, format: " + name + " }\n")
	}
	if _, err := LoadAll("", writeSchemaLibrary(t, "Formats", body.String())); err != nil {
		t.Fatalf("a library naming only supported formats must load; got %v", err)
	}
}
