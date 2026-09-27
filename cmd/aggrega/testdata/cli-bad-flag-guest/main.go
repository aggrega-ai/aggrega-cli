// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Fixture cli-bad-flag-guest plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It self-describes as a CLI plugin contributing two commands. "greet-ok"
// declares a well-formed flag; "greet-bad" declares a flag whose shorthand is two
// characters long, which the host cannot declare for help. Registering the
// plugin from a cold describe cache therefore describes this guest and reads
// both commands from its self-report, and the host's flag check skips the
// plugin whole. It imports no host functions, so it loads under the
// describe-only profile; its cli_run export is present so the descriptor passes
// the CLI minimum-export rule and is never invoked.
package main

import (
	pdk "github.com/extism/go-pdk"
)

// cliDescriptor is the fixed describe() self-report: two commands, one of which
// declares a flag with the shorthand "vv".
const cliDescriptor = `{"abi_version":"v1alpha1","plugin_type":"cli","commands":[{"name":"greet-ok","description":"Greet with a well-formed flag","flags":[{"name":"ok","shorthand":"o","type":"bool"}]},{"name":"greet-bad","description":"Greet with a malformed flag","flags":[{"name":"verbose","shorthand":"vv","description":"talk more"}]}],"exports":[{"name":"cli_run"}]}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(cliDescriptor)
	return 0
}

// cli_run is a never-invoked placeholder present only to satisfy the CLI
// minimum-export rule for the descriptor read.
//
//go:wasmexport cli_run
func cli_run() int32 {
	return 0
}

func main() {}
