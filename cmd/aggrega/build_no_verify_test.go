// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestBuildPathNeverVerifiesSignatures is the AOT-purity guard for the
// signing model: signature verification is a lock-time recorded fact, and
// the build/render machinery must never run it. Build reads the verified
// fact from the lockfile; it does not fetch referrers, contact Fulcio /
// Rekor, or fetch a TUF trust root.
//
// The guard has two prongs:
//
//  1. The compiler's build/render core packages — the IR, topology,
//     renderer, closure, and lockfile packages build invokes to turn a
//     pinned closure into rendered output, plus the socket-resolution
//     package that resolves the verdict-gate socket plugin from the local
//     cache on the build path — must not import the verification pass
//     (pkg/pullverify) or the sigstore-go live-verify packages. A stray
//     import would surface here.
//  2. The build command source must contain no call to the verify pass.
//     Build's only path to verification is the in-process auto-pull (a
//     pull, which legitimately verifies); build's own lockfile-read render
//     path makes no such call.
//
// The verification packages legitimately live in the binary as a whole
// (push signs; pull verifies), so a whole-binary `go list` guard cannot
// express this — the guard is scoped to the core packages build runs and
// to the build command's own source.
func TestBuildPathNeverVerifiesSignatures(t *testing.T) {
	t.Parallel()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	const forbidden = "aggrega-cli/pkg/pullverify"
	const liveTUF = "sigstore-go/pkg/tuf"
	const verifyPkg = "sigstore-go/pkg/verify"

	corePkgs := []string{
		"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir",
		"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology",
		"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer",
		"github.com/aggrega-ai/aggrega-cli/pkg/closure",
		"github.com/aggrega-ai/aggrega-cli/pkg/lockfile",
		"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath",
	}
	for _, pkg := range corePkgs {
		out, lerr := exec.Command(goBin, "list", "-deps", pkg).CombinedOutput()
		if lerr != nil {
			t.Fatalf("go list -deps %s: %v\n%s", pkg, lerr, out)
		}
		deps := string(out)
		for _, bad := range []string{forbidden, liveTUF, verifyPkg} {
			if strings.Contains(deps, bad) {
				t.Errorf("build/render core package %s transitively imports %q; "+
					"signature verification is a lock-time concern and must not enter the build path", pkg, bad)
			}
		}
	}

	// Prong 2: the build command source must not call the verify pass.
	src, rerr := os.ReadFile("build.go")
	if rerr != nil {
		t.Fatalf("read build.go: %v", rerr)
	}
	for _, token := range []string{"pullverify", "verifyClosureSignatures", "VerifyKeyedBundle", "VerifyKeylessBundle", "VerifyKeylessLive"} {
		if strings.Contains(string(src), token) {
			t.Errorf("build.go references %q; build must not verify signatures (verification is a pull-time recorded fact)", token)
		}
	}
}
