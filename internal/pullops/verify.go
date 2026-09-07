// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginfetchverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// VerifyClosureSignatures runs the pull-time signature verification pass over
// every registry-resolved reference in the closure — catalog entities, the four
// library sections, and Wasm plugins — and folds the verified facts into the
// lockfile, caching each verified signature bundle as a content-addressed blob for
// air-gapped re-verification. It is called only on the pull write path — never on
// build, which reads the recorded facts from the lockfile and never
// re-verifies signatures (the AOT boundary).
//
// A library and a Wasm plugin are OCI artifacts like any other: each verifies
// through the same Verify call, against the same per-registry trust policy, as a
// catalog entity — there is no separate trust path per kind. Every axis is
// combined into one verification run so the per-registry policy resolution is
// provably identical for all of them.
//
// The axes are read from the closure's own enumeration rather than named here, so
// a section added to the closure cannot quietly bypass verification: it is either
// in that enumeration or recorded there as carrying no verifiable reference.
//
// A registry-resolved item is one freshly fetched from a registry (Tier-3).
// A catalog entity or a library carries a host-less bare identity — its registry
// host and path are bound at pull time from the workspace registry and the kind,
// recovered here from the same binding the puller used — while a content-pinned
// .wasm plugin already carries its host in the URI. All are verified against
// the resolved registry's trust policy. A Tier-1 / Tier-2 / local / external
// item is skipped: it was already verified when first fetched, or never carries
// a registry signature. A fatal verification failure (a required signature that
// is missing, untrusted, or invalid for a non-advisory registry) returns a
// non-nil error and halts the pull.
//
// Verification warning notices are written to diagOut (os.Stderr in production).
//
// targetFor resolves each verifiable item's OCI target for referrer discovery;
// production passes remoteTargetFor (a real remote repository per registry).
//
// It is the one home of this pass, and it stays the one home now that it is
// reachable by name: the primitives it composes are not exported, so a caller
// outside this package can run the pass and cannot assemble a second copy of it
// from the parts.
func VerifyClosureSignatures(ctx context.Context, lf *lockfile.Lockfile, cl closure.Closure, wsCfg workspaceconfig.Config, cacheDir string, diagOut io.Writer, targetFor pullverify.TargetFor) error {
	axes := cl.VerifiableAxes()
	total := 0
	for _, axis := range axes {
		total += len(axis.Refs)
	}
	verifiable := make([]pullverify.Entity, 0, total)
	for _, axis := range axes {
		for _, e := range axis.Refs {
			// Verify only items freshly resolved from a registry (Tier-3).
			// A Tier-1 catalog or Tier-2 cache hit was already verified on
			// the pull that first fetched it (its lockfile entry carries the
			// fact); a local / external item never carries a registry
			// signature.
			if !isRegistryResolved(e.ResolutionPath) {
				continue
			}
			// A catalog entity's or library's URI is a host-less bare identity; the
			// registry host and path are bound at pull time from the workspace
			// registry and the kind, not carried in the URI. Recover that binding
			// from the SAME location rule the puller used so the verify target is the
			// location the artifact was fetched from. A host-carrying artifact (a
			// content-pinned .wasm plugin) does not bind — its URI already carries
			// the reference — so fall back to the URI-derived host for it.
			kind, location, bound := verifySubjectBinding(wsCfg.DefaultRegistry, axis, e)
			if axis.Kind != "" && !bound {
				// A single-kind axis carries host-less identities only, so there is no
				// URI to fall back to: the fall-through below exists for a host-carrying
				// artifact reference, and reaching it with a bare library name would read
				// the version suffix's colon as a host:port authority. Unbound means
				// unverifiable, so skip rather than verify against an invented location.
				continue
			}
			host := registryhost.Of(e.URI)
			if bound {
				host = registryhost.Of(location)
			}
			if !pluginfetchverify.IsRegistryHost(host) {
				continue
			}
			// A registry-resolved item is verified against its OCI manifest digest,
			// the subject referrer discovery attaches signatures to. A registry item
			// that reached here with no manifest digest means the resolution tier
			// failed to record one — referrer discovery would resolve an empty
			// subject and fail cryptically. Halting loudly here (rather than skipping,
			// which would leave the item unverified under a require-signatures policy)
			// names the item and points at the missing digest.
			if e.ManifestDigest == "" {
				return fmt.Errorf("verify: registry artifact %q has no manifest digest to verify against; the resolution tier did not record one", e.URI)
			}
			verifiable = append(verifiable, pullverify.Entity{
				URI:            e.URI,
				ManifestDigest: e.ManifestDigest,
				Host:           host,
				Location:       location,
				Kind:           kind,
			})
		}
	}
	if len(verifiable) == 0 {
		return nil
	}

	results, err := pullverify.Verify(ctx, verifiable, pullverify.Options{
		Config: wsCfg,
		Target: targetFor,
		Warn:   func(notice string) { fmt.Fprintf(diagOut, "  verify: %s\n", notice) },
	})
	if err != nil {
		return err
	}

	factByURI := make(map[string]pullverify.Result, len(results))
	for _, r := range results {
		factByURI[r.URI] = r
	}
	// The fold walks the lockfile's own enumeration of marker-bearing rows, so a
	// verified fact reaches every section that can record one without this pass
	// naming the sections.
	for _, row := range lf.VerifiedFactRows() {
		r, ok := factByURI[row.URI]
		if !ok {
			continue
		}
		if err := cacheBundleBlob(cacheDir, r.BundleDigest, r.Bundle); err != nil {
			return fmt.Errorf("cache signature bundle for %s: %w", r.URI, err)
		}
		row.RecordSignature(pluginfetchverify.SignatureFact(r))
	}

	// Retroactive deprecation discovery: at the same registry-touching moment,
	// discover a signed deprecation marker attached to each verifiable artifact,
	// verify the marker's OWN signature against the original signer OR the
	// registry's trusted deprecation identities, and record the verified marker
	// into the lockfile. Build reads the recorded fact and never re-discovers
	// (the ahead-of-time boundary).
	if err := discoverClosureDeprecations(ctx, lf, verifiable, factByURI, wsCfg, targetFor, diagOut); err != nil {
		return err
	}
	return nil
}

