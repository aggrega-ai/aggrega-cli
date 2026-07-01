// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verifyClosureSignatures runs the pull-time signature verification pass
// over the closure's registry-resolved catalog entities AND Wasm plugins,
// and folds the verified facts into the lockfile, caching each verified
// signature bundle as a content-addressed blob for air-gapped
// re-verification. It is called only on the pull write path — never on
// build, which reads the recorded facts from the lockfile and never
// re-verifies signatures (the AOT boundary).
//
// A Wasm plugin is an OCI artifact like any other: it verifies through the
// same Verify call, against the same per-registry trust policy, as a
// catalog entity — there is no separate plugin trust path. Entities and
// plugins are combined into one verification run so the per-registry policy
// resolution is provably identical for both.
//
// A registry-resolved item is one freshly fetched from a registry (Tier-3).
// A catalog entity carries a host-less bare identity — its registry host and
// path are bound at pull time from the workspace registry and the entity kind,
// recovered here from the same binding the puller used — while a content-pinned
// .wasm plugin already carries its host in the URI. Both are verified against
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
func verifyClosureSignatures(ctx context.Context, lf *lockfile.Lockfile, cl closure.Closure, wsCfg workspaceconfig.Config, cacheDir string, diagOut io.Writer, targetFor pullverify.TargetFor) error {
	verifiable := make([]pullverify.Entity, 0, len(cl.Entities)+len(cl.Plugins))
	collect := func(refs []closure.ResolvedRef) error {
		for _, e := range refs {
			// Verify only items freshly resolved from a registry (Tier-3).
			// A Tier-1 catalog or Tier-2 cache hit was already verified on
			// the pull that first fetched it (its lockfile entry carries the
			// fact); a local / external item never carries a registry
			// signature.
			if !isRegistryResolved(e.ResolutionPath) {
				continue
			}
			// A catalog entity's URI is a host-less bare identity; the registry
			// host and path are bound at pull time from the workspace registry
			// and the entity kind, not carried in the URI. Recover that binding
			// from the SAME primitive the puller used so the verify target is the
			// location the entity was fetched from. A host-carrying artifact (a
			// content-pinned .wasm plugin) does not bind — its URI already carries
			// the reference — so fall back to the URI-derived host for it.
			location, bound := catalog.BindEntityLocation(wsCfg.DefaultRegistry, e.KindHint, e.URI)
			host := registryhost.Of(e.URI)
			if bound {
				host = registryhost.Of(location)
			}
			if !isRegistryHost(host) {
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
				return fmt.Errorf("verify: registry entity %q has no manifest digest to verify against; the resolution tier did not record one", e.URI)
			}
			verifiable = append(verifiable, pullverify.Entity{
				URI:            e.URI,
				ManifestDigest: e.ManifestDigest,
				Host:           host,
				Location:       location,
			})
		}
		return nil
	}
	if err := collect(cl.Entities); err != nil {
		return err
	}
	if err := collect(cl.Plugins); err != nil {
		return err
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
	sigDir := filepath.Join(cacheDir, "signatures")
	for i := range lf.Entities {
		r, ok := factByURI[lf.Entities[i].URI]
		if !ok {
			continue
		}
		if err := cacheBundleBlob(sigDir, r.BundleDigest, r.Bundle); err != nil {
			return fmt.Errorf("cache signature bundle for %s: %w", r.URI, err)
		}
		lf.Entities[i].Signature = signatureFact(r)
	}
	for i := range lf.Plugins {
		r, ok := factByURI[lf.Plugins[i].URI]
		if !ok {
			continue
		}
		if err := cacheBundleBlob(sigDir, r.BundleDigest, r.Bundle); err != nil {
			return fmt.Errorf("cache signature bundle for %s: %w", r.URI, err)
		}
		lf.Plugins[i].Signature = signatureFact(r)
	}

	// Same-signer enforcement: a plugin is one publication — its entity
	// manifest and its .wasm artifact must be signed by the SAME identity. Both
	// signatures verified individually above; here the verified facts are paired
	// (the .wasm row references its entity) and their matched signers compared.
	// A split signer halts the pull even though each signature is individually
	// trusted.
	if err := enforcePluginSameSigner(lf); err != nil {
		return err
	}
	return nil
}

// enforcePluginSameSigner halts when a plugin's entity signature and its
// .wasm-artifact signature were produced by different identities. It pairs the
// .wasm (plugins) row with its Plugin entity (the first ReferencedFrom entry,
// the entity that pins the .wasm) and compares the verified matched signer of
// each — the certificate identity for keyless, the pinned-key fingerprint for
// keyed. The comparison runs only when BOTH sides carry a verified signature:
// if a registry does not require signatures and one side is unsigned, there is
// no signer to mismatch; the policy gate (RequiresSignatures) is what demands a
// signature be present in the first place.
func enforcePluginSameSigner(lf *lockfile.Lockfile) error {
	entitySigByURI := make(map[string]*lockfile.VerifiedSignature, len(lf.Entities))
	for i := range lf.Entities {
		if lf.Entities[i].Signature != nil {
			entitySigByURI[lf.Entities[i].URI] = lf.Entities[i].Signature
		}
	}
	for i := range lf.Plugins {
		wasmSig := lf.Plugins[i].Signature
		if wasmSig == nil || len(lf.Plugins[i].ReferencedFrom) == 0 {
			continue
		}
		entityURI := lf.Plugins[i].ReferencedFrom[0]
		entitySig, ok := entitySigByURI[entityURI]
		if !ok {
			continue
		}
		if !sameSigner(entitySig, wasmSig) {
			return fmt.Errorf("plugin %q: entity signed by %s, .wasm artifact %q signed by %s: %w",
				entityURI, signerLabel(entitySig), lf.Plugins[i].URI, signerLabel(wasmSig), signing.ErrPluginSignerMismatch)
		}
	}
	return nil
}

// sameSigner reports whether two verified signatures were produced by the same
// signer: the same mode and the same matched identity (the certificate subject
// and issuer for keyless, the pinned-key fingerprint carried in Identity for
// keyed).
func sameSigner(a, b *lockfile.VerifiedSignature) bool {
	return a.Mode == b.Mode && a.Identity == b.Identity && a.Issuer == b.Issuer
}

// signerLabel renders a verified signature's signer for a mismatch message: the
// identity (and issuer, when present) of the matched signer.
func signerLabel(s *lockfile.VerifiedSignature) string {
	if s.Issuer != "" {
		return fmt.Sprintf("%q (issuer %q)", s.Identity, s.Issuer)
	}
	return fmt.Sprintf("%q", s.Identity)
}

// verifyPluginHostFunctions re-derives each in-closure plugin's host functions
// from its freshly-cached, content-digest-verified .wasm and verifies the result
// matches the machine-owned stamp the publish flow wrote into the entity's
// spec.runtime.hostFunctions. It is called only on the pull path (write and
// verify), never on build, which reads the recorded closure and never re-derives
// (the ahead-of-time boundary).
//
// The check is defense-in-depth over the content-digest pin: the digest pin
// guarantees the bytes are the bytes the entity committed to, but it does not
// catch a binary published by something other than the aggrega publish flow
// (which would carry no stamp, or one that does not match its own imports) or a
// publish-side stamping bug. Re-deriving from the verified bytes and comparing to
// the stamp catches both, and HALTS the pull on any divergence
// (ErrWasmHostFunctionsStampMismatch).
//
// It iterates the artifact axis (registry-resolved .wasm pins), the same scope
// the signature verify covers: a host-less / local plugin carries no cached
// artifact and no registry stamp to re-derive against. The .wasm bytes are read
// once from the cache slot the artifact descent populated, keyed by the artifact
// URI and its content-digest pin. The single derivation primitive
// (wasm.DeriveHostFunctions) is the same one publish stamped with and the loader
// registers from, so the three surfaces cannot drift.
//
// diagOut receives a per-plugin verification notice (os.Stderr in production).
func verifyPluginHostFunctions(ctx context.Context, cl *closure.Closure, loader closure.RecipeLoader, workspaceRoot, cacheDir string, diagOut io.Writer) error {
	_ = ctx
	// Collect the artifact URIs sorted so the verify order is deterministic and
	// independent of closure order.
	artifactURIs := make([]string, 0, len(cl.Plugins))
	entityByArtifact := make(map[string]string, len(cl.Plugins))
	for _, art := range cl.Plugins {
		if len(art.ReferencedFrom) == 0 {
			continue
		}
		artifactURIs = append(artifactURIs, art.URI)
		// The artifact is referenced from its Plugin entity; the first
		// referencing entity carries the stamp to verify against (every entity
		// pinning the same .wasm content shares its derived imports).
		entityByArtifact[art.URI] = art.ReferencedFrom[0]
	}
	sort.Strings(artifactURIs)

	for _, artifactURI := range artifactURIs {
		entityURI := entityByArtifact[artifactURI]
		_, pinDigest, ok := catalog.SplitPluginArtifactURI(artifactURI)
		if !ok {
			// A non-content-pinned artifact reference carries no cache slot keyed
			// by digest; skip it, mirroring the signature verify's host-less skip.
			continue
		}

		entity, err := loader.LoadPlugin(entityURI)
		if err != nil {
			return fmt.Errorf("load plugin entity %q for host-function verify: %w", entityURI, err)
		}

		wasmBytes, err := oci.ReadCached(workspaceRoot, cacheDir, artifactURI, pinDigest)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound) {
				// No cached artifact (offline / hermetic): nothing to re-derive
				// against, so there is no stamp to contradict.
				continue
			}
			return fmt.Errorf("read cached .wasm %s for host-function verify: %w", artifactURI, err)
		}

		derived, err := wasm.DeriveHostFunctions(entityURI, wasmBytes)
		if err != nil {
			return fmt.Errorf("derive host functions from .wasm %s: %w", artifactURI, err)
		}

		stamped := stampedHostFunctions(entity)
		if !canonicalStringSetsEqual(stamped, derived) {
			return fmt.Errorf("plugin %q: stamped spec.runtime.hostFunctions %v does not match %v derived from the verified .wasm %s: %w",
				entityURI, stamped, derived, artifactURI, wasm.ErrWasmHostFunctionsStampMismatch)
		}
		fmt.Fprintf(diagOut, "  verify: plugin %s host functions match the published .wasm\n", entityURI)
	}
	return nil
}

