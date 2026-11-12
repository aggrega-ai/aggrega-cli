// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The view stages and the derived subject-kind set are generated into the parent
// package. Both are emitted by path rather than driven from a directive beside
// the output, because the registry is the single source and one directive set
// keeps every projection of it in step.
//
// The generator renders one target per invocation, chosen by the output path's
// base name, so a target whose committed copy lives outside this module is
// produced by passing that path rather than by a directive here: a directive
// naming a path outside the module would fail outright in a checkout of this
// repository alone, where that path does not exist.
//
// Run from this module's root:
// `go generate ./internal/compiler/constraint/factgen`.
//
//go:generate go run . ../views_gen.go
//go:generate go run . ../subjectkinds_gen.go
