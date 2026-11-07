// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The signature pass over a row the content cache served. Such a row was verified
// by the pull that fetched its bytes and carries that pull's fact forward, so it is
// normally not asked about. Where its row records no fact and a signature is
// required for it — a row a build refuses — the pass asks the registry about it,
// by the manifest digest its resolution reports, exactly as it asks about a fetched
// one, and records the fact. A row recording a fact the build refuses is the other
// row a build refuses, held in verify_reask_damaged_fact_test.go. Every other
// cache-served row is left as it is and costs no registry request.

const reaskHost = "keyed.example.com"

// askLog records the references a pass resolved a registry target for. Every
// registry request the pass makes — the signature lookup and the deprecation-marker
// discovery alike — goes through the target, so a reference absent from the log is
// one the registry was asked nothing about.
type askLog struct {
	uris []string
}

func (l *askLog) asked(uri string) bool {
	for _, u := range l.uris {
		if u == uri {
			return true
		}
	}
	return false
}

// countingTargetFor is inner with every reference it is asked for recorded.
func countingTargetFor(inner pullverify.TargetFor, log *askLog) pullverify.TargetFor {
	return func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		log.uris = append(log.uris, e.URI)
		return inner(e)
	}
}

// reaskStore publishes one artifact to an in-memory store and, when signed, signs
// its manifest digest with a fresh held key and attaches the bundle as a signature
// referrer. It returns the store, the manifest and content digests and the pinned
// public key (nil when unsigned).
func reaskStore(t *testing.T, signed bool) (store *memory.Store, manifestDg, contentDg string, pubPEM []byte) {
	t.Helper()
	ctx := context.Background()
	store = memory.New()
	body := []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Component\nmetadata:\n  name: test/svc\n")
	contentDg = digest.ContentDigest(body)
	manifestDg, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, body, nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if !signed {
		return store, manifestDg, contentDg, nil
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
	return store, manifestDg, contentDg, signer.PublicKeyPEM()
}

// reaskSection is which lockfile section the cache-served row sits in, with the
// closure reference and the lockfile row built for it.
type reaskSection struct {
	name string
	uri  string
	// fixture returns the closure and lockfile holding one cache-served reference
	// and its row, the row carrying fact.
	fixture func(manifestDg, contentDg string, fact *lockfile.VerifiedSignature) (closure.Closure, *lockfile.Lockfile)
	// fact reads the row's recorded signature back.
	fact func(lf *lockfile.Lockfile) *lockfile.VerifiedSignature
}

var reaskSections = []reaskSection{
	{
		name: "a catalog entity row",
		uri:  "test/svc:1.0.0",
		fixture: func(m, c string, fact *lockfile.VerifiedSignature) (closure.Closure, *lockfile.Lockfile) {
			return closure.Closure{Entities: []closure.ResolvedRef{{URI: "test/svc:1.0.0", ResolvedVersion: "1.0.0", KindHint: "component",
					Digest: c, ManifestDigest: m, ResolutionPath: closure.ResolutionPathCache}}},
				&lockfile.Lockfile{Entities: []lockfile.Entity{{Kind: "component", URI: "test/svc:1.0.0", ResolvedVersion: "1.0.0", Digest: c, ManifestDigest: m, Signature: fact}}}
		},
		fact: func(lf *lockfile.Lockfile) *lockfile.VerifiedSignature { return lf.Entities[0].Signature },
	},
	{
		name: "a library row",
		uri:  "shared-schemas:1.0.0",
		fixture: func(m, c string, fact *lockfile.VerifiedSignature) (closure.Closure, *lockfile.Lockfile) {
			return closure.Closure{SchemaLibraries: []closure.ResolvedLibrary{{URI: "shared-schemas:1.0.0", ResolvedVersion: "1.0.0",
					Digest: c, ManifestDigest: m, ResolutionPath: closure.ResolutionPathCache}}},
				&lockfile.Lockfile{SchemaLibraries: []lockfile.SchemaLibrary{{URI: "shared-schemas:1.0.0", ResolvedVersion: "1.0.0", Digest: c, ManifestDigest: m, Signature: fact}}}
		},
		fact: func(lf *lockfile.Lockfile) *lockfile.VerifiedSignature { return lf.SchemaLibraries[0].Signature },
	},
	{
		name: "a plugin artifact row",
		uri:  reaskHost + "/plugin/linter/publishing-linter",
		fixture: func(m, c string, fact *lockfile.VerifiedSignature) (closure.Closure, *lockfile.Lockfile) {
			const uri = reaskHost + "/plugin/linter/publishing-linter"
			return closure.Closure{Plugins: []closure.ResolvedRef{{URI: uri, ResolvedVersion: c, Digest: c, ManifestDigest: m,
					ResolutionPath: closure.ResolutionPathCache}}},
				&lockfile.Lockfile{Plugins: []lockfile.Plugin{{URI: uri, ResolvedVersion: c, Digest: c, ManifestDigest: m, Signature: fact}}}
		},
		fact: func(lf *lockfile.Lockfile) *lockfile.VerifiedSignature { return lf.Plugins[0].Signature },
	},
}

// requiringConfig is the workspace configuration that requires a signature for
// everything reaskHost serves, verified against pubPEM.
func requiringConfig(pubPEM []byte) workspaceconfig.Config {
	return workspaceconfig.Config{
		DefaultRegistry: reaskHost,
		Registries: []workspaceconfig.RegistryConfig{{
			Host: reaskHost, Signing: workspaceconfig.SigningConfig{Required: true}, PublicKey: string(pubPEM),
		}},
	}
}

// TestTheSignaturePassAsksTheRegistryAboutACacheServedRowARequirementCoversThatRecordsNoFact
// is the repair, on every section a row can sit in: the pass asks the registry about
// the row, records the verified fact on it, and caches the bundle that fact names
// where a build re-hashes it.
func TestTheSignaturePassAsksTheRegistryAboutACacheServedRowARequirementCoversThatRecordsNoFact(t *testing.T) {
	t.Parallel()
	for _, section := range reaskSections {
		t.Run(section.name, func(t *testing.T) {
			t.Parallel()
			store, manifestDg, contentDg, pubPEM := reaskStore(t, true)
			cl, lf := section.fixture(manifestDg, contentDg, nil)
			root := t.TempDir()
			log := &askLog{}
			if err := VerifyClosureSignatures(context.Background(), lf, cl, requiringConfig(pubPEM), root, ".aggrega/cache",
				io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log)); err != nil {
				t.Fatalf("VerifyClosureSignatures: %v", err)
			}
			if !log.asked(section.uri) {
				t.Fatalf("the pass asked the registry nothing about %s, whose row records no fact under a requirement; asked %v", section.uri, log.uris)
			}
			fact := section.fact(lf)
			if fact == nil || fact.Mode != "keyed" || fact.BundleDigest == "" {
				t.Fatalf("the row records no verified keyed signature with a cached bundle after the pass: %+v", fact)
			}
			if _, err := os.Stat(oci.SignatureBundlePath(root, ".aggrega/cache", fact.BundleDigest)); err != nil {
				t.Errorf("the bundle the recorded fact names is not cached where a build re-hashes it: %v", err)
			}
			if _, err := oci.VerifyCachedSignatureBundle(root, ".aggrega/cache", fact.BundleDigest); err != nil {
				t.Errorf("the cached bundle does not pass the build's re-hash: %v", err)
			}
		})
	}
}

