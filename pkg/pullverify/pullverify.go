// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pullverify runs signature verification at pull/lock time. It is
// the lock-time half of the signing model: pull discovers each pinned
// entity's signature referrer, verifies it against the registry's trust
// policy, records a verified-signature fact, and caches the bundle as a
// content-addressed blob for air-gapped re-verification. Build never
// imports this package — verification is a recorded fact build reads from
// the lockfile, not a step build re-runs (the AOT boundary: build is
// offline and lockfile-only).
//
// The verification LOGIC lives in pkg/signing (keyed-bundle and
// keyless-bundle verify); this package orchestrates it across a closure's
// entities, resolving each entity's trust policy and turning the verify
// outcome into a durable fact. The package is deliberately decoupled from
// the lockfile and closure types: callers supply the entities and a
// per-entity target factory, and receive neutral Result values to fold
// into the lockfile.
package pullverify

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Entity is the minimal pin a verification needs: the canonical URI, the
// OCI manifest digest the signature commits to, and the registry host the
// entity resolved from.
type Entity struct {
	URI string
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the cryptographic verify subject. It is the manifest
	// coordinate, not the portable content digest the caller uses for the
	// cache pin and integrity check.
	ManifestDigest string
	// Host is the registry host the entity resolved from, already
	// normalised via registryhost.Of. Empty for a non-registry (local /
	// catalog) entity, which is never verified.
	Host string
	// Location is the full OCI repository reference (host + path) the entity
	// resolved from, when the reference cannot be recovered from URI alone. A
	// catalog entity's URI is a host-less bare identity — the registry host and
	// path are bound at pull time from the workspace registry and the entity
	// kind — so its referrer-discovery target is resolved from Location, not
	// URI. Empty for a host-carrying artifact (a content-pinned .wasm plugin),
	// whose URI already carries the reference; the target factory then falls
	// back to the URI.
	Location string
	// Kind is the entity's canonical catalog kind (PascalCase, e.g. "Component"
	// or "Plugin"), used to consult the workspace signing policy's kind scope.
	// The collection site derives it from the closure kind hint through the
	// single kind-normalization primitive. Empty when the kind could not be
	// classified, in which case an active org signing policy still requires a
	// signature (the fail-safe below refuses to silently exempt an
	// unclassifiable artifact).
	Kind string
}

// Result is the verified-signature fact for one entity, plus the cached
// bundle bytes the caller writes to the closure as a content-addressed
// blob. Mode is "keyless" or "keyed"; Identity/Issuer are set for keyless.
type Result struct {
	URI          string
	Mode         string
	Identity     string
	Issuer       string
	BundleDigest string
	Bundle       []byte
}

// TargetFor returns the read-only OCI target and subject reference to
// discover an entity's signature referrers against. The subjectRef is the
// reference the target resolves to the entity's manifest descriptor
// (typically the content digest or version tag). A nil target with a nil
// error means the entity has no reachable registry (local/catalog) and is
// skipped.
type TargetFor func(e Entity) (target oras.ReadOnlyTarget, subjectRef string, err error)

// Options configures a Verify run.
type Options struct {
	// Config is the resolved workspace config carrying the per-registry
	// trust policy and the global skip.
	Config workspaceconfig.Config
	// Target resolves a per-entity target for referrer discovery.
	Target TargetFor
	// Warn reports a non-fatal verification notice (advisory downgrade,
	// global skip). Never nil in production; a nil Warn drops notices.
	Warn func(string)
}

