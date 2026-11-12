// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os/exec"
	"strings"
	"testing"
)

// TestProductionBinary_NoRegistryHarnessDep asserts the in-process OCI
// registry harness never enters the production binary's import graph. The
// harness is the heavyweight go-containerregistry/pkg/registry server,
// used only by the integration-tagged registry round-trip tests; the
// default (non-test, untagged) build of the command must not depend on it.
//
// The guard is scoped to pkg/registry (the harness), not to the whole
// go-containerregistry module: the signing crypto library transitively
// brings the small reference-parsing leaf go-containerregistry/pkg/name
// into the production graph, which is a legitimate dependency of the
// keyed-signing path and not the in-process server this guard exists to
// keep out.
//
// `go list -deps` over the package's production build is the lock: a stray
// non-test import of the registry harness would surface here in the fast
// suite rather than shipping in a release binary.
func TestProductionBinary_NoRegistryHarnessDep(t *testing.T) {
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
	if strings.Contains(string(out), "go-containerregistry/pkg/registry") {
		t.Errorf("the in-process registry harness (go-containerregistry/pkg/registry) is in the " +
			"production (non-test) dependency graph of cmd/aggrega; it must remain integration-test-only")
	}
}