// TestTheSignaturePassLeavesEveryOtherCacheServedRowAsItIs holds the rows the re-ask
// must not reach: a row that records a fact the build accepts — its bundle intact
// in the cache and made for the row's own artifact — and a row whose missing fact
// nothing halts on. Each differs from the repaired case in one input, and each
// costs no registry request.
func TestTheSignaturePassLeavesEveryOtherCacheServedRowAsItIs(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		// cfg derives the case's configuration from the requiring one.
		cfg     func(workspaceconfig.Config) workspaceconfig.Config
		carried bool
	}{
		{name: "a row that records a fact", cfg: func(c workspaceconfig.Config) workspaceconfig.Config { return c }, carried: true},
		{name: "no requirement", cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.Registries[0].Signing.Required = false
			return c
		}},
		{name: "the registry is advisory", cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.Registries[0].Advisory = true
			return c
		}},
		{name: "the registry's own skip", cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.Registries[0].SkipSignatureVerification = true
			return c
		}},
		{name: "the global skip", cfg: func(c workspaceconfig.Config) workspaceconfig.Config {
			c.SkipSignatureVerification = true
			return c
		}},
	}
	for _, section := range reaskSections {
		for _, row := range rows {
			t.Run(section.name+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				store, manifestDg, contentDg, pubPEM := reaskStore(t, true)
				root := t.TempDir()
				// The carried fact names a bundle that is in the cache and was made for
				// the row's own manifest, so the build accepts it: the row carries no
				// fault but the one the case varies.
				var carried lockfile.VerifiedSignature
				var fact *lockfile.VerifiedSignature
				if row.carried {
					carried = lockfile.VerifiedSignature{Mode: "keyed", Identity: "carried-from-the-prior-lockfile",
						BundleDigest: cacheBundleMadeFor(t, root, manifestDg)}
					c := carried
					fact = &c
				}
				cl, lf := section.fixture(manifestDg, contentDg, fact)
				log := &askLog{}
				if err := VerifyClosureSignatures(context.Background(), lf, cl, row.cfg(requiringConfig(pubPEM)), root, ".aggrega/cache",
					io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log)); err != nil {
					t.Fatalf("VerifyClosureSignatures: %v", err)
				}
				if len(log.uris) != 0 {
					t.Errorf("the pass made a registry request for a row it must leave as it is: %v", log.uris)
				}
				got := section.fact(lf)
				switch {
				case row.carried && (got == nil || *got != carried):
					t.Errorf("the row's recorded fact changed: %+v", got)
				case !row.carried && got != nil:
					t.Errorf("a row the pass did not ask about gained a fact: %+v", got)
				}
			})
		}
	}
}

