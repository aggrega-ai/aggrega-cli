// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The signature pass over a row the content cache served that records a fact the
// build does not accept: its cached bundle removed or altered since the pull that
// cached it, or a fact copied from another row, whose bundle is intact and was made
// for another artifact. The build refuses such a row whatever the signing
// requirement says, so the pass asks the registry about it again through the same
// verification call as every other row, and the answer replaces the fact.

const reaskCacheDir = ".aggrega/cache"

// signedReaskStore publishes the re-ask fixture's artifact signed, as reaskStore
// does, and also returns the bundle the registry serves for it — the bundle a pull
// that verified it cached, and the one a repair must cache again.
func signedReaskStore(t *testing.T) (store *memory.Store, manifestDg, contentDg string, pubPEM, bundle []byte) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/svc\n")
	contentDg = digest.ContentDigest(body)
	manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, artifactType, err := signer.Sign(ctx, manifestDg)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(ctx, store, "1.0.0", artifactType, bundle); err != nil {
		t.Fatalf("AttachReferrer: %v", err)
	}
	return store, manifestDg, contentDg, signer.PublicKeyPEM(), bundle
}

// writeCachedBundle writes body at the cache slot named by bundleDigest under root.
func writeCachedBundle(t *testing.T, root, bundleDigest string, body []byte) string {
	t.Helper()
	path := oci.SignatureBundlePath(root, reaskCacheDir, bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// cacheBundleMadeFor caches under root a bundle the toolchain's keyed signer made
// over madeFor, with a key of its own, and returns the bundle's digest: an intact
// cached bundle, made for the artifact whose manifest digest is madeFor.
func cacheBundleMadeFor(t *testing.T, root, madeFor string) string {
	t.Helper()
	privPEM, _, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	signer, err := signing.LoadKeyedSigner(privPEM, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, _, err := signer.Sign(context.Background(), madeFor)
	if err != nil {
		t.Fatalf("sign %s: %v", madeFor, err)
	}
	d := digest.ContentDigest(bundle)
	writeCachedBundle(t, root, d, bundle)
	return d
}

// factDamage is one way a cached row's recorded fact comes to be one the build
// refuses, with the condition the build refuses it with.
type factDamage struct {
	name string
	// lay writes under root what the damage leaves in the cache and returns the
	// fact the row records; own is the bundle the registry serves for the row's
	// artifact.
	lay  func(t *testing.T, root string, own []byte) *lockfile.VerifiedSignature
	cond error
}

var factDamages = []factDamage{
	{
		name: "the cached bundle removed",
		lay: func(_ *testing.T, _ string, own []byte) *lockfile.VerifiedSignature {
			return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried", BundleDigest: digest.ContentDigest(own)}
		},
		cond: oci.ErrSignatureBundleDigestMismatch(),
	},
	{
		name: "the cached bundle altered",
		lay: func(t *testing.T, root string, own []byte) *lockfile.VerifiedSignature {
			d := digest.ContentDigest(own)
			writeCachedBundle(t, root, d, []byte("rewritten after the pull"))
			return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried", BundleDigest: d}
		},
		cond: oci.ErrSignatureBundleDigestMismatch(),
	},
	{
		name: "a fact copied from another artifact's row",
		lay: func(t *testing.T, root string, _ []byte) *lockfile.VerifiedSignature {
			other := cacheBundleMadeFor(t, root, digest.ContentDigest([]byte("the manifest of another artifact")))
			return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried", BundleDigest: other}
		},
		cond: oci.ErrSignatureForAnotherArtifact(),
	},
}

// assertRepaired holds a row to the repair: it records the registry's own bundle,
// the bundle is cached at its slot byte for byte, and the build's decision accepts
// the fact for the artifact the row resolves to.
func assertRepaired(t *testing.T, got *lockfile.VerifiedSignature, root, manifestDg string, own []byte) {
	t.Helper()
	ownDigest := digest.ContentDigest(own)
	if got == nil || got.BundleDigest != ownDigest {
		t.Fatalf("the row does not record the registry's own signature (bundle %s) after the pass: %+v", ownDigest, got)
	}
	cached, err := os.ReadFile(oci.SignatureBundlePath(root, reaskCacheDir, ownDigest))
	if err != nil {
		t.Fatalf("the bundle the fact names is not cached: %v", err)
	}
	if !bytes.Equal(cached, own) {
		t.Errorf("the cached bundle is not the one the registry serves:\n%s", cached)
	}
	if _, err := oci.HoldRecordedSignature(root, reaskCacheDir, oci.RecordedBundle{Digest: got.BundleDigest},
		oci.MadeForManifest(manifestDg)); err != nil {
		t.Errorf("the build's decision still refuses the repaired fact: %v", err)
	}
}

// TestTheSignaturePassAsksAgainAboutACacheServedRowWhoseRecordedFactABuildRefuses
// is the repair, on every section a row can sit in, for every damage, with and
// without a signing requirement: the pass asks the registry, the answer replaces
// the fact, and the bundle it names is cached again — an altered slot rewritten.
func TestTheSignaturePassAsksAgainAboutACacheServedRowWhoseRecordedFactABuildRefuses(t *testing.T) {
	t.Parallel()
	for _, required := range []bool{true, false} {
		for _, section := range reaskSections {
			for _, damage := range factDamages {
				name := section.name + "/" + damage.name + "/requirement " + map[bool]string{true: "on", false: "off"}[required]
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					store, manifestDg, contentDg, pubPEM, own := signedReaskStore(t)
					root := t.TempDir()
					cl, lf := section.fixture(manifestDg, contentDg, damage.lay(t, root, own))
					cfg := requiringConfig(pubPEM)
					cfg.Registries[0].Signing.Required = required
					log := &askLog{}
					if err := VerifyClosureSignatures(context.Background(), lf, cl, cfg, root, reaskCacheDir,
						io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log)); err != nil {
						t.Fatalf("VerifyClosureSignatures: %v", err)
					}
					if !log.asked(section.uri) {
						t.Fatalf("the pass asked the registry nothing about %s, whose recorded fact the build refuses; asked %v",
							section.uri, log.uris)
					}
					assertRepaired(t, section.fact(lf), root, manifestDg, own)
				})
			}
		}
	}
}

