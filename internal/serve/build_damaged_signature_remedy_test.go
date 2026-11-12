// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's build names the pull that repairs a recorded signature fact it
// refuses, as the command line's does: the workspace whose Component the content
// cache serves, its row given a fact naming a cached bundle that is no longer in
// the cache — a bundle removed after the pull that cached it.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheDaemonBuildNamesThePullThatRepairsARemovedSignatureBundle is the refusal
// on this surface, read off the wire: the condition, the row and the remedy.
func TestTheDaemonBuildNamesThePullThatRepairsARemovedSignatureBundle(t *testing.T) {
	ws, releaseRel := cacheServedComponentWorkspace(t)
	lockPath := lockfile.PathFor(filepath.Join(ws, releaseRel))
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	recorded := false
	for i := range lf.Entities {
		if strings.Contains(lf.Entities[i].URI, "demo/svc") {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe",
				BundleDigest: digest.ContentDigest([]byte("a bundle removed from the cache"))}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the lockfile holds no row for demo/svc: %+v", lf.Entities)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}

	resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws), `{"release":"`+releaseRel+`"}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built a row whose recorded signature bundle is not in the cache\nwire:\n%s", pushed)
	}
	for _, want := range []string{
		oci.ErrSignatureBundleDigestMismatch().Error(),
		"demo/svc",
		"remediation: run `aggrega pull --release ",
		"` to verify the artifact's signature at its registry again, record it in ",
	} {
		if !strings.Contains(pushed, want) {
			t.Errorf("the refusal does not carry %q:\nwire:\n%s", want, pushed)
		}
	}
}
