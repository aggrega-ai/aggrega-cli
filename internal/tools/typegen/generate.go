// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The reflect-free guest codec is generated into the sibling aggrega-abi
// module. The generator lives here in aggrega-cli; aggrega-abi carries no
// dependency on aggrega-cli, so the codec is emitted across the submodule
// boundary by path rather than driven from a directive inside aggrega-abi.
// Run from the umbrella checkout: `go generate ./internal/tools/typegen`.
//
//go:generate go run . ../../../../aggrega-abi/go/pkg/abi/frp_gen.go