// TestTheSignaturePassAsksNothingAboutACacheServedRowWhoseRecordedFactABuildAccepts
// is the other half: a row whose fact the build accepts — its own bundle intact in
// the cache, or a fact naming no bundle at all — costs no registry request online,
// and offline the pass passes as it does over a cache it has no question about.
func TestTheSignaturePassAsksNothingAboutACacheServedRowWhoseRecordedFactABuildAccepts(t *testing.T) {
	t.Parallel()
	facts := []struct {
		name string
		lay  func(t *testing.T, root string, own []byte) *lockfile.VerifiedSignature
	}{
		{name: "its own bundle intact", lay: func(t *testing.T, root string, own []byte) *lockfile.VerifiedSignature {
			d := digest.ContentDigest(own)
			writeCachedBundle(t, root, d, own)
			return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried", BundleDigest: d}
		}},
		{name: "a fact naming no bundle", lay: func(*testing.T, string, []byte) *lockfile.VerifiedSignature {
			return &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried"}
		}},
	}
	for _, offline := range []bool{false, true} {
		for _, section := range reaskSections {
			for _, f := range facts {
				t.Run(section.name+"/"+f.name+"/offline "+map[bool]string{true: "yes", false: "no"}[offline], func(t *testing.T) {
					t.Parallel()
					store, manifestDg, contentDg, pubPEM, own := signedReaskStore(t)
					root := t.TempDir()
					fact := f.lay(t, root, own)
					carried := *fact
					cl, lf := section.fixture(manifestDg, contentDg, fact)
					target := pluginTargetFor(store, "1.0.0")
					if offline {
						target = pluginfetchverify.RemoteTargetFor(nil)
					}
					log := &askLog{}
					if err := VerifyClosureSignatures(context.Background(), lf, cl, requiringConfig(pubPEM), root, reaskCacheDir,
						io.Discard, countingTargetFor(target, log)); err != nil {
						t.Fatalf("VerifyClosureSignatures: %v", err)
					}
					if len(log.uris) != 0 {
						t.Errorf("the pass made a registry request for a row whose recorded fact the build accepts: %v", log.uris)
					}
					if got := section.fact(lf); got == nil || *got != carried {
						t.Errorf("the row's recorded fact changed: %+v", got)
					}
				})
			}
		}
	}
}

