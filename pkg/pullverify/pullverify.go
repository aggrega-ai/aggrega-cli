// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pullverify runs signature verification at pull/lock time. It is
// the lock-time half of the signing model: pull discovers each pinned
// entity's signature referrers, verifies them against the registry's trust
// policy, records a verified-signature fact for the earliest that verifies,
// and caches its bundle as a content-addressed blob for air-gapped
// re-verification. Build never imports this package — verification is a
// recorded fact build reads from the lockfile, not a step build re-runs (the
// AOT boundary: build is offline and lockfile-only).
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
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
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
// blob. Mode is "keyless" or "keyed"; Issuer is set for keyless, and Identity
// for both (for keyed, the pinned key's fingerprint).
//
// LoggedAt is the time the transparency log recorded a keyless signature, as
// the verified log entry attests it, in UTC. It is the zero time for a keyed
// signature, which no log records, and for a keyless one whose verified
// entries carry no time. It is a verified fact like the others, so a caller
// may use it as a trustworthy time where it is set.
type Result struct {
	URI          string
	Mode         string
	Identity     string
	Issuer       string
	BundleDigest string
	Bundle       []byte
	LoggedAt     time.Time
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
// facts. Where an entity carries several signatures that verify, its fact is
// the earliest of them by EarlierSignature, never the first the registry
// happens to list. A fatal verification failure (a required signature that is
// missing, untrusted, or invalid for a non-advisory registry) returns a
// non-nil error and halts the pull. A required signature whose lookup could
// not be completed halts it too: a check that did not run has not passed.
// Where no signature is required, neither an absent signature nor an
// unanswerable lookup halts, because neither leaves anything to verify.
// Advisory registries downgrade failures to warnings; the global skip and
// per-registry skip disable verification loudly.
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
		// halts on a missing one. "Missing" covers both ways a signature can
		// fail to turn up — the registry answered and nothing is attached, and
		// the registry could not be asked — because neither leaves a signature
		// to verify. The two part company only once a signature is required.
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

		res, verr := verifyEntity(ctx, opts.Config, e, policy, configured, opts.Target)
		if verr != nil {
			if !requireSignature {
				// Two outcomes leave nothing to verify, and neither is evidence
				// against the artifact. They are admitted separately because
				// they are different facts about the registry.
				if errors.Is(verr, signing.ErrNoSignatureFound()) {
					// The registry answered and nothing is attached:
					// verify-if-present means nothing to verify. Not a failure.
					continue
				}
				if errors.Is(verr, oci.ErrReferrerDiscoveryUnavailable()) {
					// The registry could not be asked, so nothing was
					// established either way. Treating that as a failed
					// verification would refuse an artifact on the strength of
					// a question nobody managed to put — but it is said out
					// loud, because a check that did not run is not a check
					// that passed, and silence would read as a clean unsigned
					// pull. Where a signature IS required this case is
					// deliberately not admitted here: it falls through to the
					// halt below, because a required check that could not run
					// has not passed.
					warn(fmt.Sprintf("signature verification could not run for %s "+
						"(the registry could not answer the signature lookup and no signature is required; continuing): %v", e.URI, verr))
					continue
				}
			}
			if policy.Advisory {
				warn(fmt.Sprintf("signature verification failed for %s (advisory; continuing): %v", e.URI, verr))
				continue
			}
			return nil, fmt.Errorf("verifying the signature of %s: %w", e.URI, verr)
		}
		if res != nil {
			results = append(results, *res)
		}
	}
	return results, nil
}

