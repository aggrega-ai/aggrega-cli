// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

// The remedy the build's refusal of a recorded fact names. A pull asks the
// registry again about a row the content cache served whose recorded fact the
// build refuses, and rewrites a cached bundle that no longer matches its digest,
// so the refusal of such a row names that pull — the Release's own, the file it
// records the fact in — for either condition and for either store. A row read from
// a catalog directory is never asked about at a registry, so its refusal names no
// pull.

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
)

// pullRemedy is the remedy the build names for a refused fact recorded in file,
// for the Release release.
func pullRemedy(release, file string) string {
	return "remediation: run `aggrega pull --release " + release + "` to verify the artifact's signature at its " +
		"registry again, record it in " + file + " and cache the bundle it names"
}

// TestTheBuildNamesThePullThatRepairsARecordedFactItRefuses is the table: one
// damage per row on a cache-served lockfile row, the trust record's verdict plugin
// beside them, and the catalog-directory row that names no pull.
func TestTheBuildNamesThePullThatRepairsARecordedFactItRefuses(t *testing.T) {
	t.Parallel()
	manifest := digest.ContentDigest([]byte("manifest of test/svc"))
	rows := []struct {
		name string
		// fact lays the row's fact down under root and returns it.
		fact func(t *testing.T, root, cacheDir string) *lockfile.VerifiedSignature
		path string
		cond error
		// named reports whether the refusal names the pull.
		named bool
	}{
		{name: "a cache-served row whose bundle was removed", path: closure.ResolutionPathCache, named: true,
			cond: oci.ErrSignatureBundleDigestMismatch(),
			fact: func(t *testing.T, root, cacheDir string) *lockfile.VerifiedSignature {
				f := signedFactFor(t, root, cacheDir, copiedFactSigner(t), manifest)
				if err := os.Remove(oci.SignatureBundlePath(root, cacheDir, f.BundleDigest)); err != nil {
					t.Fatal(err)
				}
				return f
			}},
		{name: "a cache-served row whose bundle was altered", path: closure.ResolutionPathCache, named: true,
			cond: oci.ErrSignatureBundleDigestMismatch(),
			fact: func(t *testing.T, root, cacheDir string) *lockfile.VerifiedSignature {
				f := signedFactFor(t, root, cacheDir, copiedFactSigner(t), manifest)
				if err := os.WriteFile(oci.SignatureBundlePath(root, cacheDir, f.BundleDigest), []byte("rewritten"), 0o644); err != nil {
					t.Fatal(err)
				}
				return f
			}},
		{name: "a cache-served row carrying a fact copied from another artifact", path: closure.ResolutionPathCache, named: true,
			cond: oci.ErrSignatureForAnotherArtifact(),
			fact: func(t *testing.T, root, cacheDir string) *lockfile.VerifiedSignature {
				return signedFactFor(t, root, cacheDir, copiedFactSigner(t), digest.ContentDigest([]byte("manifest of another artifact")))
			}},
		{name: "a catalog-directory row whose bundle was altered names no pull", path: closure.ResolutionPathCatalog,
			cond: oci.ErrSignatureBundleDigestMismatch(),
			fact: func(t *testing.T, root, cacheDir string) *lockfile.VerifiedSignature {
				f := signedFactFor(t, root, cacheDir, copiedFactSigner(t), manifest)
				if err := os.WriteFile(oci.SignatureBundlePath(root, cacheDir, f.BundleDigest), []byte("rewritten"), 0o644); err != nil {
					t.Fatal(err)
				}
				return f
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cfg := requiring()
			fact := row.fact(t, root, cfg.ResolvedArtifactCacheDir())
			ref := closure.ResolvedRef{URI: "test/svc", ResolvedVersion: "1.0.0", ResolutionPath: row.path, KindHint: "component"}
			if row.path == closure.ResolutionPathCache {
				ref.ManifestDigest = manifest
			}
			lf := &lockfile.Lockfile{Version: 1, Entities: []lockfile.Entity{{Kind: "component", URI: "test/svc", ResolvedVersion: "1.0.0", Signature: fact}}}
			err := enforceRecordedSignatures(gateInputs(root, cfg, lf), closure.Closure{Entities: []closure.ResolvedRef{ref}})
			if !errors.Is(err, row.cond) {
				t.Fatalf("want %v, got %v", row.cond, err)
			}
			text := err.Error()
			if !strings.HasPrefix(text, `artifact "test/svc": `) {
				t.Errorf("the refusal does not open by naming the row:\n%s", text)
			}
			remedy := pullRemedy(gateRelease, lockfile.PathFor(gateRelease))
			if row.named != strings.Contains(text, remedy) {
				t.Errorf("names the pull: %t, want %t:\n%s", !row.named, row.named, text)
			}
			if !row.named && strings.Contains(text, "remediation:") {
				t.Errorf("a row no pull asks about names a remedy:\n%s", text)
			}
		})
	}

	t.Run("a cache-served verdict plugin whose bundle was removed names the pull and the record", func(t *testing.T) {
		t.Parallel()
		c := newVerdictGateCase(t, "remedy-cache-served", false)
		fact := gateFact(t, c.root, c.cacheDir, []byte("verdict plugin"))
		if err := os.Remove(oci.SignatureBundlePath(c.root, c.cacheDir, fact.BundleDigest)); err != nil {
			t.Fatal(err)
		}
		c.record(t, fact)
		err := c.check(requiring(), 1)
		if !errors.Is(err, oci.ErrSignatureBundleDigestMismatch()) {
			t.Fatalf("want the cached-bundle mismatch, got %v", err)
		}
		if want := pullRemedy(c.releasePath, pluginstrust.PathFor(c.releasePath)); !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name the pull and the record:\nwant %s\n got %s", want, err)
		}
	})
}