// stampedHostFunctions returns the entity's machine-owned host-function stamp, or
// nil when the entity carries no runtime block.
func stampedHostFunctions(entity *types.Plugin) []string {
	if entity == nil || entity.Spec.Runtime == nil {
		return nil
	}
	return entity.Spec.Runtime.HostFunctions
}

// canonicalStringSetsEqual reports whether two string slices are equal as sets
// (order and duplicates ignored). The derived set is canonical (sorted,
// de-duplicated) by construction; canonicalising the stamp before comparison
// keeps the check robust against a hand-edited stamp whose order or duplicates
// drifted, so the comparison is a true set equality rather than a slice equality.
func canonicalStringSetsEqual(a, b []string) bool {
	ca := slices.Clone(a)
	cb := slices.Clone(b)
	slices.Sort(ca)
	slices.Sort(cb)
	ca = slices.Compact(ca)
	cb = slices.Compact(cb)
	return slices.Equal(ca, cb)
}

// signatureFact turns a pullverify result into the durable lockfile fact.
func signatureFact(r pullverify.Result) *lockfile.VerifiedSignature {
	return &lockfile.VerifiedSignature{
		Mode:         r.Mode,
		Identity:     r.Identity,
		Issuer:       r.Issuer,
		BundleDigest: r.BundleDigest,
	}
}