// verifySubjectBinding resolves one closure reference's catalog kind and the
// registry location its signature referrers are discovered against. It reports
// bound=false when no location can be formed, in which case the caller falls back
// to the URI-derived host — the same posture the puller takes when it cannot bind a
// bare identity.
//
// The two shapes differ only in where the kind comes from, and the axis says which:
// a mixed-kind axis carries a collection-site hint per reference, and a
// single-kind axis states the kind once. Both bind through catalog.OCILocation,
// which is the single home of the identity-to-location rule that push publishes at
// and every pull tier fetches from, so verify-location, pull-location and
// push-location cannot diverge.
//
// A library identity is not scope-qualified — unlike a Recipe's provider/name or a
// Plugin's pluginType/name — so its location is the kind path segment plus the bare
// name, which is what the location rule produces for an empty scope.
func verifySubjectBinding(registry string, axis closure.VerifiableAxis, ref closure.VerifiableRef) (kind, location string, bound bool) {
	if axis.Kind == "" {
		// Derive the canonical PascalCase kind from the closure kind hint through the
		// single normalization primitive so the verify pass can consult the org
		// signing policy's kind scope without re-spelling the kind vocabulary.
		location, bound = catalog.BindEntityLocation(registry, ref.KindHint, ref.URI)
		return catalog.EntityKindForHint(ref.KindHint), location, bound
	}
	if registry == "" {
		return axis.Kind, "", false
	}
	identity, _, _ := catalog.SplitVersionedURI(ref.URI)
	return axis.Kind, registry + "/" + catalog.OCILocation(axis.Kind, "", identity), true
}

