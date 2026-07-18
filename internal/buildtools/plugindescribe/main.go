// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Command plugindescribe extracts a Wasm plugin guest's describe() descriptor
// once at build time and writes it as JSON alongside the embedded guest, so the
// plugin listing can report the guest's static metadata without instantiating
// it. It loads the guest through the host's describe-only load gate — inert
// host-function stubs, describe() invoked and validated — and marshals the
// validated descriptor, so the extracted descriptor is byte-for-byte the shape a
// runtime describe() produces for the same guest.
//
// It is a build tool, not a shipped command: the embedded-guest build scripts
// invoke it after producing each guest's .wasm.gz and before the CLI is
// compiled, so the descriptor asset is present for the embed directive to
// capture. The emitted JSON is deterministic — the descriptor is composed of
// structs and slices in declared order with no maps, so encoding/json produces
// identical bytes for identical input.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// describeMaxMemoryPages is the linear-memory cap applied for the describe()
// call. It matches the ceiling the plugin listing raises so the largest
// built-in provider guests instantiate for a describe(); the descriptor is a
// static self-report, so the cap only needs to admit the guest, not size any
// render.
const describeMaxMemoryPages uint32 = 4096

func main() {
	wasmPath := flag.String("wasm", "", "path to the guest .wasm to describe")
	name := flag.String("name", "", "diagnostic name for the guest")
	out := flag.String("out", "", "path to write the descriptor JSON to")
	flag.Parse()

	if *wasmPath == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "plugindescribe: -wasm and -out are required")
		os.Exit(2)
	}

	if err := run(*wasmPath, *name, *out); err != nil {
		fmt.Fprintf(os.Stderr, "plugindescribe: %v\n", err)
		os.Exit(1)
	}
}

// run loads the guest at wasmPath, invokes its describe() export through the
// host's describe-only load gate, and writes the validated descriptor as JSON to
// out.
func run(wasmPath, name, out string) error {
	bytes, err := os.ReadFile(wasmPath)
	if err != nil {
		return fmt.Errorf("read guest: %w", err)
	}

	ctx := context.Background()
	loader := wasm.NewCachingLoader(nil)
	plugin, desc, err := loader.LoadForDescribe(ctx, wasm.PluginSpec{
		Name:           name,
		Bytes:          bytes,
		MaxMemoryPages: describeMaxMemoryPages,
	})
	if err != nil {
		return fmt.Errorf("describe guest: %w", err)
	}
	_ = plugin.Close(ctx)

	data, err := json.Marshal(desc)
	if err != nil {
		return fmt.Errorf("marshal descriptor: %w", err)
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write descriptor: %w", err)
	}
	return nil
}
