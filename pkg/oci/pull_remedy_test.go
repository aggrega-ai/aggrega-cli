// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestWithPullRemedyNamesThePullThatRepairsEitherCondition holds the one spelling
// of the remedy a refused recorded fact names: the pull of the Release, the file it
// records the fact in, and the bundle it caches, appended to either condition the
// decision raises and to nothing else, with the condition still reachable through
// the result.
func TestWithPullRemedyNamesThePullThatRepairsEitherCondition(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	_, missing := oci.HoldRecordedSignature(ws, cacheDir,
		oci.RecordedBundle{Digest: digest.ContentDigest([]byte("never cached"))}, oci.NoManifestRead())
	if !errors.Is(missing, oci.ErrSignatureBundleDigestMismatch()) {
		t.Fatalf("a bundle that is not in the cache must be refused as a mismatch; got %v", missing)
	}
	copiedDigest := cacheBundle(t, ws, cacheDir, keyedBundleFor(t, heldManifest))
	_, copied := oci.HoldRecordedSignature(ws, cacheDir, oci.RecordedBundle{Digest: copiedDigest}, oci.MadeForManifest(otherManifest))
	if !errors.Is(copied, oci.ErrSignatureForAnotherArtifact()) {
		t.Fatalf("a bundle made for another artifact must be refused as one; got %v", copied)
	}
	other := errors.New("a refusal the decision does not raise")

	rows := []struct {
		name       string
		refusal    error
		release    string
		recordedIn string
		want       string
		cond       error
	}{
		{name: "the cached-bundle mismatch names the pull, the file and the bundle", refusal: missing,
			release: "release.yaml", recordedIn: "release.lock", cond: oci.ErrSignatureBundleDigestMismatch(),
			want: missing.Error() + ". remediation: run `aggrega pull --release release.yaml` to verify the artifact's " +
				"signature at its registry again, record it in release.lock and cache the bundle it names"},
		{name: "a signature made for another artifact names the same remedy", refusal: copied,
			release: "release.yaml", recordedIn: "plugins-trust.yaml", cond: oci.ErrSignatureForAnotherArtifact(),
			want: copied.Error() + ". remediation: run `aggrega pull --release release.yaml` to verify the artifact's " +
				"signature at its registry again, record it in plugins-trust.yaml and cache the bundle it names"},
		{name: "a surface holding no Release names a placeholder", refusal: missing,
			recordedIn: "release.lock", cond: oci.ErrSignatureBundleDigestMismatch(),
			want: missing.Error() + ". remediation: run `aggrega pull --release <release>` to verify the artifact's " +
				"signature at its registry again, record it in release.lock and cache the bundle it names"},
		{name: "no file names none", refusal: missing, release: "release.yaml", cond: oci.ErrSignatureBundleDigestMismatch(),
			want: missing.Error() + ". remediation: run `aggrega pull --release release.yaml` to verify the artifact's " +
				"signature at its registry again, record it and cache the bundle it names"},
		{name: "any other refusal is returned as it is", refusal: other, release: "release.yaml", recordedIn: "release.lock",
			want: other.Error(), cond: other},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			got := oci.WithPullRemedy(row.refusal, row.release, row.recordedIn)
			if got == nil {
				t.Fatal("a refusal was dropped")
			}
			if got.Error() != row.want {
				t.Errorf("text:\n got %q\nwant %q", got.Error(), row.want)
			}
			if !errors.Is(got, row.cond) {
				t.Errorf("the condition is no longer reachable through the result: %v", got)
			}
		})
	}
	if oci.WithPullRemedy(nil, "release.yaml", "release.lock") != nil {
		t.Error("no refusal must stay no refusal")
	}
}

// TestAFactRecordingNoBundleDigestIsAcceptedWithNothingToHoldItTo is the decision's
// own admission of a fact that names no cached bundle: accepted with an empty path,
// whatever manifest it is asked about, so a reader asking the decision — the
// build's check, the load site and a pull's signature pass — gets one answer.
func TestAFactRecordingNoBundleDigestIsAcceptedWithNothingToHoldItTo(t *testing.T) {
	t.Parallel()
	for _, madeFor := range []oci.MadeFor{oci.NoManifestRead(), oci.MadeForManifest(heldManifest)} {
		path, err := oci.HoldRecordedSignature(t.TempDir(), ".aggrega/cache", oci.RecordedBundle{RecordedIn: "release.lock"}, madeFor)
		if err != nil {
			t.Errorf("a fact naming no bundle was refused: %v", err)
		}
		if path != "" {
			t.Errorf("a fact naming no bundle returned the path %q", path)
		}
	}
}
