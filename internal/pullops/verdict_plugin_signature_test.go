// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/verdicttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verdictSignatureHost is the registry host the verdict plugin's identity is bound
// under in these cases; the in-memory store stands in for it.
const verdictSignatureHost = "keyed.example.com"

// verdictSignatureConfig binds a host-less identity to verdictSignatureHost and
// pins pubPEM there, requiring a signature where required says so.
func verdictSignatureConfig(pubPEM []byte, required bool) workspaceconfig.Config {
	return workspaceconfig.Config{
		DefaultRegistry: verdictSignatureHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host: verdictSignatureHost, Signing: workspaceconfig.SigningConfig{Required: required}, PublicKey: string(pubPEM),
		}},
	}
}

// TestTheVerdictPluginPullCarriesTheSignatureItVerified is the pull half of the
// verdict plugin's signature fact: the one verification the pull runs for such a
// plugin no longer discards what it verified. The fact rides on the resolution the
// record is written from, and the bundle it names is cached at the slot the build
// re-hashes — asserted through the build's own re-hash, so a bundle cached anywhere
// else fails here.
func TestTheVerdictPluginPullCarriesTheSignatureItVerified(t *testing.T) {
	root := t.TempDir()
	seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "governance", "signed-probe", "1.0.0")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
	store, manifestDigest, _, pubPEM := signedPluginStore(t)

	resolved, err := verifyCachedVerdictSocketPlugin(context.Background(), verdictSignatureConfig(pubPEM, true), lazy,
		seeded.URI, manifestDigest, root, cacheDir, pluginTargetFor(store, "1.0.0"), io.Discard)
	if err != nil {
		t.Fatalf("verify the signed verdict plugin: %v", err)
	}
	if resolved.Coordinate != seeded.Coordinate {
		t.Fatalf("precondition: the plugin must be identified from the cache; got coordinate %q", resolved.Coordinate)
	}
	if resolved.Signature == nil {
		t.Fatal("the pull verified the plugin's signature and carried no fact for the record: the fact was discarded")
	}
	if resolved.Signature.Mode != "keyed" || resolved.Signature.BundleDigest == "" || resolved.Signature.Identity == "" {
		t.Errorf("the carried fact is incomplete: %+v", resolved.Signature)
	}
	if _, err := oci.VerifyCachedSignatureBundle(root, cacheDir, resolved.Signature.BundleDigest); err != nil {
		t.Errorf("the bundle the fact names is not cached where the build re-hashes it: %v", err)
	}
}

// TestTheVerdictPluginPullCarriesNoFactWhereItVerifiedNone is the other direction:
// an unsigned plugin nothing requires a signature for is admitted with no fact and
// no bundle, so the record written from it carries no signature key at all.
func TestTheVerdictPluginPullCarriesNoFactWhereItVerifiedNone(t *testing.T) {
	root := t.TempDir()
	seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "governance", "unsigned-probe", "1.0.0")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
	store := memory.New()
	manifestDigest, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, verdicttest.HeaderOnlyWasm, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}

	resolved, err := verifyCachedVerdictSocketPlugin(context.Background(), verdictSignatureConfig(nil, false), lazy,
		seeded.URI, manifestDigest, root, cacheDir, pluginTargetFor(store, "1.0.0"), io.Discard)
	if err != nil {
		t.Fatalf("an unsigned plugin nothing requires a signature for must be admitted: %v", err)
	}
	if resolved.Coordinate != seeded.Coordinate {
		t.Fatalf("precondition: the plugin must be identified from the cache; got coordinate %q", resolved.Coordinate)
	}
	if resolved.Signature != nil {
		t.Errorf("no signature was verified, so none may be carried: %+v", resolved.Signature)
	}
	if _, err := os.Stat(filepath.Join(root, cacheDir, "signatures")); !os.IsNotExist(err) {
		t.Errorf("no signature was verified, so no bundle may be cached; stat %v", err)
	}
}

// TestAPullWritesTheVerdictPluginsSignatureFactIntoTheRecord carries the fact the
// rest of the way: a pull that resolved a signed plugin writes the fact into the
// record — on a first sighting, and onto an entry the record already holds whose
// fact went missing, which is the repair the build's refusal names.
func TestAPullWritesTheVerdictPluginsSignatureFactIntoTheRecord(t *testing.T) {
	root := t.TempDir()
	seeded := verdicttest.SeedCached(t, root, filepath.Join(root, "unsearched-catalog"), "governance", "recorded-probe", "1.0.0")
	cacheDir := workspaceconfig.Config{}.ResolvedArtifactCacheDir()
	lazy := catalog.NewLazyLookup(root, cacheDir, nil, nil)
	store, manifestDigest, _, pubPEM := signedPluginStore(t)
	resolved, err := verifyCachedVerdictSocketPlugin(context.Background(), verdictSignatureConfig(pubPEM, true), lazy,
		seeded.URI, manifestDigest, root, cacheDir, pluginTargetFor(store, "1.0.0"), io.Discard)
	if err != nil {
		t.Fatalf("verify the signed verdict plugin: %v", err)
	}
	resolved.BareURI, _ = splitAttachmentRef(seeded.URI)
	releasePath := filepath.Join(root, "release.yaml")
	attachments := attachingVerdictPlugins(seeded.URI)

	assertRecorded := func(t *testing.T, when string) {
		t.Helper()
		entry, held := verdicttest.TrustRecordAt(t, releasePath).Find(seeded.Coordinate)
		if !held {
			t.Fatalf("%s: the record holds no entry for %s", when, seeded.Coordinate)
		}
		if entry.Signature == nil || *entry.Signature != *resolved.Signature {
			t.Fatalf("%s: the entry carries %+v; want the fact the pull verified, %+v", when, entry.Signature, resolved.Signature)
		}
	}

	if _, err := recordVerdictPluginTrust("", releasePath, attachments, []verdictPluginResolution{resolved},
		catalog.PinReuseMode{}, io.Discard); err != nil {
		t.Fatalf("record the first sighting: %v", err)
	}
	assertRecorded(t, "a first sighting")

	// The entry loses its fact — a hand edit, or a record written before the field
	// existed — and an ordinary pull, which leaves the identity alone, records it
	// back.
	record := verdicttest.TrustRecordAt(t, releasePath)
	record.Plugins[0].Signature = nil
	if err := pluginstrust.Write(pluginstrust.PathFor(releasePath), record); err != nil {
		t.Fatalf("strip the fact: %v", err)
	}
	if _, err := recordVerdictPluginTrust("", releasePath, attachments, []verdictPluginResolution{resolved},
		catalog.PinReuseMode{}, io.Discard); err != nil {
		t.Fatalf("an ordinary pull over the stripped entry: %v", err)
	}
	assertRecorded(t, "an ordinary pull over an entry that lost its fact")
}
