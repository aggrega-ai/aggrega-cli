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
// primitive: every surface that needs to know what host functions a binary calls
// routes through it, and there is no parallel derivation anywhere.
//
// The .wasm import section is ground truth for which host functions the guest
// calls, so no authored list can drift from the binary. The record itself is
// machine-owned rather than authored: `aggrega plugin sync` derives it and writes
// it into the Plugin entity's spec.runtime.hostFunctions, publish verifies the
// committed record against the binary and refuses to publish a divergent pair,
// pull re-derives from the content-verified bytes and refuses a record that does
// not describe them, and the loader derives once more and registers exactly what
// it finds. Load never consults the record — the binary is ground truth at the
// moment of execution, and the record is the consume-side integrity claim about
// it, not a substitute for reading it.
//
// The canonical ordering makes a re-derive of the same .wasm byte-identical, so a
// record is stable across publishes and a record-vs-binary comparison is a plain
// set equality rather than a diff. Registration is order-independent, so sorting
// does not affect the bound host surface.
//
// It reuses the load gate's import-section parser and keeps only imports under
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