// TestTheSignaturePassHaltsOfflineOnARowWhoseRecordedFactABuildRefuses holds the
// re-ask to the offline posture: asking means reaching the registry, the egress
// gate refuses the question, and the pass halts with that refusal — which names
// the remedy, allowing the network for one run — rather than write a row the next
// build refuses. The requirement does not change it, because the build refuses
// such a fact whatever the requirement says.
func TestTheSignaturePassHaltsOfflineOnARowWhoseRecordedFactABuildRefuses(t *testing.T) {
	t.Parallel()
	for _, required := range []bool{true, false} {
		for _, damage := range factDamages {
			t.Run(damage.name+"/requirement "+map[bool]string{true: "on", false: "off"}[required], func(t *testing.T) {
				t.Parallel()
				_, manifestDg, contentDg, pubPEM, own := signedReaskStore(t)
				root := t.TempDir()
				fact := damage.lay(t, root, own)
				carried := *fact
				cl, lf := reaskSections[0].fixture(manifestDg, contentDg, fact)
				cfg := requiringConfig(pubPEM)
				cfg.Registries[0].Signing.Required = required
				err := VerifyClosureSignatures(context.Background(), lf, cl, cfg, root, reaskCacheDir, io.Discard,
					pluginfetchverify.RemoteTargetFor(nil))
				if !errors.Is(err, egress.ErrOfflineEgress()) {
					t.Fatalf("want the offline refusal, got %v", err)
				}
				if got := reaskSections[0].fact(lf); got == nil || *got != carried {
					t.Errorf("a halted pass changed the row's recorded fact: %+v", got)
				}
			})
		}
	}
}

// TestWhatTheSkipsAndAnAdvisoryRegistryDoToARowWhoseRecordedFactABuildRefuses
// states what the verification call itself answers for a row asked about again,
// which the pass keeps: the skips ask nothing and give no fact, so the fold clears
// the row's; an advisory registry asks, records what verifies and gives no fact
// where nothing does. A cleared row is one the build admits under the same
// settings, which admit a row recording no signature.
func TestWhatTheSkipsAndAnAdvisoryRegistryDoToARowWhoseRecordedFactABuildRefuses(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name     string
		signed   bool
		cfg      func(workspaceconfig.Config) workspaceconfig.Config
		asked    bool
		repaired bool
	}{
		{name: "the global skip: asks nothing, clears the fact", signed: true, cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.SkipSignatureVerification = true
			return c
		}},
		{name: "the registry's own skip: asks nothing, clears the fact", signed: true, cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.Registries[0].SkipSignatureVerification = true
			return c
		}},
		{name: "an advisory registry serving the signature: asks, repairs", signed: true, asked: true, repaired: true,
			cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
				c.Registries[0].Advisory = true
				return c
			}},
		{name: "an advisory registry serving none: asks, clears the fact", asked: true,
			cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
				c.Registries[0].Advisory = true
				return c
			}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			store, manifestDg, contentDg, pubPEM, own := signedReaskStore(t)
			if !row.signed {
				// The same artifact published with no signature attached; the key the
				// configuration pins stays, and finds nothing to verify.
				store, manifestDg, contentDg, _ = reaskStore(t, false)
			}
			root := t.TempDir()
			fact := &lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried", BundleDigest: digest.ContentDigest(own)}
			cl, lf := reaskSections[0].fixture(manifestDg, contentDg, fact)
			log := &askLog{}
			if err := VerifyClosureSignatures(context.Background(), lf, cl, row.cfg(requiringConfig(pubPEM)), root, reaskCacheDir,
				io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log)); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v", err)
			}
			if got := log.asked(reaskSections[0].uri); got != row.asked {
				t.Errorf("asked the registry: %t, want %t (asked %v)", got, row.asked, log.uris)
			}
			got := reaskSections[0].fact(lf)
			if row.repaired {
				assertRepaired(t, got, root, manifestDg, own)
				return
			}
			if got != nil {
				t.Errorf("the row keeps a fact the call gave no answer for: %+v", got)
			}
		})
	}
}

