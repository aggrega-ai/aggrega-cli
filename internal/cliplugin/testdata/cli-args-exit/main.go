// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture cli-args-exit plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It exercises the full CLI socket runner end-to-end: it self-describes as a CLI
// plugin, reads its argument vector from the cli_run input payload, streams two
// separate chunks to the host's stdout through the streaming host function
// aggrega_cli_write_stdout, and returns an exit code taken from its first
// argument. The two separate host-function calls prove the host streams each
// write to the injected writer as it arrives rather than buffering the whole run
// and dumping it once; the argument-derived exit code proves the guest's status
// propagates through the runner as a value.
//
// The guest imports aggrega_cli_write_stdout, so it loads only under the runtime
// CLI profile (Loader.LoadCLI); the compile-time build profile rejects the
// streaming import.
package main

import (
	"encoding/json"
	"strconv"

	pdk "github.com/extism/go-pdk"
)

// aggregaCLIWriteStdout is the host-imported binding for the CLI profile's
// stdout streaming host function: the host copies length bytes from guest memory
// at ptr and writes them to the injected stdout stream.
//
//go:wasmimport extism:host/user aggrega_cli_write_stdout
func aggregaCLIWriteStdout(ptr uint64, length uint64)

// cliDescriptor is the fixed describe() self-report: a CLI plugin whose single
// cli_run export imports the stdout streaming host function.
const cliDescriptor = `{"abi_version":"v1beta1","plugin_type":"cli","exports":[{"name":"cli_run","host_functions":["aggrega_cli_write_stdout"]}]}`

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
		Args []string `json:"args"`
	}
	if err := json.Unmarshal(pdk.Input(), &in); err != nil {
		writeStdout("decode-error\n")
		return 1
	}

	msg := ""
	if len(in.Args) >= 2 {
		msg = in.Args[1]
	}
	// Two separate streaming calls: the host delivers each to the injected
	// writer independently, so a recording writer observes two writes.
	writeStdout("arg:" + msg + "\n")
	writeStdout("second\n")

	if len(in.Args) >= 1 {
		if code, err := strconv.Atoi(in.Args[0]); err == nil {
			return int32(code)
		}
	}
	return 0
}

func main() {}
