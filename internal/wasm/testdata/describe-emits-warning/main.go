// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture describe-emits-warning plugin for the Aggrega Wasm runtime, built with
// the standard Go toolchain for GOOS=wasip1.
//
// Its describe() export calls the host function aggrega_emit_warning before
// returning its descriptor, so a warning is recorded on the host only when the
// real host function is bound. It exists to prove the describe-only introspection
// load path binds inert stubs: under that path describe() calls the stub, no
// warning is recorded, and the plugin's warning channel stays empty; under the
// build profile's real host functions the same describe() records the warning.
//
// The guest imports aggrega_emit_warning, a shared-allowlist host function, so it
// loads under both the build profile and the describe-only profile — the shared
// import is what lets the two paths be compared against the same guest.
package main

import (
	pdk "github.com/extism/go-pdk"
)

// aggregaEmitWarning is the host-imported binding for the structured-warning
// emitter: the host reads three strings (code, message, path) from guest memory
// at the given offsets and, when the real host function is bound, records the
// warning on the plugin's warning channel.
//
//go:wasmimport extism:host/user aggrega_emit_warning
func aggregaEmitWarning(codePtr uint64, messagePtr uint64, pathPtr uint64)

// linterDescriptor is the fixed describe() self-report: a linter plugin whose
// single lint_probe export declares the aggrega_emit_warning host function it
// imports, so the load gate's describe()-vs-imports cross-check passes.
const linterDescriptor = `{"abi_version":"v1alpha1","plugin_type":"linter","exports":[{"name":"lint_probe","host_functions":["aggrega_emit_warning"]}]}`

func emitWarning(code, message, path string) {
	codeMem := pdk.AllocateString(code)
	defer codeMem.Free()
	messageMem := pdk.AllocateString(message)
	defer messageMem.Free()
	pathMem := pdk.AllocateString(path)
	defer pathMem.Free()
	aggregaEmitWarning(uint64(codeMem.Offset()), uint64(messageMem.Offset()), uint64(pathMem.Offset()))
}

//go:wasmexport describe
func describe() int32 {
	// The observable side effect: a warning that is recorded on the host only
	// when the real host function is bound, not when an inert stub is.
	emitWarning("probe", "describe-side-effect", "probe.yaml")
	pdk.OutputString(linterDescriptor)
	return 0
}

//go:wasmexport lint_probe
func lint_probe() int32 {
	return 0
}

func main() {}