// verifyEntity fetches and verifies one entity's signatures and returns the
// earliest that verifies, by EarlierSignature. Returns a nil Result with no
// error when the entity has no reachable registry (skipped). Returns
// ErrNoSignatureFound when the registry reports no signature referrer, and an
// error wrapping oci.ErrReferrerDiscoveryUnavailable when the referrer lookup
// could not be completed at all — the caller distinguishes the two because only
// the first is an answer.
//
// The entity is trusted when ANY of its signatures verifies, and a signature
// that could not be fetched or does not verify beside one that does changes
// nothing. Where none verifies, the error is the last failure met in the order
// the registry lists the signatures. Every signature is read and judged rather
// than the walk stopping at the first that verifies, so an entity carrying one
// more signature costs one more fetch and one more verification: the listing
// order is an order of referrer digests and says nothing about when each
// signature was attached, so stopping early would let a signature attached
// later stand in for the entity's own.
func verifyEntity(ctx context.Context, cfg workspaceconfig.Config, e Entity, policy workspaceconfig.RegistryConfig, configured bool, targetFor TargetFor) (*Result, error) {
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
		return nil, signing.ErrNoSignatureFound()
	}

	// Track the last error so a wholly-unverifiable set surfaces a meaningful
	// failure.
	var (
		earliest *Result
		lastErr  error
	)
	for _, ref := range refs {
		bundleBytes, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		res, verr := VerifyBundle(cfg, bundleBytes, e.ManifestDigest, policy)
		if verr != nil {
			lastErr = verr
			continue
		}
		if earliest == nil || EarlierSignature(res, earliest) {
			earliest = res
		}
	}
	if earliest != nil {
		earliest.URI = e.URI
		return earliest, nil
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed()
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
func VerifyBundle(cfg workspaceconfig.Config, bundleBytes []byte, artifactDigest string, policy workspaceconfig.RegistryConfig) (*Result, error) {
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

	anchor, err := keylessAnchor(cfg, policy)
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
		LoggedAt:     matched.LoggedAt,
	}, nil
}

// EarlierSignature reports whether verified signature a ranks before b in the
// order that decides which of an artifact's signatures stands for its
// publication where more than one verifies. It is the one ordering: the
// pull-time pass ranks by it when it records an artifact's verified signature,
// and so does the registry index when it dates a version and names its signer,
// so a later signature, however it was attached, never displaces the earliest
// for either of them, and the two rank by one rule rather than by two copies of
// it free to drift apart.
//
// A signature carrying a transparency-log time ranks before one carrying none,
// and of two that carry one the earlier time ranks first. The log's time is the
// publication date wherever a verified signature carries one, so a signature
// without one is never preferred over a signature that attests a time. A keyed
// signature carries no log time, but a keyed and a keyless signature never both
// verify under one policy — VerifyBundle verifies keyed against a pinned key and
// keyless only where none is pinned — so in practice the rule orders keyless
// signatures by their log time, and among keyed ones, all of them the pinned
// key's, decides nothing but which bundle is recorded.
//
// A tie — two equal log times, which the log records to the second, or two
// signatures carrying none — breaks on the lower bundle digest, an order the
// registry's referrer listing does not affect.
func EarlierSignature(a, b *Result) bool {
	aLogged, bLogged := !a.LoggedAt.IsZero(), !b.LoggedAt.IsZero()
	if aLogged != bLogged {
		return aLogged
	}
	if aLogged && !a.LoggedAt.Equal(b.LoggedAt) {
		return a.LoggedAt.Before(b.LoggedAt)
	}
	return a.BundleDigest < b.BundleDigest
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
// LIVE-ONLY for the no-pinned-root branch: with no pinned root, the keyless
// anchor is the real public-good Sigstore trust root fetched and cached via
// TUF — the one network-touching branch of the verify pass — obtained through
// the network-egress gate, so an offline workspace refuses the fetch loudly
// before any dial. An air-gapped deployment pins a trusted_root and never
// reaches the live branch; the pinned-root branch and the verification logic
// that consumes either anchor are fully exercised offline.
func keylessAnchor(cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error) {
	if policy.TrustedRoot != "" {
		return signing.TrustedMaterialFromRootPath(policy.TrustedRoot)
	}
	return livetrust.TrustedRoot(cfg)
}

// blobDigest returns the sha256:... digest of b — the content address of
// the cached bundle blob.
func blobDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum)
}
