// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docsbundle

// Which version of the documentation bundle a reader whose own version is not
// published reads: the newest published at or below it, chosen from what the
// registry lists online and from what the cache holds offline, under the same
// signature check as an exact read.

import (
	"context"
	"errors"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle/docsbundletest"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// publishedAt returns a registry publishing a distinct signed bundle at each of
// versions, a target serving it with the production verification and counting
// its reaches, and the key that signed them.
func publishedAt(t *testing.T, versions ...string) (*docsbundletest.Registry, TargetFor, *int, []byte) {
	t.Helper()
	reg := docsbundletest.NewRegistry()
	signer := docsbundletest.NewSigner(t)
	for _, v := range versions {
		reg.Publish(t, v, docsbundletest.Bundle(t, []docsbundletest.Member{
			{Name: testPage, Body: "# Component " + v + "\n"},
		}), signer)
	}
	reaches := 0
	target := func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		reaches++
		return reg, Verifier(cfg, ref, version, reg, warn), nil
	}
	return reg, target, &reaches, signer.PublicKeyPEM()
}

// TestScaffoldExampleVersionTheExactOneWhereItIsPublished pins the first half of
// the choice: a version the registry publishes is the one read, even with newer
// ones beside it, and a second read of it reaches no registry.
func TestScaffoldExampleVersionTheExactOneWhereItIsPublished(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, target, reaches, key := publishedAt(t, "1.0.0", "1.1.0", "1.2.0")

	held, acquired, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, "1.1.0", target, nil)
	if err != nil || held != "1.1.0" || !acquired {
		t.Fatalf("answered (%q, %v, %v), want 1.1.0 acquired", held, acquired, err)
	}
	held, acquired, err = EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, "1.1.0", target, nil)
	if err != nil || held != "1.1.0" || acquired {
		t.Fatalf("the second read answered (%q, %v, %v), want 1.1.0 from the cache", held, acquired, err)
	}
	if *reaches != 1 {
		t.Errorf("the registry was reached %d times, want once", *reaches)
	}
}

// TestScaffoldExampleVersionTheNewestAtOrBelowWhereTheExactOneIsNot pins the
// fallback: with the version asked for unpublished, the newest release below it
// is read — never a newer one, and never a pre-release.
func TestScaffoldExampleVersionTheNewestAtOrBelowWhereTheExactOneIsNot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, target, _, key := publishedAt(t, "0.9.0", "1.0.0", "1.1.0-rc.1", "1.2.0")

	held, _, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, "1.1.0", target, nil)
	if err != nil || held != "1.0.0" {
		t.Fatalf("answered (%q, %v), want 1.0.0", held, err)
	}
	if err := docscache.Admitted(root, testRef, "1.0.0", Admission(context.Background(), requiring(key), testRef, nil)); err != nil {
		t.Errorf("the version chosen is not held under the workspace's check: %v", err)
	}
	if cached, err := docscache.Cached(root, testRef, "1.2.0"); err != nil || cached {
		t.Errorf("a version above the one asked for was acquired (%v, %v)", cached, err)
	}
}

// TestScaffoldExampleVersionADescribedBuildReadsTheReleaseItWasBuiltAfter pins how
// a build that names itself by the commits since a release is read: after that
// release, so the release is the one at or below it — rather than before it, as a
// pre-release of it would sort, which would skip it for the one before.
func TestScaffoldExampleVersionADescribedBuildReadsTheReleaseItWasBuiltAfter(t *testing.T) {
	t.Parallel()
	for _, described := range []string{"1.1.0-55-g94165d3e-dirty", "1.1.0-3-gabcdef0"} {
		t.Run(described, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			_, target, _, key := publishedAt(t, "1.0.0", "1.1.0", "1.2.0")
			held, _, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, described, target, nil)
			if err != nil || held != "1.1.0" {
				t.Fatalf("answered (%q, %v), want 1.1.0", held, err)
			}
		})
	}
}

// TestScaffoldExampleVersionNothingAtOrBelowIsRefused pins the end of the
// fallback: a registry publishing only newer versions has nothing to offer, and
// the refusal is the cache miss the exact read met, saying so.
func TestScaffoldExampleVersionNothingAtOrBelowIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, target, _, key := publishedAt(t, "2.0.0")

	_, _, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), root, testRef, "1.0.0", target, nil)
	if !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("answered %v, want the cache miss", err)
	}
	if cached, _ := docscache.Cached(root, testRef, "2.0.0"); cached {
		t.Error("a version above the one asked for was acquired")
	}
}

// TestScaffoldExampleVersionADevelopmentBuildHasNoFallback pins that a version
// that is no release number is read exactly or not at all.
func TestScaffoldExampleVersionADevelopmentBuildHasNoFallback(t *testing.T) {
	t.Parallel()
	_, target, reaches, key := publishedAt(t, "1.0.0")
	_, _, err := EnsureCachedAtOrBelow(context.Background(), requiring(key), t.TempDir(), testRef, "(devel)", target, nil)
	if !errors.Is(err, docscache.ErrFetchNotFound()) {
		t.Fatalf("answered %v, want the cache miss of the exact read", err)
	}
	if *reaches != 1 {
		t.Errorf("the registry was reached %d times, want only the exact read's", *reaches)
	}
}

// TestScaffoldExampleVersionOfflineReadsTheNewestCachedAtOrBelow pins the offline
// half: no registry can be asked, so the newest copy at or below the version that
// the cache holds and the workspace admits is read — and with none held, the
// offline refusal is returned as it is.
func TestScaffoldExampleVersionOfflineReadsTheNewestCachedAtOrBelow(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, target, _, key := publishedAt(t, "0.9.0", "1.0.0", "1.2.0")
	online := requiring(key)
	for _, v := range []string{"0.9.0", "1.0.0", "1.2.0"} {
		if _, err := EnsureCached(context.Background(), online, root, testRef, v, target, nil); err != nil {
			t.Fatalf("seed %s: %v", v, err)
		}
	}
	offline := requiring(key)
	offline.Offline = true

	// No target is named and none is stated, so the production target is the one
	// asked, and the workspace's egress refuses it before any dial.
	held, acquired, err := EnsureCachedAtOrBelow(context.Background(), offline, root, testRef, "1.1.0", nil, nil)
	if err != nil || held != "1.0.0" || acquired {
		t.Fatalf("offline answered (%q, %v, %v), want 1.0.0 from the cache", held, acquired, err)
	}

	_, _, err = EnsureCachedAtOrBelow(context.Background(), offline, t.TempDir(), testRef, "1.1.0", nil, nil)
	if !errors.Is(err, egress.ErrOfflineEgress()) {
		t.Fatalf("offline with nothing cached answered %v, want the offline refusal", err)
	}
}
