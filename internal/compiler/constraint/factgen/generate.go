// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The view stages and the derived subject-kind set are generated into the parent
// package. Both are emitted by path rather than driven from a directive beside
// the output, because the registry is the single source and one directive set
// keeps every projection of it in step.
//
// The generator renders one target per invocation, chosen by the output path's
// base name, so a target whose committed copy lives outside this module is
// produced by passing that path rather than by a directive here. Its drift is
// gated by the workspace build, which has every tree present; a directive naming
// a path outside the module would fail outright in a standalone checkout of this
// repository, where that path does not exist.
//
// Run from the umbrella checkout:
// `go generate ./internal/compiler/constraint/factgen`.
//
//go:generate go run . ../views_gen.go
//go:generate go run . ../subjectkinds_gen.go
