// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build wasip1

// Fixture cli-stream-probe plugin for the Aggrega Wasm runtime, built with the
// standard Go toolchain for GOOS=wasip1.
//
// It exercises the runtime CLI instantiation profile's streaming host function
// aggrega_cli_write_stdout end-to-end: the cli_run export writes a fixed string
// to the host's injected stdout stream via the host function and returns exit
// code 0. The Extism Go PDK supplies the memory allocation and reactor
// conventions; the streaming host function is imported directly because it is
// granted only under the CLI profile.
//
// The guest imports aggrega_cli_write_stdout, so a load through the compile-time
// build profile (Loader.Load) rejects it — the streaming name is not on the
// shared allowlist — while a load through the runtime CLI profile
// (Loader.LoadCLI) accepts it.
//
// Build: GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -o cli-stream-probe.wasm ./
package main

import (
	pdk "github.com/extism/go-pdk"
)

// aggregaCLIWriteStdout is the host-imported binding for the CLI profile's
// stdout streaming host function. Signature matches the host-side declaration:
// (ptr i64, len i64); the host copies len bytes from guest memory at ptr and
// writes them to the injected stdout stream.
//
//go:wasmimport extism:host/user aggrega_cli_write_stdout
func aggregaCLIWriteStdout(ptr uint64, length uint64)

//go:wasmexport cli_run
func cli_run() int32 {
	mem := pdk.AllocateString("streamed-cli-output\n")
	defer mem.Free()
	aggregaCLIWriteStdout(uint64(mem.Offset()), uint64(mem.Length()))
	return 0
}

func main() {}
