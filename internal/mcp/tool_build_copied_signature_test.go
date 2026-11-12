// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The build tool holds a recorded signature fact to which artifact its bundle was
// made for, as the command line's build does: the fixture resolved through the pull
// tool, one Component moved into the content cache as a registry fetch lays it, and
// its row given a fact whose bundle — intact, in the cache, made by the toolchain's
// own keyed signer — was made for a different artifact, which is what a fact
// copied from another row is.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestTheBuildToolRefusesASignatureMadeForAnotherArtifact is the refusal on this
// surface, read by the condition's registered sentence.
func TestTheBuildToolRefusesASignatureMadeForAnotherArtifact(t *testing.T) {
	s, root, dirs, releasePath := pullWorkspace(t)
	resolveTheWorkspace(t, s, root, dirs, releasePath)
	moveComponentIntoTheCache(t, root)

	priv, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, _, err := signer.Sign(context.Background(), digest.ContentDigest([]byte("the manifest of another artifact")))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	bundleDigest := digest.ContentDigest(bundle)
	path := oci.SignatureBundlePath(root, ".aggrega/cache", bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	recorded := false
	for i := range lf.Entities {
		if strings.HasPrefix(lf.Entities[i].URI, recordedSignatureComponent) {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: bundleDigest}
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
		t.Fatalf("the build tool built a row whose recorded signature was made for another artifact: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, oci.ErrSignatureForAnotherArtifact().Error()) {
		t.Errorf("the refusal does not carry the signature-for-another-artifact condition:\n%s", text)
	}
	if !strings.Contains(text, recordedSignatureComponent) {
		t.Errorf("the refusal does not name the row:\n%s", text)
	}
}
