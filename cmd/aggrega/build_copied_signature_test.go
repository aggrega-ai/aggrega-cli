// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// The command line's build holds a recorded signature fact to which artifact its
// bundle was made for, over the hermetic remote-aggregate fixture: its
// sub-aggregate pulled from a catalog directory and then moved into the content
// cache with the manifest a registry serves for it, so the build reads it through
// the pinned view's cache slot. The row is given a fact whose bundle — intact, in
// the cache, made by the toolchain's own keyed signer — was made either for the
// manifest the build reads or for another one, which is what a fact copied from
// another row is.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// recordSubAggregateFactMadeFor records on the sub-aggregate's row a fact whose
// cached bundle the keyed signer made over madeFor.
func recordSubAggregateFactMadeFor(t *testing.T, root, releasePath, madeFor string) {
	t.Helper()
	priv, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(priv, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("sign %s: %v", madeFor, err)
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
		if lf.Entities[i].URI == recordedSignatureSub {
			lf.Entities[i].Signature = &lockfile.VerifiedSignature{Mode: "keyed", Identity: "keyed:sha256:probe", BundleDigest: bundleDigest}
			recorded = true
		}
	}
	if !recorded {
		t.Fatalf("the lockfile holds no row for %q: %+v", recordedSignatureSub, lf.Entities)
	}
	if err := lockfile.WriteLockfile(lockPath, lf); err != nil {
		t.Fatalf("write the lockfile: %v", err)
	}
}

// subAggregateManifestDigest is the digest of the manifest the content cache holds
// beside the sub-aggregate, which is the artifact the build reads it as.
func subAggregateManifestDigest(t *testing.T, root string) string {
	t.Helper()
	manifest, err := oci.ReadCachedManifest(root, ".aggrega/cache", recordedSignatureSub+":1.0.0", "1.0.0")
	if err != nil {
		t.Fatalf("read the sub-aggregate's cached manifest: %v", err)
	}
	return digest.ContentDigest(manifest)
}

// TestTheCommandLineHoldsARecordedSignatureToTheArtifactItWasMadeFor is the
// command line's table: the row's own signature builds, and a signature made for
// another artifact is refused under the hub's built-in requirement and under the
// hub's own skip alike, named as a reader meets it.
func TestTheCommandLineHoldsARecordedSignatureToTheArtifactItWasMadeFor(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name    string
		cfg     string
		another bool
	}{
		{name: "the row's own signature, under the hub's requirement: builds", cfg: "auto_pull: false\n"},
		{name: "a signature made for another artifact, under the hub's requirement: refused", cfg: "auto_pull: false\n", another: true},
		{name: "a signature made for another artifact, under the hub's own skip: still refused",
			cfg: "auto_pull: false\n" + fakeRegistryTrust, another: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, releasePath, catalogDir := cacheServedSubAggregate(t)
			read := subAggregateManifestDigest(t, root)
			madeFor := read
			if row.another {
				madeFor = digest.ContentDigest([]byte("the manifest of another artifact"))
			}
			recordSubAggregateFactMadeFor(t, root, releasePath, madeFor)

			err := buildRecordedSignature(t, root, releasePath, catalogDir, row.cfg)
			if !row.another {
				if err != nil {
					t.Fatalf("a row whose signature was made for the artifact the build reads must build: %v", err)
				}
				return
			}
			if !errors.Is(err, oci.ErrSignatureForAnotherArtifact()) {
				t.Fatalf("want the signature-for-another-artifact refusal, got %v", err)
			}
			if errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) || errors.Is(err, lockfile.ErrSignatureNotRecorded()) {
				t.Errorf("the refusal also reports a damaged bundle or a missing signature, and neither is true: %v", err)
			}
			text := err.Error()
			for _, want := range []string{
				`"` + recordedSignatureSub + `"`,
				oci.ErrSignatureForAnotherArtifact().Error(),
				lockfile.PathFor(releasePath) + " records it",
				"the artifact read has manifest " + read,
				"remediation: run `aggrega pull --release " + releasePath + "` to verify the artifact's signature at its " +
					"registry again, record it in " + lockfile.PathFor(releasePath),
			} {
				if !strings.Contains(text, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, text)
				}
			}
			if head := formatCompilerError(err); !strings.Contains(head, "TRUST_ERROR E0332") {
				t.Errorf("the printed diagnostic does not open with the trust category and the condition's short code:\n%s", head)
			}
		})
	}
}