// TestTheSignaturePassHaltsOnARowWhoseRecordedFactABuildRefusesWithNoRegistryToAsk
// is the row that cannot be asked about: a host-less identity — a catalog entity,
// a library — recording a fact the build refuses, in a workspace that names no
// default registry. The pass halts with the build's own refusal, wrapped to say no
// registry is configured to ask about it again, asks nothing and leaves the fact
// as it is; a plugin artifact carrying its own registry host is asked about, and
// repaired, all the same.
func TestTheSignaturePassHaltsOnARowWhoseRecordedFactABuildRefusesWithNoRegistryToAsk(t *testing.T) {
	t.Parallel()
	for _, section := range reaskSections {
		for _, damage := range factDamages {
			t.Run(section.name+"/"+damage.name, func(t *testing.T) {
				t.Parallel()
				store, manifestDg, contentDg, pubPEM, own := signedReaskStore(t)
				root := t.TempDir()
				fact := damage.lay(t, root, own)
				carried := *fact
				cl, lf := section.fixture(manifestDg, contentDg, fact)
				cfg := requiringConfig(pubPEM)
				cfg.DefaultRegistry = ""
				log := &askLog{}
				err := VerifyClosureSignatures(context.Background(), lf, cl, cfg, root, reaskCacheDir,
					io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log))
				if strings.HasPrefix(section.uri, reaskHost+"/") {
					if err != nil {
						t.Fatalf("a row carrying its own registry host must be asked about: %v", err)
					}
					assertRepaired(t, section.fact(lf), root, manifestDg, own)
					return
				}
				if !errors.Is(err, damage.cond) {
					t.Fatalf("want the build's own refusal (%v), got %v", damage.cond, err)
				}
				want := `verify: "` + section.uri + `" records a signature a build does not accept, and no registry is configured to ask about it again: `
				if !strings.HasPrefix(err.Error(), want) {
					t.Errorf("the halt does not say no registry is configured to ask about the row:\n got %s\nwant prefix %s", err, want)
				}
				if len(log.uris) != 0 {
					t.Errorf("the pass asked a registry while halting: %v", log.uris)
				}
				if got := section.fact(lf); got == nil || *got != carried {
					t.Errorf("a halted pass changed the row's recorded fact: %+v", got)
				}
			})
		}
	}
}

// TestTheBundleWriteRewritesASlotThatNoLongerHoldsItsDigest holds the write a
// verified signature is cached by: an absent slot is written, a slot holding
// other bytes is rewritten, and a slot holding the right bytes is left as it is —
// not rewritten, so the cache stays byte-stable and its time stamp does not move.
func TestTheBundleWriteRewritesASlotThatNoLongerHoldsItsDigest(t *testing.T) {
	t.Parallel()
	bundle := []byte(`{"probe":"a verified signature bundle"}`)
	d := digest.ContentDigest(bundle)
	stamp := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	rows := []struct {
		name string
		// before is what the slot holds before the write; nil leaves it absent.
		before []byte
		// untouched asserts the write left the slot's time stamp where the case set it.
		untouched bool
	}{
		{name: "an absent slot is written"},
		{name: "an altered slot is rewritten", before: []byte("rewritten after the pull")},
		{name: "a truncated slot is rewritten", before: []byte{}},
		{name: "an intact slot is left as it is", before: bundle, untouched: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			path := oci.SignatureBundlePath(root, reaskCacheDir, d)
			if row.before != nil {
				writeCachedBundle(t, root, d, row.before)
				if err := os.Chtimes(path, stamp, stamp); err != nil {
					t.Fatal(err)
				}
			}
			if err := cacheBundleBlob(root, reaskCacheDir, d, bundle); err != nil {
				t.Fatalf("cacheBundleBlob: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read the slot: %v", err)
			}
			if !bytes.Equal(got, bundle) {
				t.Errorf("the slot holds %q, want the verified bundle", got)
			}
			if _, err := oci.HoldRecordedSignature(root, reaskCacheDir, oci.RecordedBundle{Digest: d}, oci.NoManifestRead()); err != nil {
				t.Errorf("the build's re-hash refuses the slot the write left: %v", err)
			}
			if row.untouched {
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				if !info.ModTime().Equal(stamp) {
					t.Errorf("an intact slot was written again: its time stamp moved to %s", info.ModTime())
				}
			}
		})
	}
}
