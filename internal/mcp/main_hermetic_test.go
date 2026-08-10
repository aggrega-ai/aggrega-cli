// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
)

// TestMain isolates the per-user homes for this test binary so the suite never
// merges the developer's ambient global configuration beneath its temporary
// fixtures, and never reads their registry credentials. The tool and server
// handlers resolve a workspace, which loads the global configuration layer;
// pointing that layer at an empty throwaway keeps results independent of the
// machine the suite runs on.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}

// TestPerUserHomesAreIsolated is the regression floor for the TestMain
// redirect: if it is ever dropped, this fails loudly rather than letting the
// developer's own configuration and credentials silently reach the suite.
func TestPerUserHomesAreIsolated(t *testing.T) {
	hometest.AssertRedirected(t)
}
