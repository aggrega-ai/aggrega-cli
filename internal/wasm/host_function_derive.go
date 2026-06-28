// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"fmt"
	"sort"
	"strings"
)

// deriveDeclaredHostFunctions reads the host functions a plugin actually imports
// from the .wasm bytes themselves, returning the imported custom host-function
// names in import order, de-duplicated. The derived set is the authoritative
// input the loader registers host-function closures for and verifies against the
// curated allowlist — the .wasm import section is ground truth for which host
// functions the guest calls, rather than an authored list that could drift from
// the binary.
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
func deriveDeclaredHostFunctions(pluginName string, wasmBytes []byte) ([]string, error) {
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
	return derived, nil
}

// crossCheckAuthoredHostFunctions verifies an authored host-function
// declaration agrees with the set derived from the .wasm bytes, returning a
// Loud Guardrail error wrapping [ErrWasmHostFunctionsDeclarationMismatch] when
// the two sets differ as sets (order and duplicates ignored) and nil when they
// agree. The error names the plugin, the authored-but-not-imported names (the
// declaration claims a host function the binary does not call) and the
// imported-but-not-declared names (the binary calls a host function the
// declaration omits), each sorted for a build-invariant message.
//
// The authored declaration is a deliberate independent cross-check of the
// derived truth, sanctioned despite the apparent duplication: it lets an entity
// author state the host surface they expect and catch a binary that drifts from
// it. The caller invokes this only when the authored declaration is non-empty.
func crossCheckAuthoredHostFunctions(pluginName string, authored, derived []string) error {
	authoredSet := make(map[string]bool, len(authored))
	for _, n := range authored {
		authoredSet[n] = true
	}
	derivedSet := make(map[string]bool, len(derived))
	for _, n := range derived {
		derivedSet[n] = true
	}

	var authoredNotImported []string
	for n := range authoredSet {
		if !derivedSet[n] {
			authoredNotImported = append(authoredNotImported, n)
		}
	}
	var importedNotDeclared []string
	for n := range derivedSet {
		if !authoredSet[n] {
			importedNotDeclared = append(importedNotDeclared, n)
		}
	}
	if len(authoredNotImported) == 0 && len(importedNotDeclared) == 0 {
		return nil
	}
	sort.Strings(authoredNotImported)
	sort.Strings(importedNotDeclared)
	return fmt.Errorf(
		"plugin %q declared host functions disagree with the .wasm imports "+
			"(declared-but-not-imported: [%s]; imported-but-not-declared: [%s]): %w",
		pluginName, strings.Join(authoredNotImported, ", "), strings.Join(importedNotDeclared, ", "),
		ErrWasmHostFunctionsDeclarationMismatch)
}
