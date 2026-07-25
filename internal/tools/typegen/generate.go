// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The reflect-free guest codec is generated into the sibling aggrega-abi
// module. The generator lives here in aggrega-cli; aggrega-abi carries no
// dependency on aggrega-cli, so the codec is emitted across the submodule
// boundary by path rather than driven from a directive inside aggrega-abi.
// Run from the umbrella checkout: `go generate ./internal/tools/typegen`.
//
//go:generate go run . ../../../../aggrega-abi/rust/src/cbor_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/cbor_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/frp_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/describe_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/parse_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/import_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/hostfunc_error_enums_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/hostfunc_error_enums_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/hostfunc_error_enums_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/annotationtransform_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/annotationtransform_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/annotationtransform_gen.ts
//go:generate go run . ../../compiler/pipeline/annotationtransform_host_gen.go
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/diagnostic_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/diagnostic_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/diagnostic_gen.ts
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/ir_gen.go
//go:generate go run . ../../../../aggrega-abi/rust/src/ir_gen.rs
//go:generate go run . ../../../../aggrega-abi/typescript/src/ir_gen.ts
