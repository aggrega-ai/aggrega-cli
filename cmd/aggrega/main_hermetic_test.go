// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/hometest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestMain isolates the per-user homes for the whole test binary so no test
// reads from or writes to the developer's real global configuration or registry
// credentials. The redirect runs before any test resolves a workspace, so a
// populated global file in the ambient environment cannot leak into the suite.
//
// It also owns the teardown of artifacts shared across tests. Anything built
// once and reused by several tests outlives the test that triggered the build,
// so it cannot be tied to that test's lifetime and is released here instead,
// after every test has finished.
func TestMain(m *testing.M) {
	code := hometest.RunHermetic(m)
	removeSharedServeBinary()
	os.Exit(code)
}

// TestConfigHomeIsIsolated is the regression floor for the TestMain redirect. It
// asserts that the process environment the whole binary runs under is isolated,
// and that the global config path the production resolver would consult sits
// under the redirected home. If the redirect is ever dropped, these assertions
// fail loudly rather than letting the developer's real global configuration
// silently merge beneath every test's fixtures.
//
// It is an environment-level floor. The proof that the production resolver
// actually reads the redirected home — rather than a third location, or nothing
// — is a planting control, and lives with the resolver's own suite.
func TestConfigHomeIsIsolated(t *testing.T) {
	hometest.AssertRedirected(t)

	globalPath, ok := workspaceconfig.GlobalConfigPath(nil)
	if !ok {
		t.Fatal("GlobalConfigPath reported no resolvable path under the redirected home")
	}
	if !strings.HasPrefix(globalPath, hometest.ConfigHome()) {
		t.Fatalf("global config path %q does not sit under the redirected home %q", globalPath, hometest.ConfigHome())
	}
}
