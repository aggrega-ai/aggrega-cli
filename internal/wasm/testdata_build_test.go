// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// ensureFixtureBuilt is a thin wrapper around the shared
// wasmtest.EnsureFixtureBuilt helper. The shared package generalises
// the build-on-demand logic across two source layouts: PDK fixtures
// at aggrega-pdk/<lang>/examples/<socket>/ AND production plugins at
// aggrega-cli/plugins/<name>/. The integration tests in this
// package consume only the PDK-fixture layout.
//
// The wrapper preserves the original signature so the existing
// callers in loader_integration_test.go do not need to import the
// shared package directly.
func ensureFixtureBuilt(t *testing.T, artifactName string) string {
	t.Helper()
	return wasmtest.EnsureFixtureBuilt(t, artifactName)
}
