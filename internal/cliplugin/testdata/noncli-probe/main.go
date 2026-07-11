// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture noncli-probe plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It self-describes as a provider plugin — a non-CLI type — so the CLI socket
// runner loads and describes it successfully but then refuses to run it as a
// command. It imports no host functions, so it loads under either profile; only
// its declared plugin type distinguishes it. Its render_node export is present
// so the descriptor passes the provider minimum-export rule; the runner never
// invokes it, because the type check refuses the plugin first.
package main

import (
	pdk "github.com/extism/go-pdk"
)

// providerDescriptor is the fixed describe() self-report: a provider plugin with
// the required render_node export and no host-function imports.
const providerDescriptor = `{"abi_version":"v1alpha1","plugin_type":"provider","compatible_providers":["compose"],"exports":[{"name":"render_node"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(providerDescriptor)
	return 0
}

//go:wasmexport render_node
func render_node() int32 {
	pdk.OutputString("{}")
	return 0
}

func main() {}