// verifyPluginHostFunctions re-derives each in-closure plugin's host functions
// from its freshly-cached, content-digest-verified .wasm and verifies the result
// matches the machine-owned record committed in the entity's
// spec.runtime.hostFunctions. It is called only on the pull path (write and
// verify), never on build, which reads the recorded closure and never re-derives
// (the ahead-of-time boundary).
//
// The check is defense-in-depth over the content-digest pin: the digest pin
// guarantees the bytes are the bytes the entity committed to, but it does not
// catch a binary published by something other than the aggrega publish flow
// (which would carry no record, or one that does not match its own imports) or a
// bug in the tooling that records it. Re-deriving from the verified bytes and
// comparing to the record catches both, and HALTS the pull on any divergence
// (ErrWasmHostFunctionsStampMismatch).
//
// The two sides of the lifecycle answer a divergence the same way and for
// different reasons. At publish there is an author present and a file to correct,
// so the refusal names the sync command that re-records the set. At pull there is
// neither: the registry artifact is immutable to the consumer, and a record that
// disagrees with the binary it ships with is evidence the pair was not produced
// by the publish flow at all. Both refuse; only the remedy differs.
//
// It iterates the artifact axis (registry-resolved .wasm pins), the same scope
// the signature verify covers: a host-less / local plugin carries no cached
// artifact and no registry record to re-derive against. The .wasm bytes are read
// once from the cache slot the artifact descent populated, keyed by the artifact
// URI and its content-digest pin. The single derivation primitive
// (wasm.DeriveHostFunctions) is the same one the recording tooling wrote with,
// publish verified against, and the loader registers from, so no two of those
// surfaces can drift.
//
// diagOut receives a per-plugin verification notice (os.Stderr in production).
func verifyPluginHostFunctions(ctx context.Context, cl *closure.Closure, loader closure.RecipeLoader, workspaceRoot, cacheDir string, diagOut io.Writer) error {
	_ = ctx
	// Collect the artifact URIs sorted so the verify order is deterministic and
	// independent of closure order.
	artifactURIs := make([]string, 0, len(cl.Plugins))
	entityByArtifact := make(map[string]string, len(cl.Plugins))
	pinByArtifact := make(map[string]string, len(cl.Plugins))
	for _, art := range cl.Plugins {
		if len(art.ReferencedFrom) == 0 {
			continue
		}
		artifactURIs = append(artifactURIs, art.URI)
		// The artifact is referenced from its Plugin entity; the first
		// referencing entity carries the stamp to verify against (every entity
		// pinning the same .wasm content shares its derived imports).
		entityByArtifact[art.URI] = art.ReferencedFrom[0]
		// The content pin is the closure ref's Digest — the cache slot key the
		// artifact descent wrote under. It is read from the recorded pin, not
		// re-parsed from the (digest-free) artifact URI.
		pinByArtifact[art.URI] = art.Digest
	}
	sort.Strings(artifactURIs)

	for _, artifactURI := range artifactURIs {
		entityURI := entityByArtifact[artifactURI]
		pinDigest := pinByArtifact[artifactURI]
		if pinDigest == "" {
			// No content pin recorded (a host-less / local plugin carries no cached
			// artifact); skip it, mirroring the signature verify's host-less skip.
			continue
		}

		entity, err := loader.LoadPlugin(entityURI)
		if err != nil {
			return fmt.Errorf("load plugin entity %q for host-function verify: %w", entityURI, err)
		}

		wasmBytes, err := oci.ReadCached(workspaceRoot, cacheDir, artifactURI, pinDigest)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound()) {
				// No cached artifact (offline / hermetic): nothing to re-derive
				// against, so there is no stamp to contradict.
				continue
			}
			return fmt.Errorf("read cached .wasm %s for host-function verify: %w", artifactURI, err)
		}

		if err := pluginfetchverify.VerifyHostFunctionStamp(entity, entityURI, artifactURI, wasmBytes, diagOut); err != nil {
			return err
		}
	}
	return nil
}

// cacheBundleBlob writes bundle to the content-addressed signature-bundle blob
// named by its digest under cacheDir, creating the `signatures/` subtree as
// needed. The blob location is spelled once in oci.SignatureBundlePath so the
// build path re-hashes the same slot this write populated. The write is
// idempotent: a blob already present (a re-pull of the same signature) is left
// untouched so the cache stays byte-stable.
func cacheBundleBlob(cacheDir, bundleDigest string, bundle []byte) error {
	if bundleDigest == "" || len(bundle) == 0 {
		return nil
	}
	path := oci.SignatureBundlePath(cacheDir, bundleDigest)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, statErr := os.Stat(path); statErr == nil {
		return nil
	}
	return os.WriteFile(path, bundle, 0o644)
}

// isRegistryResolved reports whether a closure entity resolved by fetching from
// a registry during this run — the only case where a signature is verified at
// pull time. A local-catalog, cache or external resolution is not re-verified
// here.
//
// It delegates to the shared predicate rather than testing the tag itself: a
// string comparison here would keep compiling after the tag was respelled and
// then silently stop matching, leaving signatures unverified with nothing failed
// and nothing logged.
func isRegistryResolved(resolutionPath string) bool {
	return closure.ResolvedFromRegistry(resolutionPath)
}
