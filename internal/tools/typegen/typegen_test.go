// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRegenClean is the regen-drift gate: for every registered kind, the
// generator's output must byte-match the committed <kind>_gen.go in pkg/types.
// A failure means the schema (or the generator) changed without regenerating —
// run `go generate ./pkg/types/...` and commit. One command covers every kind,
// so kind-batch migrations and CI self-verify determinism without bespoke steps.
func TestRegenClean(t *testing.T) {
	for outFile, src := range kindSources {
		t.Run(outFile, func(t *testing.T) {
			got, err := generate(src.typeName, src.schema)
			if err != nil {
				t.Fatalf("generate %s: %v", src.typeName, err)
			}
			path := filepath.Join("..", "..", "..", "pkg", "types", outFile)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate ./pkg/types/...` and commit the result.", outFile)
			}
		})
	}
}

// TestRegenClean_Enums is the regen-drift gate for the string-enum-const
// targets: the generator's output must byte-match the committed
// <kind>_enums_gen.go in pkg/types. A failure means the schema's enum (or the
// generator) changed without regenerating — run `go generate ./pkg/types/...`
// and commit. This extends the entity regen-drift contract to the enum-const
// spine, so the consts a consumer aliases stay in lockstep with the schema by
// construction.
func TestRegenClean_Enums(t *testing.T) {
	for outFile := range enumSources {
		t.Run(outFile, func(t *testing.T) {
			got, err := generateFor(outFile)
			if err != nil {
				t.Fatalf("generate enums %s: %v", outFile, err)
			}
			dir, ok := enumTargetPaths[outFile]
			if !ok {
				t.Fatalf("no committed path registered for enum target %q", outFile)
			}
			path := filepath.Join(append(append([]string{}, dir...), outFile)...)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Regenerate the target and commit the result.", outFile)
			}
		})
	}
}

// enumTargetPaths maps each enum target's output basename to the directory
// (relative to this test package) holding the committed generated file. The
// Plugin entity's enum consts live in pkg/types; the host-function error-code
// consts are committed across the aggrega-abi submodule boundary (Go/Rust/TS),
// reached up to the umbrella root then into aggrega-abi, and read with
// os.ReadFile so the cross-module read needs no go.work membership.
var enumTargetPaths = map[string][]string{
	"plugin_enums_gen.go":         {"..", "..", "..", "pkg", "types"},
	"hostfunc_error_enums_gen.go": {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"hostfunc_error_enums_gen.rs": {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"hostfunc_error_enums_gen.ts": {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
}

// codecTargetPaths maps each codec target's output basename to the directory
// (relative to this test package) holding the committed generated file. The
// regen-drift gate reads the committed file there and byte-compares it to a
// fresh generation. The guest codec is committed in the sibling aggrega-abi
// module, so its path reaches across the submodule boundary (up to the
// umbrella root, then into aggrega-abi); the gate reads it with os.ReadFile,
// not a Go import, so the cross-module read needs no go.work membership.
var codecTargetPaths = map[string][]string{
	"frp_gen.go":            {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"frp_gen.rs":            {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"frp_gen.ts":            {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"cli_gen.go":            {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"cli_gen.rs":            {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"cli_gen.ts":            {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"describe_gen.go":       {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"describe_gen.rs":       {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"describe_gen.ts":       {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"mutator_gen.go":        {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"mutator_gen.rs":        {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"mutator_gen.ts":        {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"annotationtransform_gen.go": {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"annotationtransform_gen.rs": {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"annotationtransform_gen.ts": {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"linter_gen.go":         {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"linter_gen.rs":         {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"linter_gen.ts":         {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"ir_gen.go":             {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"ir_gen.rs":             {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"ir_gen.ts":             {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"policy_gen.go":         {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"policy_gen.rs":         {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"policy_gen.ts":         {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"providerrender_gen.go": {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"providerrender_gen.rs": {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"providerrender_gen.ts": {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"parse_gen.go":          {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"parse_gen.rs":          {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"parse_gen.ts":          {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	"import_gen.go":         {"..", "..", "..", "..", "aggrega-abi", "go", "pkg", "abi"},
	"import_gen.rs":         {"..", "..", "..", "..", "aggrega-abi", "rust", "src"},
	"import_gen.ts":         {"..", "..", "..", "..", "aggrega-abi", "typescript", "src"},
	// The host describe codec is committed inside aggrega-cli's wasm package.
	"describe_host_gen.go": {"..", "..", "wasm"},
	// The host mutator codec is committed inside aggrega-cli's topology package.
	"wasm_types_gen.go": {"..", "..", "compiler", "topology"},
	// The host annotation-transform codec is committed inside aggrega-cli's pipeline package.
	"annotationtransform_host_gen.go": {"..", "..", "compiler", "pipeline"},
	// The host linter codec is committed inside aggrega-cli's validator package.
	"linter_host_gen.go": {"..", "..", "..", "pkg", "validator"},
	// The host provider render codec is committed inside aggrega-cli's rendercore package.
	"providerrender_host_gen.go": {"..", "..", "compiler", "rendercore"},
}

// TestRegenClean_Codec is the regen-drift gate for the multi-type codec
// targets: re-running the generator for each codec target must byte-match the
// committed generated file. A failure means the schema or the generator
// changed without regenerating — run `go generate` for the target package and
// commit. This extends the entity regen-drift contract to the codec spine so
// the reflect-free codec self-verifies determinism alongside the entity types.
func TestRegenClean_Codec(t *testing.T) {
	for outFile := range codecTargets {
		t.Run(outFile, func(t *testing.T) {
			got, err := generateFor(outFile)
			if err != nil {
				t.Fatalf("generate codec %s: %v", outFile, err)
			}
			dir, ok := codecTargetPaths[outFile]
			if !ok {
				t.Fatalf("no committed path registered for codec target %q", outFile)
			}
			path := filepath.Join(append(append([]string{}, dir...), outFile)...)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read committed %s: %v", path, err)
			}
			if string(got) != string(want) {
				t.Errorf("%s is stale: generator output differs from the committed file.\n"+
					"Run `go generate` for the target package and commit the result.", outFile)
			}
		})
	}
}
