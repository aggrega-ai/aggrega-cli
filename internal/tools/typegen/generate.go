// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The reflect-free guest codec is generated into the sibling aggrega-abi
// module. The generator lives here in aggrega-cli; aggrega-abi carries no
// dependency on aggrega-cli, so the codec is emitted across the repository
// boundary by path rather than driven from a directive inside aggrega-abi, and
// the directives below need the aggrega-abi repository checked out beside this
// one. Run from this module's root: `go generate ./internal/tools/typegen`.
//
// Every Rust codec target is driven from here. The Rust crate is lint-gated as a
// whole — the gate reads the crate, not a per-file list — so a Rust target left
// out of this set is a file the gate still judges and no command refreshes. The
// gap is not visible at the gate either: the omitted file simply keeps whatever
// the generator emitted when someone last invoked it by path, and reads as
// current. So a new Rust target is added here in the same edit that registers it.
//
// Every codec target committed inside this module is driven from here as well.
// No directive anywhere else names one, so a target left out of this set is
// refreshed by no batch command at all: `go generate ./internal/tools/typegen/...`
// exits zero without touching it, and the file keeps whatever the generator
// emitted when someone last invoked it by path. So a new in-module target is
// added here in the same edit that registers it.
//
//go:generate go run . ../../../../aggrega-abi/rust/src/cbor_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/cbor_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/orderedmap_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/orderedmap_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/orderedmap_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/frp_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/frp_gen.rs
//go:generate go run . ../../compiler/featurerecipe_pipeline/frp_host_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/describe_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/describe_gen.rs
//go:generate go run . ../../wasm/describe_host_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/parse_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/parse_gen.rs
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/import_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/import_gen.rs
//go:generate go run . ../../../../aggrega-abi/rust/src/cli_gen.rs
//go:generate go run . ../../../../aggrega-abi/rust/src/linter_gen.rs
//go:generate go run . ../../../pkg/validator/linter_host_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/mutator_gen.rs
//go:generate go run . ../../compiler/topology/wasm_types_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/policy_gen.rs
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/hostfunc_error_enums_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/hostfunc_error_enums_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/hostfunc_error_enums_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/abi_version_enums_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/abi_version_enums_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/abi_version_enums_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/annotationtransform_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/annotationtransform_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/annotationtransform_gen.ts
//go:generate go run . ../../compiler/pipeline/annotationtransform_host_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/diagnostic_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/diagnostic_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/diagnostic_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/diagnostic_enums_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/diagnostic_enums_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/diagnostic_enums_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/ir_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/ir_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/ir_gen.ts
//go:generate go run . ../../../pkg/types/ir_host_gen.go
//go:generate go run . ../../compiler/analyzer/policy_host_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/providerrender_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/providerrender_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/providerrender_gen.ts
//go:generate go run . ../../compiler/rendercore/providerrender_host_gen.go
//go:generate go run . ../../pluginwire/pluginwire_gen.go
//go:generate go run . ../../wasm/hostexport_gen.go
//go:generate go run . ../../../../aggrega-abi/typescript/src/servewire_gen.ts
//go:generate go run . ../../apiversion/apiversion_gen.go
