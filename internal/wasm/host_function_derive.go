// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"sort"
)

// DeriveHostFunctions reads the host functions a plugin actually imports from
// the .wasm bytes themselves, returning the imported custom host-function names
// in canonical form: sorted and de-duplicated. It is the single derivation
// primitive — publish stamps the canonical set it returns, pull re-derives and
// verifies against the stamp, the loader registers the derived set, and the
// pre-publish tooling surfaces it. The .wasm import section is ground truth for
// which host functions the guest calls, so no authored list can drift from the
// binary.
//
// The canonical ordering makes a re-derive of the same .wasm byte-identical, so
// a stamp is stable across publishes and a stamp-vs-binary comparison at pull is
// a plain equality. Registration is order-independent, so sorting does not
// affect the bound host surface.
//
// It reuses the firewall's import-section parser and keeps only imports under
// the Extism custom-host namespace (extism:host/user). Imports under the Extism
// runtime ABI namespace (extism:host/env) and WASI (wasi_snapshot_preview1) are
// excluded: the former is the runtime's own memory/IO machinery, the latter the
// language runtime's startup surface — neither is an Aggrega curated host
// function. A plugin importing nothing under the custom-host namespace yields an
// empty set.
//
// Returns an error wrapping the parser's diagnostic when the bytes are not a
// well-formed Wasm module; pluginName names the plugin in that message.
func DeriveHostFunctions(pluginName string, wasmBytes []byte) ([]string, error) {
	imports, err := parseFunctionImports(wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: parsing wasm import section: %w", pluginName, err)
	}
	var derived []string
	seen := make(map[string]bool)
	for _, imp := range imports {
		if imp.Module != extismHostUserNamespace {
			continue
		}
		if seen[imp.Name] {
			continue
		}
		seen[imp.Name] = true
		derived = append(derived, imp.Name)
	}
	sort.Strings(derived)
	return derived, nil
}