// Verify runs the verification pass over entities and returns the verified
// facts. A fatal verification failure (a required signature that is
// missing, untrusted, or invalid for a non-advisory registry) returns a
// non-nil error and halts the pull. Advisory registries downgrade failures
// to warnings; the global skip and per-registry skip disable verification
// loudly.
func Verify(ctx context.Context, entities []Entity, opts Options) ([]Result, error) {
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}

	if opts.Config.SkipSignatureVerification {
		warn("signature verification is GLOBALLY DISABLED (skip_signature_verification: true) — " +
			"no artifact's signature is being checked, including the hub baseline")
		return nil, nil
	}

	var results []Result
	for _, e := range entities {
		host := e.Host
		if host == "" {
			host = registryhost.Of(e.URI)
		}
		policy, configured := opts.Config.RegistryTrustPolicy(host)

		// A signature is required when EITHER the top-level (all-registries)
		// policy requires it for this entity's kind OR this host's own signing
		// block requires it. The two are the one trust stance read at two scopes;
		// the top-level layer forces a signature on an in-scope kind even from a
		// host whose own block requires none.
		orgRequired := opts.Config.SigningRequiredForKind(e.Kind)
		// Fail-safe: an active top-level policy must not silently exempt an
		// unclassifiable artifact. When the policy is on but the kind could not
		// be determined, require a signature rather than tolerate a missing one.
		if opts.Config.Signing.Required && e.Kind == "" {
			orgRequired = true
		}
		registryRequired := configured && policy.SigningRequiredForKind(e.Kind)
		if configured && policy.Signing.Required && e.Kind == "" {
			registryRequired = true
		}

		// Default-for-unknown: a host with no explicit policy, no baseline, and
		// no required stance verifies only if a signature is present, and never
		// halts on a missing one.
		requireSignature := registryRequired || orgRequired

		// A per-host break-glass skip disables verification for this host — but
		// it does NOT defeat the top-level required policy. An org-required
		// in-scope artifact is still verified here; only the global
		// skip_signature_verification switch (handled above) can disable that.
		// A per-host required stance is not immune to the per-host skip: setting
		// both on one host is the allowed-but-perverse push-required + pull-skip
		// combination, and the skip wins on the verify side.
		if policy.SkipSignatureVerification && !orgRequired {
			warn(fmt.Sprintf("signature verification skipped for registry %q (skip_signature_verification: true)", host))
			continue
		}

		res, verr := verifyEntity(ctx, e, policy, configured, opts.Target)
		if verr != nil {
			if errors.Is(verr, signing.ErrNoSignatureFound) && !requireSignature {
				// Unknown / non-mandatory registry, no signature present:
				// verify-if-present means nothing to verify. Not a failure.
				continue
			}
			if policy.Advisory {
				warn(fmt.Sprintf("signature verification failed for %s (advisory; continuing): %v", e.URI, verr))
				continue
			}
			return nil, fmt.Errorf("pullverify: %s: %w", e.URI, verr)
		}
		if res != nil {
			results = append(results, *res)
		}
	}
	return results, nil
}

// verifyEntity fetches and verifies one entity's signature. Returns a nil
// Result with no error when the entity has no reachable registry (skipped).
// Returns ErrNoSignatureFound when no signature referrer is present.
func verifyEntity(ctx context.Context, e Entity, policy workspaceconfig.RegistryConfig, configured bool, targetFor TargetFor) (*Result, error) {
	if targetFor == nil {
		return nil, nil
	}
	target, subjectRef, err := targetFor(e)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	if target == nil {
		// Local / catalog entity — never carries a registry signature.
		return nil, nil
	}

	refs, err := oci.ListReferrers(ctx, target, subjectRef, signing.BundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("list signature referrers: %w", err)
	}
	if len(refs) == 0 {
		return nil, signing.ErrNoSignatureFound
	}

	// Verify the entity's content digest against any attached signature.
	// A registry may carry more than one signature referrer; the entity is
	// trusted if ANY one verifies against the policy. Track the last error
	// so a wholly-unverifiable set surfaces a meaningful failure.
	var lastErr error
	for _, ref := range refs {
		bundleBytes, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		res, verr := VerifyBundle(bundleBytes, e.ManifestDigest, policy)
		if verr == nil {
			res.URI = e.URI
			return res, nil
		}
		lastErr = verr
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed
	}
	return nil, lastErr
}

