// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture cli-install-guest plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It self-describes as a CLI plugin that names the command it contributes
// (command "moo") and carries a one-line description used as the command's short
// help. It imports no host functions, so it loads under either instantiation
// profile; the install path describes it to read the command name it registers.
// Its cli_run export is present so the descriptor passes the CLI minimum-export
// rule; the install path never invokes it, reading only the describe() output.
package main

import (
	pdk "github.com/extism/go-pdk"
)

// cliDescriptor is the fixed describe() self-report: a CLI plugin that names its
// command and its short help, with the required cli_run export and no
// host-function imports.
const cliDescriptor = `{"abi_version":"v1alpha1","plugin_type":"cli","command":"moo","description":"Print a friendly greeting","exports":[{"name":"cli_run"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(cliDescriptor)
	return 0
}

//go:wasmexport cli_run
func cli_run() int32 {
	pdk.OutputString("moo\n")
	return 0
}

func main() {}
