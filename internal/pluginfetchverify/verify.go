// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginfetchverify

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Fetched describes one freshly fetched plugin for the single verification
// home: its registry identity and the manifest digest its signature commits to
// (the signature inputs), plus — when the caller can supply them — the entity
// stamp and its content-verified .wasm bytes (the host-function inputs). A caller
// that resolves a plugin without its entity body or cached bytes leaves those
// fields zero; the host-function check is then a no-op for that fetch, mirroring
// the host-less skip already applied on every tier.
type Fetched struct {
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

// AsksRegistryForSignature reports whether Verify asks a registry for in's
// signature: in names a registry host and carries the manifest digest a signature
// commits to. A fetch it does not hold for carries no registry signature, so
// Verify asks nothing about it.
func (in Fetched) AsksRegistryForSignature() bool {
	return IsRegistryHost(in.Host) && in.ManifestDigest != ""
}

// Verify is the single home every plugin fetch routes through:
// registry signature verification (against the resolving registry's trust policy)
// and host-function stamp re-derivation. The verdict-socket cache, the CLI
// install and an import acquiring a core importer of the compiler's release call
// it directly; the pinned closure descent reaches the same two
// verification primitives (pullverify.Verify and VerifyHostFunctionStamp)
// across its two structurally separate passes — the host-function pass runs
// before the lockfile is built and the signature pass folds its facts into the
// lockfile — so no tier carries a second copy of either check and no fetched
// plugin reaches use having skipped one.
//
// Signature verification is inherently a pull-online operation — it discovers the
// signature referrers of the plugin's manifest at the registry — so the call
// takes the ctx, the trust config, and the target factory; it is skipped for a
// non-registry / host-less plugin (which carries no registry signature), and under
// the offline posture the egress gate refuses it. Host-function re-derivation
// reads the plugin's already content-verified .wasm bytes; it is skipped when the
// caller supplies no entity stamp or no bytes. That second half is
// VerifyWithoutSignature, which a caller that asks no registry runs alone.
//
// It returns the verified result — the fact SignatureFact turns into a record,
// together with the bundle bytes that fact names — or nil where no signature was
// verified. The verdict-socket cache records the fact in the trust record beside
// the lockfile and caches the bundle, so a build can hold the plugin to it; the CLI
// install discards it, because an installed extension command is held to no record,
// and so does an import acquiring a core importer, whose module every later read
// holds to the digest the compiler carries for it.
// What a caller does with the result is thus the caller's choice, not a second
// function.
func Verify(ctx context.Context, in Fetched, wsCfg workspaceconfig.Config, targetFor pullverify.TargetFor, diagOut io.Writer) (*pullverify.Result, error) {
	var verified *pullverify.Result
	// An explicitly requested verify does not silently skip the network when the
	// workspace is offline: the signature fetch runs and is refused loudly by the
	// egress gate (through targetFor / the live-trust-root fetch) rather than
	// passing as unverified. The offline decision lives at the gate, not here.
	if in.AsksRegistryForSignature() {
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
			result := results[0]
			verified = &result
		}
	}
	if err := VerifyWithoutSignature(in, diagOut); err != nil {
		return nil, err
	}
	return verified, nil
}

// VerifyWithoutSignature is the half of Verify that asks no registry anything: it
// re-derives the host functions of in's .wasm and holds them to its entity's
// stamp (VerifyHostFunctionStamp), and it is a no-op where in carries no entity
// stamp or no bytes. Verify runs it after the signature pass, so the half run on
// its own and the half Verify runs cannot come to disagree about when the check
// applies.
//
// On its own it verifies no signature, so it is for a caller that then reports
// the plugin's signature as not checked, never one about to use the plugin on
// the strength of this verification: a fetch whose plugin is used goes through
// Verify. The chokepoint guard pins its callers for that reason.
func VerifyWithoutSignature(in Fetched, diagOut io.Writer) error {
	if in.Entity == nil || len(in.WasmBytes) == 0 {
		return nil
	}
	return VerifyHostFunctionStamp(in.Entity, in.EntityURI, in.ArtifactURI, in.WasmBytes, diagOut)
}

// VerifyHostFunctionStamp re-derives host functions from a plugin's
// content-verified .wasm bytes and verifies they match the machine-owned record
// committed in the entity's spec.runtime.hostFunctions, HALTING
// on any divergence (ErrWasmHostFunctionsStampMismatch). It is the single
// per-plugin host-function check: the pinned closure pass and the shared fetch
// verification home (through VerifyWithoutSignature) both call it, so no tier
// carries a second copy of the stamp comparison. A stamp-less entity derives against an empty stamp — the same
// benign match a plugin that declares no host functions produces on either side.
//
// entityURI names the plugin in the derivation and any diagnostic; artifactURI
// names the .wasm the bytes came from. diagOut receives the per-plugin match
// notice (os.Stderr in production).
func VerifyHostFunctionStamp(entity *types.Plugin, entityURI, artifactURI string, wasmBytes []byte, diagOut io.Writer) error {
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

// SignatureFact turns a pullverify result into the durable lockfile fact. It is
// the one conversion: the shared fetch home and the closure signature pass both
// record the same four fields from the same result, so a fact recorded on one
// tier cannot carry a different shape from a fact recorded on another.
func SignatureFact(r pullverify.Result) *lockfile.VerifiedSignature {
	return &lockfile.VerifiedSignature{
		Mode:         r.Mode,
		Identity:     r.Identity,
		Issuer:       r.Issuer,
		BundleDigest: r.BundleDigest,
	}
}

// IsRegistryHost reports whether host names a registry, so that a registry
// signature exists to check, rather than being the first segment of a
// host-less catalog identity or of a relative-path artifact. A host-less or
// path-like entity carries no registry signature and is skipped by the verify
// pass.
//
// It is the verification question asked of the one reading of a registry host,
// and it forms no reading of its own: it is registryhost.NamesHost, which reads
// host through registryhost.Normalize and judges it by registryhost.IsHost — the
// predicate the configuration load holds every configured host to. So a host a
// configuration may name is never one this filter skips: "localhost" is verified,
// and a host written in capitals or with its default port is verified under the
// policy configured for it.
//
// It is kept under this name because the shared fetch home and the install path
// that skips it for a local reference both ask it here; the closure signature
// pass asks the same predicate through catalog.SignatureSubjectOf, which the
// build's recorded-signature check shares with it.
func IsRegistryHost(host string) bool {
	return registryhost.NamesHost(host)
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
