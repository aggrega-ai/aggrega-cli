// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
)

// TestMain isolates the per-user configuration home for this test binary so the
// suite never merges the developer's ambient global configuration beneath its
// temporary fixtures. Tests that inject their own environment lookup into
// Resolve are unaffected; the redirect only governs the ambient default.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}