// remoteTargetFor resolves a real remote OCI repository for an entity's
// registry, returning the manifest digest as the subject reference to discover
// signature referrers against — the manifest the referrer is attached to, which
// the signature also commits to. It serves both axes: a catalog-entity URI
// (host/path, no scheme) and a content-pinned .wasm artifact URI
// (oci://host/path@sha256:...). Authentication delegates to the shared OCI
// credential mechanisms.
func remoteTargetFor(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
	// A catalog entity's URI is a host-less bare identity, so its repository
	// reference is the location bound at pull time (host + path), carried on the
	// entity. A host-carrying artifact (a content-pinned .wasm plugin) leaves
	// Location empty and supplies its reference through the URI.
	repoRef := e.Location
	if repoRef == "" {
		// A content-pinned .wasm artifact URI carries the oci:// scheme; the
		// remote-repository parser wants a bare host/path reference, so strip it.
		repoRef = strings.TrimPrefix(e.URI, "oci://")
		// Strip any version or content-digest suffix from the URI to form the
		// repository reference; the subject ref is the manifest digest the
		// signature commits to.
		if at := strings.LastIndex(repoRef, "@"); at >= 0 {
			repoRef = repoRef[:at]
		}
	}
	repo, err := oci.NewRemoteRepository(repoRef)
	if err != nil {
		return nil, "", err
	}
	return repo, e.ManifestDigest, nil
}

// cacheBundleBlob writes bundle to a content-addressed blob named by its
// digest under dir, creating dir as needed. The write is idempotent: a
// blob already present (a re-pull of the same signature) is left untouched
// so the cache stays byte-stable.
func cacheBundleBlob(dir, bundleDigest string, bundle []byte) error {
	if bundleDigest == "" || len(bundle) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := strings.ReplaceAll(bundleDigest, ":", "-") + ".bundle"
	path := filepath.Join(dir, name)
	if _, statErr := os.Stat(path); statErr == nil {
		return nil
	}
	return os.WriteFile(path, bundle, 0o644)
}

// isRegistryResolved reports whether a closure entity's resolution path
// indicates a fresh registry (Tier-3) fetch — the only case where a
// signature is verified at pull time. A Tier-1 / Tier-2 / external path is
// not re-verified here.
func isRegistryResolved(resolutionPath string) bool {
	return strings.Contains(resolutionPath, "tier-3")
}

// isRegistryHost reports whether host looks like a real registry authority
// — a dotted host (with an optional port) or a host:port — rather than a
// bare host-less catalog name or a relative-path artifact (a leading "."
// from a `./file` reference). A host-less or path-like entity carries no
// registry signature and is skipped by the verify pass.
func isRegistryHost(host string) bool {
	if host == "" || strings.HasPrefix(host, ".") || strings.HasPrefix(host, "/") {
		return false
	}
	if i := strings.IndexByte(host, ':'); i > 0 {
		return true // host:port
	}
	dot := strings.IndexByte(host, '.')
	return dot > 0 && dot < len(host)-1
}
