// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
)

// TestMain isolates the per-user homes for this test binary. A publication pins
// the entity it publishes, which resolves a workspace, loads the global
// configuration layer, and fetches sources with the credential the netrc file
// holds; pointing each at an empty throwaway keeps every result independent of
// the machine the suite runs on and keeps the developer's own credentials away
// from the suite's test servers.
func TestMain(m *testing.M) {
	os.Exit(hometest.RunHermetic(m))
}

// TestPerUserHomesAreIsolated is the regression floor for the TestMain redirect:
// if it is ever dropped, this fails loudly rather than letting the developer's
// own configuration and credentials silently reach the suite.
func TestPerUserHomesAreIsolated(t *testing.T) {
	hometest.AssertRedirected(t)
}
