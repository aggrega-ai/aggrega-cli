// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture cli-multi-guest plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It is one CLI plugin backing two commands, "greet-a" and "greet-b", proving a
// single guest routes on the invoked command name. It self-describes both
// commands and one cli_run export; at invocation it reads the resolved command
// name from the cli_run input and streams a different line per command through
// the streaming host function aggrega_cli_write_stdout. The branch it takes on
// the discriminator is the proof the host delivered the correct command name.
//
// The guest imports aggrega_cli_write_stdout, so it loads only under the runtime
// CLI profile (Loader.LoadCLI); the compile-time build profile rejects the
// streaming import.
package main

import (
	"encoding/json"

	pdk "github.com/extism/go-pdk"
)

// aggregaCLIWriteStdout is the host-imported binding for the CLI profile's
// stdout streaming host function: the host copies length bytes from guest memory
// at ptr and writes them to the injected stdout stream.
//
//go:wasmimport extism:host/user aggrega_cli_write_stdout
func aggregaCLIWriteStdout(ptr uint64, length uint64)

// cliDescriptor is the fixed describe() self-report: a CLI plugin declaring two
// commands and one cli_run export that imports the stdout streaming host
// function. It self-reports a build version so a Plugin entity authoring the same
// metadata.version cross-checks clean at publish.
const cliDescriptor = `{"abi_version":"v1beta1","plugin_type":"cli","commands":[{"name":"greet-a","description":"Greet in style A"},{"name":"greet-b","description":"Greet in style B"}],"exports":[{"name":"cli_run","host_functions":["aggrega_cli_write_stdout"]}],"metadata":{"version":"1.0.0"}}`

//go:wasmexport describe
func describe() int32 {
	pdk.OutputString(cliDescriptor)
	return 0
}

func writeStdout(s string) {
	mem := pdk.AllocateString(s)
	defer mem.Free()
	aggregaCLIWriteStdout(uint64(mem.Offset()), uint64(mem.Length()))
}

//go:wasmexport cli_run
func cli_run() int32 {
	var in struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(pdk.Input(), &in); err != nil {
		writeStdout("decode-error\n")
		return 1
	}
	switch in.Command {
	case "greet-a":
		writeStdout("hello from A\n")
	case "greet-b":
		writeStdout("hello from B\n")
	default:
		writeStdout("unknown command: " + in.Command + "\n")
		return 1
	}
	return 0
}

func main() {}
