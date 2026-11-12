// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The build tool names the pull that repairs a recorded signature fact it refuses,
// as the command line's build does: the fixture resolved through the pull tool, one
// Component moved into the content cache as a registry fetch lays it, and its row
// given a fact naming a cached bundle that is no longer in the cache.

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheBuildToolNamesThePullThatRepairsARemovedSignatureBundle is the refusal on
// this surface: the condition, the row and the remedy naming the Release.
func TestTheBuildToolNamesThePullThatRepairsARemovedSignatureBundle(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)
	moveComponentIntoTheCache(t, root)

	lockPath := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	recorded := false
	for i := range lf.Entities {
		if strings.HasPrefix(lf.Entities[i].URI, recordedSignatureComponent) {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe",
				BundleDigest: digest.ContentDigest([]byte("a bundle removed from the cache"))}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the lockfile holds no row for %q: %+v", recordedSignatureComponent, lf.Entities)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}

	res := callBuild(t, s, root, dirs, releasePath)
	if !res.IsError {
		t.Fatalf("the build tool built a row whose recorded signature bundle is not in the cache: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	for _, want := range []string{
		oci.ErrSignatureBundleDigestMismatch().Error(),
		recordedSignatureComponent,
		"remediation: run `aggrega pull --release ",
		"` to verify the artifact's signature at its registry again, record it in ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, text)
		}
	}
}
