// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestProductionBinary_NoRegistryTestDep asserts the test-only
// go-containerregistry dependency never enters the production binary's import
// graph. The in-process OCI registry harness that uses it is integration-tagged
// and test-only, so the default (non-test, untagged) build of the command must
// not depend on it. `go list -deps` over the package's production build is the
// lock: a stray non-test import of go-containerregistry would surface here in the
// fast suite rather than shipping in a release binary.
func TestProductionBinary_NoRegistryTestDep(t *testing.T) {
	t.Parallel()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}
	// Full import path, so the result is independent of the test's working dir.
	out, err := exec.Command(goBin, "list", "-deps",
		"github.com/aggrega-ai/aggrega-cli/cmd/aggrega").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "go-containerregistry") {
		t.Errorf("go-containerregistry is in the production (non-test) dependency graph of cmd/aggrega; " +
			"it must remain integration-test-only")
	}
}
