// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestADriftFindingNamesTheReleaseItWasVerifiedFor pins that the drift warning
// names the Release whose lockfile drifted, spelled as the request named it — the
// value `--release` takes — so the command it carries is the one that rewrites
// that file, and never a placeholder.
//
// Each row verifies the same drifted lockfile for a different Release and must
// name that Release and no other row's; the clean row verifies a lockfile pinning
// nothing against a closure reaching nothing, and must carry no finding at all, so
// a verify that published the remedy whatever it found fails it.
func TestADriftFindingNamesTheReleaseItWasVerifiedFor(t *testing.T) {
	t.Parallel()
	const drifted = "version: 1\nrelease: r\ngenerated_at: \"2026-01-01T00:00:00Z\"\n" +
		"entities:\n  - kind: component\n    uri: example/web\n    resolved_version: 1.0.0\n    digest: "
	const clean = "version: 1\nrelease: r\ngenerated_at: \"2026-01-01T00:00:00Z\"\n"
	releases := []string{"release.yaml", "release-helm-synth.yaml", "envs/prod.yaml"}

	cases := []struct {
		release, lockfile string
		wantFinding       bool
	}{
		{release: releases[0], lockfile: drifted + contentDigestHex([]byte("example/web")) + "\n", wantFinding: true},
		{release: releases[1], lockfile: drifted + contentDigestHex([]byte("example/web")) + "\n", wantFinding: true},
		{release: releases[2], lockfile: drifted + contentDigestHex([]byte("example/web")) + "\n", wantFinding: true},
		{release: releases[1], lockfile: clean},
	}
	for _, tc := range cases {
		name := tc.release + " drifted"
		if !tc.wantFinding {
			name = tc.release + " clean"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tmp := t.TempDir()
			srv := newTestServer(t, tmp, nil, nil)
			lockfilePath := filepath.Join(tmp, "release.lock")
			if err := os.WriteFile(lockfilePath, []byte(tc.lockfile), 0o644); err != nil {
				t.Fatalf("write lockfile: %v", err)
			}
			if lf := srv.verifyDriftAndUpdateCache(lockfilePath, tc.release, closure.Closure{}); lf == nil {
				t.Fatal("the lockfile did not load, so the verify was never reached")
			}
			findings := srv.snapshotDiagnostics(lockfilePath)
			if !tc.wantFinding {
				if len(findings) != 0 {
					t.Errorf("a lockfile that verifies clean carries %d finding(s): %+v", len(findings), findings)
				}
				return
			}
			if len(findings) != 1 {
				t.Fatalf("want one drift finding, got %d: %+v", len(findings), findings)
			}
			message := findings[0].Message
			if remedy := compilerops.LockfileDriftRemedy(tc.release); !strings.Contains(message, remedy) {
				t.Errorf("the finding does not carry the remedy for %s, %q:\n%s", tc.release, remedy, message)
			}
			for _, other := range releases {
				if other != tc.release && strings.Contains(message, compilerops.LockfileDriftRemedy(other)) {
					t.Errorf("the finding verified for %s names %s:\n%s", tc.release, other, message)
				}
			}
			if strings.Contains(message, "<release>") {
				t.Errorf("the finding names a placeholder rather than the Release:\n%s", message)
			}
		})
	}
}
