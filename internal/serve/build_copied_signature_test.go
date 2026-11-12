// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

// The daemon's build holds a recorded signature fact to which artifact its bundle
// was made for, as the command line's does: the workspace whose Component the
// content cache serves, its row given a fact whose bundle — intact, in the cache,
// made by the toolchain's own keyed signer — was made for a different artifact,
// which is what a fact copied from another row is.

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

// recordFactMadeForAnotherArtifact records, on the lockfile row whose reference
// names row, a signature fact whose cached bundle was made for another artifact.
func recordFactMadeForAnotherArtifact(t *testing.T, ws, releaseRel, row string) {
	t.Helper()
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
	path := oci.SignatureBundlePath(ws, ".aggrega/cache", bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	lockPath := lockfile.PathFor(filepath.Join(ws, releaseRel))
	lf, _, err := lockfile.LoadLockfile(lockPath)
	if err != nil {
		t.Fatalf("load the lockfile: %v", err)
	}
	recorded := false
	for i := range lf.Entities {
		if strings.Contains(lf.Entities[i].URI, row) {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: bundleDigest}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the lockfile holds no row for %q: %+v", row, lf.Entities)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
}

// TestTheDaemonBuildRefusesASignatureMadeForAnotherArtifact is the refusal on this
// surface, read off the wire by the condition's registered sentence.
func TestTheDaemonBuildRefusesASignatureMadeForAnotherArtifact(t *testing.T) {
	ws, releaseRel := cacheServedComponentWorkspace(t)
	recordFactMadeForAnotherArtifact(t, ws, releaseRel, "demo/svc")

	resp, pushed := driveBuildWithRawParams(t, newFillCapableServer(t, ws), `{"release":"`+releaseRel+`"}`)
	if resp.Error == nil {
		t.Fatalf("the daemon built a row whose recorded signature was made for another artifact\nwire:\n%s", pushed)
	}
	if !strings.Contains(pushed, oci.ErrSignatureForAnotherArtifact().Error()) {
		t.Errorf("the refusal does not carry the signature-for-another-artifact condition:\nwire:\n%s", pushed)
	}
	if !strings.Contains(pushed, "demo/svc") {
		t.Errorf("the refusal does not name the row:\nwire:\n%s", pushed)
	}
}