// TestTheTopLevelPolicyMakesThePassAskDespiteTheRegistrysOwnSkip is the cell where
// the two scopes disagree: a registry's own skip does not stand down the top-level
// requirement, at a pull as at a build, so the row is asked about.
func TestTheTopLevelPolicyMakesThePassAskDespiteTheRegistrysOwnSkip(t *testing.T) {
	t.Parallel()
	section := reaskSections[0]
	store, manifestDg, contentDg, pubPEM := reaskStore(t, true)
	cl, lf := section.fixture(manifestDg, contentDg, nil)
	cfg := requiringConfig(pubPEM)
	cfg.Registries[0].Signing.Required = false
	cfg.Registries[0].SkipSignatureVerification = true
	cfg.Signing.Required = true
	log := &askLog{}
	err := VerifyClosureSignatures(context.Background(), lf, cl, cfg, t.TempDir(), ".aggrega/cache",
		io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log))
	if err != nil {
		t.Fatalf("VerifyClosureSignatures: %v", err)
	}
	if !log.asked(section.uri) {
		t.Fatalf("the top-level requirement covers the row, and the pass asked nothing about it")
	}
	if section.fact(lf) == nil {
		t.Fatal("the row records no fact after the pass asked about it")
	}
}

// TestTheSignaturePassHaltsWhereTheReAskCannotEstablishTheFact is the refusing half:
// a required check that finds no signature, or that cannot reach the registry, has
// not passed, so the pull halts rather than write a row the next build refuses.
// Offline, the refusal is the offline posture's, which names how to allow the
// network for one run.
func TestTheSignaturePassHaltsWhereTheReAskCannotEstablishTheFact(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name   string
		signed bool
		// target is the case's registry target factory, given the store.
		target func(store *memory.Store) pullverify.TargetFor
		want   error
	}{
		{name: "the registry attaches no signature", signed: false,
			target: func(store *memory.Store) pullverify.TargetFor { return pluginTargetFor(store, "1.0.0") },
			want:   signing.ErrNoSignatureFound()},
		{name: "the workspace is offline", signed: true,
			target: func(*memory.Store) pullverify.TargetFor { return pluginfetchverify.RemoteTargetFor(nil) },
			want:   egress.ErrOfflineEgress()},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			store, manifestDg, contentDg, pubPEM := reaskStore(t, row.signed)
			cl, lf := reaskSections[0].fixture(manifestDg, contentDg, nil)
			cfg := requiringConfig(pubPEM)
			if pubPEM == nil {
				cfg.Registries[0].PublicKey = "irrelevant: no signature is attached to check against it"
			}
			err := VerifyClosureSignatures(context.Background(), lf, cl, cfg, t.TempDir(), ".aggrega/cache", io.Discard, row.target(store))
			if !errors.Is(err, row.want) {
				t.Fatalf("want %v, got %v", row.want, err)
			}
			if got := reaskSections[0].fact(lf); got != nil {
				t.Errorf("a halted pass recorded a fact: %+v", got)
			}
		})
	}
}

// TestTwoCacheServedReferencesToOneArtifactAreAskedOnce holds the re-ask to one
// question per artifact: one Component pinned under two references, both served
// from the content cache and neither recording a fact, is asked about once, and the
// answer reaches both rows.
func TestTwoCacheServedReferencesToOneArtifactAreAskedOnce(t *testing.T) {
	t.Parallel()
	store, manifestDg, contentDg, pubPEM := reaskStore(t, true)
	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "test/svc", KindHint: "component", Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: closure.ResolutionPathCache},
		{URI: "test/svc:1.0.0", KindHint: "component", Digest: contentDg, ManifestDigest: manifestDg, ResolutionPath: closure.ResolutionPathCache},
	}}
	lf := &lockfile.Lockfile{Entities: []lockfile.Entity{
		{Kind: "component", URI: "test/svc", Digest: contentDg, ManifestDigest: manifestDg},
		{Kind: "component", URI: "test/svc:1.0.0", Digest: contentDg, ManifestDigest: manifestDg},
	}}
	log := &askLog{}
	if err := VerifyClosureSignatures(context.Background(), lf, cl, requiringConfig(pubPEM), t.TempDir(), ".aggrega/cache",
		io.Discard, countingTargetFor(pluginTargetFor(store, "1.0.0"), log)); err != nil {
		t.Fatalf("VerifyClosureSignatures: %v", err)
	}
	if log.asked("test/svc:1.0.0") {
		t.Errorf("the second reference to an artifact already asked about was asked again: %v", log.uris)
	}
	first, second := lf.Entities[0].Signature, lf.Entities[1].Signature
	if first == nil || second == nil {
		t.Fatalf("both rows pin the artifact the pass verified; got %+v and %+v", first, second)
	}
	if *first != *second {
		t.Errorf("two rows pinning one manifest record different facts: %+v, %+v", *first, *second)
	}
}
