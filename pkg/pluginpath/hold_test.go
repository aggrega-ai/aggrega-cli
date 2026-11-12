// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginpath

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAModuleIsHeldToTheDigestItMustMatch holds the one comparison of a resolved
// module's bytes against a digest they must match, from both of its callers.
//
// Through HoldToDigest, bytes whose content digest is the one named pass, and any
// other bytes are refused under the integrity condition, the refusal naming the
// reference, the digest the bytes carry and what they were held to, followed by
// the digest they had to carry. Through the cascade's own pin check, the refusal
// of a module that does not match its lockfile pin is still, word for word, the
// sentence the cascade has always written — the comparison moved into one home
// without the cascade's wording moving with it.
func TestAModuleIsHeldToTheDigestItMustMatch(t *testing.T) {
	const uri = "importer/held:1.0.0"
	accepted := []byte("the-accepted-bytes")
	acceptedDigest := digest.ContentDigest(accepted)
	other := []byte("other-bytes")
	otherDigest := digest.ContentDigest(other)
	const heldTo = "the digest a release publishes"

	if err := HoldToDigest(ResolvedPlugin{URI: uri, Tier: TierCache, Bytes: accepted}, acceptedDigest, heldTo); err != nil {
		t.Fatalf("the module the digest names was refused: %v", err)
	}

	err := HoldToDigest(ResolvedPlugin{URI: uri, Tier: TierCache, Bytes: other}, acceptedDigest, heldTo)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("a module the digest does not name was not refused under the integrity condition: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("plugin %q", uri),
		"content digest " + otherDigest,
		"does not match " + heldTo + " " + acceptedDigest,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not carry %q", err.Error(), want)
		}
	}

	colocatedDir := writeColocated(t, filepath.Join(t.TempDir(), "colocated"), uri, other)
	_, err = Resolver{
		colocatedDirs: map[string]ColocatedDir{uri: {Dir: colocatedDir}},
		pins:          map[string]string{uri: acceptedDigest},
	}.Resolve(uri)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("a module that does not match its lockfile pin was not refused under the integrity condition: %v", err)
	}
	want := fmt.Sprintf("plugin %q resolved from %s: content digest %s does not match lockfile pin %s: ",
		uri, TierColocated.Source(), otherDigest, acceptedDigest)
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the lockfile pin refusal reads %q; want it to open with %q", err.Error(), want)
	}
}

// TestAnAcceptedDigestReadsTheSlotItAddressesAndHoldsTheBytesToIt drives the
// cascade built from AcceptedDigest over a workspace content cache laid out one
// way per row, in order, over one workspace.
//
// The digest is the version the cache tier addresses the reference's slot by, so
// a module written at the unversioned slot is not read and one written at the
// digest's slot is — the slot a fetch held to that digest writes, keyed by its
// content as a pull keys a plugin it fetched. Bytes at that slot that are not the
// module the digest names are refused under the integrity condition, naming what
// the digest was held to rather than a lockfile pin. And no row raises the
// unrecorded-identity advisory: every resolution under this source carries a pin.
//
// 🔴 PLANTED DEFECT: drop PinVersions from the maps AcceptedDigest builds
// (pkg/pluginpath/construct.go) and the row reading the digest's slot fails, the
// cascade reading the unversioned slot instead; drop HeldTo and the refusal row
// fails, naming a lockfile pin.
func TestAnAcceptedDigestReadsTheSlotItAddressesAndHoldsTheBytesToIt(t *testing.T) {
	advisory, report := captureUnrecordedAdvisory(t)
	const uri = "importer/accepted"
	const cacheDir = ".aggrega/cache"
	const heldTo = "the digest a release publishes"
	accepted := []byte("the accepted module")
	acceptedDigest := digest.ContentDigest(accepted)
	other := []byte("another module")
	otherDigest := digest.ContentDigest(other)

	ws := t.TempDir()
	cascade := AcceptedDigest(uri, acceptedDigest, heldTo).Resolver(ResolutionContext{WorkspaceRoot: ws, CacheDir: cacheDir, Unrecorded: report})
	write := func(t *testing.T, version string, b []byte) {
		t.Helper()
		if err := oci.WriteCached(ws, cacheDir, oci.PluginArtifactKind(), uri, version, b); err != nil {
			t.Fatalf("write the cache slot at %q: %v", version, err)
		}
	}

	if _, err := cascade.Resolve(uri); !errors.Is(err, ErrPluginArtifactNotFound()) {
		t.Fatalf("an empty cache did not exhaust the cascade: %v", err)
	}

	write(t, "", accepted)
	if _, err := cascade.Resolve(uri); !errors.Is(err, ErrPluginArtifactNotFound()) {
		t.Fatalf("the module at the unversioned slot was read, or refused otherwise: %v", err)
	}

	write(t, acceptedDigest, accepted)
	got, err := cascade.Resolve(uri)
	if err != nil {
		t.Fatalf("the module at the slot the digest addresses was refused: %v", err)
	}
	if got.Tier != TierCache || string(got.Bytes) != string(accepted) || got.Version != acceptedDigest {
		t.Errorf("resolved %q from %s at %q; want the accepted module from the cache tier at %s",
			got.Bytes, got.Tier, got.Version, acceptedDigest)
	}

	write(t, acceptedDigest, other)
	_, err = cascade.Resolve(uri)
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Fatalf("another module at the digest's slot was not refused under the integrity condition: %v", err)
	}
	want := fmt.Sprintf("plugin %q resolved from %s: content digest %s does not match %s %s: ",
		uri, TierCache.Source(), otherDigest, heldTo, acceptedDigest)
	if !strings.HasPrefix(err.Error(), want) {
		t.Errorf("the refusal reads %q; want it to open with %q", err.Error(), want)
	}

	if advisory.Len() != 0 {
		t.Errorf("a resolution carrying a pin raised the unrecorded-identity advisory: %q", advisory.String())
	}
}
