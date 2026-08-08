// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// verifyClosureSignatures runs the pull-time signature verification pass over
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
func verifyClosureSignatures(ctx context.Context, lf *lockfile.Lockfile, cl closure.Closure, wsCfg workspaceconfig.Config, cacheDir string, diagOut io.Writer, targetFor pullverify.TargetFor) error {
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
		row.RecordSignature(signatureFact(r))
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

		if err := verifyPluginHostFunctionStamp(entity, entityURI, artifactURI, wasmBytes, diagOut); err != nil {
			return err
		}
	}
	return nil
}

// verifyPluginHostFunctionStamp re-derives host functions from a plugin's
// content-verified .wasm bytes and verifies they match the machine-owned stamp
// the publish flow wrote into the entity's spec.runtime.hostFunctions, HALTING
// on any divergence (ErrWasmHostFunctionsStampMismatch). It is the single
// per-plugin host-function check: the pinned closure pass and the shared fetch
// verification home both call it, so no tier carries a second copy of the stamp
// comparison. A stamp-less entity derives against an empty stamp — the same
// benign match a plugin that declares no host functions produces on either side.
//
// entityURI names the plugin in the derivation and any diagnostic; artifactURI
// names the .wasm the bytes came from. diagOut receives the per-plugin match
// notice (os.Stderr in production).
func verifyPluginHostFunctionStamp(entity *types.Plugin, entityURI, artifactURI string, wasmBytes []byte, diagOut io.Writer) error {
	derived, err := wasm.DeriveHostFunctions(entityURI, wasmBytes)
	if err != nil {
		return fmt.Errorf("derive host functions from .wasm %s: %w", artifactURI, err)
	}

	stamped := stampedHostFunctions(entity)
	if !canonicalStringSetsEqual(stamped, derived) {
		return fmt.Errorf("plugin %q: stamped spec.runtime.hostFunctions %v does not match %v derived from the verified .wasm %s: %w",
			entityURI, stamped, derived, artifactURI, wasm.ErrWasmHostFunctionsStampMismatch())
	}
	fmt.Fprintf(diagOut, "  verify: plugin %s host functions match the published .wasm\n", entityURI)
	return nil
}

// fetchedPlugin describes one freshly fetched plugin for the single verification
// home: its registry identity and the manifest digest its signature commits to
// (the signature inputs), plus — when the caller can supply them — the entity
// stamp and its content-verified .wasm bytes (the host-function inputs). A caller
// that resolves a plugin without its entity body or cached bytes leaves those
// fields zero; the host-function check is then a no-op for that fetch, mirroring
// the host-less skip already applied on every tier.
type fetchedPlugin struct {
	// URI is the plugin's canonical identity — a host-less bare identity for a
	// catalog-resolved gate, or a host-carrying oci:// reference for a directly
	// fetched artifact. It is the signature verify's reported subject.
	URI string
	// ManifestDigest is the OCI manifest digest the signature commits to. Empty
	// for a non-registry (embedded / local) plugin, which carries no signature.
	ManifestDigest string
	// Host and Location bind the plugin to the registry its signature referrers
	// are discovered against. Location is the full host+path reference for a
	// host-less identity (bound from the workspace registry and the Plugin kind);
	// a host-carrying URI leaves it empty and supplies the reference through URI.
	Host     string
	Location string
	// Kind is the canonical catalog kind ("Plugin"), consulted by the org signing
	// policy's kind scope.
	Kind string
	// Entity carries the host-function stamp to re-derive against; nil skips the
	// host-function check. EntityURI and ArtifactURI name the entity and its .wasm
	// in the derivation; WasmBytes are the content-verified bytes to derive from.
	Entity      *types.Plugin
	EntityURI   string
	ArtifactURI string
	WasmBytes   []byte
}

// verifyFetchedPlugin is the single home every plugin fetch routes through:
// registry signature verification (against the resolving registry's trust policy)
// and host-function stamp re-derivation. The verdict-socket cache and the CLI
// install call it directly; the pinned closure descent reaches the same two
// verification primitives (pullverify.Verify and verifyPluginHostFunctionStamp)
// across its two structurally separate passes — the host-function pass runs
// before the lockfile is built and the signature pass folds its facts into the
// lockfile — so no tier carries a second copy of either check and no fetched
// plugin reaches use having skipped one.
//
// Signature verification is inherently a pull-online operation — it discovers the
// signature referrers of the plugin's manifest at the registry — so the call
// takes the ctx, the trust config, and the target factory; it is skipped for a
// non-registry / host-less plugin (which carries no registry signature) and while
// the workspace is offline. Host-function re-derivation reads the plugin's
// already content-verified .wasm bytes; it is skipped when the caller supplies no
// entity stamp or no bytes.
//
// It returns the verified signature fact so a pinned caller could record it in
// the lockfile; a cache-not-pin caller (verdict gate, CLI install) discards it —
// the fact is not a reproducibility pin for a plugin that never enters the
// lockfile. Recording the fact is thus the caller's choice, not a second
// function.
func verifyFetchedPlugin(ctx context.Context, in fetchedPlugin, wsCfg workspaceconfig.Config, targetFor pullverify.TargetFor, diagOut io.Writer) (*lockfile.VerifiedSignature, error) {
	var fact *lockfile.VerifiedSignature
	// An explicitly requested verify does not silently skip the network when the
	// workspace is offline: the signature fetch runs and is refused loudly by the
	// egress gate (through targetFor / the live-trust-root fetch) rather than
	// passing as unverified. The offline decision lives at the gate, not here.
	if isRegistryHost(in.Host) && in.ManifestDigest != "" {
		results, err := pullverify.Verify(ctx, []pullverify.Entity{{
			URI:            in.URI,
			ManifestDigest: in.ManifestDigest,
			Host:           in.Host,
			Location:       in.Location,
			Kind:           in.Kind,
		}}, pullverify.Options{
			Config: wsCfg,
			Target: targetFor,
			Warn:   func(notice string) { fmt.Fprintf(diagOut, "  verify: %s\n", notice) },
		})
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			fact = signatureFact(results[0])
		}
	}
	if in.Entity != nil && len(in.WasmBytes) > 0 {
		if err := verifyPluginHostFunctionStamp(in.Entity, in.EntityURI, in.ArtifactURI, in.WasmBytes, diagOut); err != nil {
			return nil, err
		}
	}
	return fact, nil
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
// remoteTargetForWith binds an egress-gate HTTP client to the resolution: the
// client is nil for an offline workspace, in which case a referrer fetch is
// refused loudly with ErrOfflineEgress. An explicitly requested verify never
// silently skips the network step when offline — it refuses, so a
// pinned-trust-root offline verify (which never reaches this fetch) stays the
// only offline-capable verify path.
func remoteTargetForWith(httpClient *http.Client) pullverify.TargetFor {
	return func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		if httpClient == nil {
			return nil, "", egress.ErrOfflineEgress()
		}
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
		repo, err := oci.NewRemoteRepository(repoRef, httpClient)
		if err != nil {
			return nil, "", err
		}
		return repo, e.ManifestDigest, nil
	}
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