// VerifyBundle verifies one signature bundle against the registry trust
// policy and returns the verified fact (Result without URI) on success.
// Keyed mode is chosen when the policy pins a public key; otherwise keyless
// against the policy's trusted identities and trust anchor. artifactDigest is
// the OCI manifest digest the signature commits to — the cryptographic
// subject.
//
// The recorded Identity/Issuer is the bundle's actual matched signer: for
// keyless mode the certificate's verified identity reported by the verify
// pass; for keyed mode the pinned-key fingerprint (a keyed bundle carries
// no certificate, so the pinned key IS the signer). The recorded identity
// is what the closure-aware same-signer check compares across a plugin's
// entity and .wasm.
//
// It is the single bundle-verification primitive: the pull-time signature
// pass verifies each entity's signature through it, and the retroactive
// deprecation-marker pass verifies a marker's own signature through it too
// (passing a policy whose trusted set is the deprecation-signer set), so both
// surfaces share one crypto path and cannot drift.
func VerifyBundle(bundleBytes []byte, artifactDigest string, policy workspaceconfig.RegistryConfig) (*Result, error) {
	digest := blobDigest(bundleBytes)
	if policy.PublicKey != "" {
		if err := signing.VerifyKeyedBundle(bundleBytes, []byte(policy.PublicKey), artifactDigest); err != nil {
			return nil, err
		}
		return &Result{
			Mode:         "keyed",
			Identity:     keyedSignerIdentity(policy.PublicKey),
			BundleDigest: digest,
			Bundle:       bundleBytes,
		}, nil
	}

	ids := make([]signing.TrustedIdentity, 0, len(policy.TrustedIdentities))
	for _, id := range policy.TrustedIdentities {
		ids = append(ids, signing.TrustedIdentity{
			Identity:       id.Identity,
			IdentityRegexp: id.IdentityRegexp,
			Issuer:         id.Issuer,
		})
	}

	anchor, err := keylessAnchor(policy)
	if err != nil {
		return nil, err
	}
	matched, err := signing.VerifyKeylessBundle(bundleBytes, anchor, ids, artifactDigest)
	if err != nil {
		return nil, err
	}
	return &Result{
		Mode:         "keyless",
		Identity:     matched.Identity,
		Issuer:       matched.Issuer,
		BundleDigest: digest,
		Bundle:       bundleBytes,
	}, nil
}

// keyedSignerIdentity is the same-signer identity for a keyed verification:
// a keyed bundle carries no certificate, so the pinned public key itself is
// the signer. A stable fingerprint of the pinned key (the sha256 of its PEM)
// names that signer so two keyed bundles signed under the same pinned key
// compare equal, and two under different keys compare unequal.
func keyedSignerIdentity(publicKeyPEM string) string {
	sum := sha256.Sum256([]byte(publicKeyPEM))
	return fmt.Sprintf("keyed:sha256:%x", sum)
}

// keylessAnchor builds the keyless trust anchor for the policy: a pinned
// trust root (air-gapped, offline) when policy.TrustedRoot is set,
// otherwise the live public-good root fetched via TUF.
//
// LIVE-ONLY for the no-pinned-root branch: with no pinned root, the
// keyless anchor is the real public-good Sigstore trust root fetched and
// cached via TUF — the one network-touching branch of the verify pass. An
// air-gapped deployment pins a trusted_root and never reaches it; the
// pinned-root branch and the verification logic that consumes either
// anchor are fully exercised offline.
func keylessAnchor(policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error) {
	if policy.TrustedRoot != "" {
		return signing.TrustedMaterialFromRootPath(policy.TrustedRoot)
	}
	live, err := root.NewLiveTrustedRoot(tuf.DefaultOptions())
	if err != nil {
		return nil, fmt.Errorf("%w: live TUF fetch: %v", signing.ErrTrustRootLoad, err)
	}
	return live, nil
}

// blobDigest returns the sha256:... digest of b — the content address of
// the cached bundle blob.
func blobDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum)
}
