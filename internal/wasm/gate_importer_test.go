// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"errors"
	"testing"
)

// TestPluginDescriptorValidateImporter proves the importer minimum-export gate:
// an importer descriptor declaring the import export passes Validate, and the
// same descriptor without it is refused wrapping ErrWasmManifestRejected. The
// required export name is read from ImportExport so the test and the registry
// name one export rather than each re-spelling the literal.
func TestPluginDescriptorValidateImporter(t *testing.T) {
	t.Parallel()

	// An importer descriptor with the import export passes Validate.
	withImport := PluginDescriptor{
		AbiVersion:   ABIVersion,
		PluginType:   string(PluginTypeImporter),
		Capabilities: []string{"import:terraform"},
		Exports:      []PluginExport{{Name: ImportExport.Name()}},
	}
	if err := withImport.Validate("acme/importer"); err != nil {
		t.Fatalf("importer with %q: want accept, got %v", ImportExport, err)
	}

	// The same descriptor without the import export fails, wrapping
	// ErrWasmManifestRejected.
	withoutImport := PluginDescriptor{
		AbiVersion:   ABIVersion,
		PluginType:   string(PluginTypeImporter),
		Capabilities: []string{"import:terraform"},
		Exports:      []PluginExport{{Name: "read"}},
	}
	if err := withoutImport.Validate("acme/importer"); !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("importer without %q: want ErrWasmManifestRejected, got %v", ImportExport, err)
	}
}
