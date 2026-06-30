// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// TestPublishingLinterCrateCompiles is the build gate for the
// production publishing-linter plugin crate. It force-compiles the
// crate to the wasm32 target on every run — bypassing the mtime
// freshness cache — so a crate that does not compile FAILS the Go
// suite directly, rather than hiding behind a stale prebuilt .wasm
// that a freshness check might still consider current.
//
// Skip contract: the gate skips ONLY when the wasm32 target or the
// Rust toolchain is absent (the workspace-setup script installs them).
// A genuine compile error is a FAIL, never a skip. After the wasm
// build succeeds the gate runs the crate's own Rust unit/integration
// tests (`cargo test`, host target) so a behavioural regression in a
// rule surfaces here too.
func TestPublishingLinterCrateCompiles(t *testing.T) {
	wasmtest.ForcePluginBuild(t, "publishing-linter")
	wasmtest.ForcePluginTest(t, "publishing-linter")
}
