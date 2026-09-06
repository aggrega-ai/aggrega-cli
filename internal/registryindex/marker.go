// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"context"
	"errors"
	"fmt"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// errNoMarker signals that an artifact carries no deprecation referrer — the
// common, non-error case. A caller skips a subject that returns it,
// distinguishing "not deprecated" from "deprecated by a marker that does not
// verify".
var errNoMarker = errors.New("no deprecation marker")

// NoMarker returns the signal a discovery pass yields for an artifact that
// carries no deprecation referrer at all, which is the common case rather than a
// failure.
//
// IsNoMarker is how a caller tells it apart from a marker that is present and
// does not verify. Neither is an exported error handle, and deliberately so:
// this is an internal classification that decides whether a pass carries on, and
// no surface reports it to a user as a condition. A condition a user is told to
// match carries a short code and a documented entry; this one carries neither,
// and an exported handle would enrol it in that register by its shape alone.
func NoMarker() error { return errNoMarker }

// IsNoMarker reports whether a discovery failure means there was no marker to
// find.
func IsNoMarker(err error) bool { return errors.Is(err, errNoMarker) }

// LatestMarker fetches and decodes each candidate marker referrer and returns
// the descriptor and decoded marker carrying the newest issuedOn. A blank or
// unparseable issuedOn sorts oldest; the marker manifest digest is the
// deterministic tiebreak when two markers share a timestamp, so the selection is
// independent of the order the registry lists referrers in. Markers that fail to
// fetch or decode are skipped; the last such error surfaces only when no marker
// decoded at all.
func LatestMarker(ctx context.Context, target oras.ReadOnlyTarget, markers []ocispec.Descriptor) (ocispec.Descriptor, deprecation.Marker, error) {
	var (
		bestDesc    ocispec.Descriptor
		bestMarker  deprecation.Marker
		bestTime    time.Time
		bestHasTime bool
		found       bool
		lastErr     error
	)
	for _, mk := range markers {
		payload, ferr := oci.FetchReferrerPayload(ctx, target, mk)
		if ferr != nil {
			lastErr = fmt.Errorf("fetch deprecation marker payload: %w", ferr)
			continue
		}
		decoded, derr := deprecation.Unmarshal(payload)
		if derr != nil {
			lastErr = fmt.Errorf("decode deprecation marker: %w", derr)
			continue
		}
		t, hasTime := decoded.IssuedOnTime()
		if !found || LaterMarker(hasTime, t, mk.Digest.String(), bestHasTime, bestTime, bestDesc.Digest.String()) {
			bestDesc, bestMarker, bestTime, bestHasTime, found = mk, decoded, t, hasTime, true
		}
	}
	if !found {
		if lastErr != nil {
			return ocispec.Descriptor{}, deprecation.Marker{}, lastErr
		}
		return ocispec.Descriptor{}, deprecation.Marker{}, errNoMarker
	}
	return bestDesc, bestMarker, nil
}

// LaterMarker reports whether a candidate marker (aHasTime/aTime/aDigest) ranks
// after the current best (bHasTime/bTime/bDigest) in the newest-issuedOn order.
// A marker with a timestamp beats one without; among timestamped markers the
// newer wins; ties (and two timestamp-less markers) break on the higher digest
// string, a stable order the registry's referrer listing does not provide.
func LaterMarker(aHasTime bool, aTime time.Time, aDigest string, bHasTime bool, bTime time.Time, bDigest string) bool {
	if aHasTime != bHasTime {
		return aHasTime
	}
	if aHasTime && !aTime.Equal(bTime) {
		return aTime.After(bTime)
	}
	return aDigest > bDigest
}

// VerifyMarkerSignature verifies a deprecation marker's OWN signature against
// the honored signer set and returns the verified signature to record. A marker
// is a signed artifact in its own right: its signature is a referrer of the
// marker's manifest digest, discovered and verified exactly as an artifact
// signature is, but against the deprecation-signer trust set rather than the
// artifact one.
//
// It is the single home for that verification. Two passes need it — the
// dependency-resolution discovery that records a marker onto a lockfile row, and
// the index build that records a marker per published version — and a second copy
// would be free to drift on the one judgement that matters, which signer set is
// honored.
//
// origSigner is the original artifact's own verified signer, folded into the
// honored set for a keyless registry so a publisher can withdraw their own
// artifact; nil narrows the honored set to the registry's configured deprecation
// identities. Returns the no-signature sentinel for a marker with no signature at
// all, and the verification error for one whose signature does not verify — so an
// unsigned marker and an untrusted one stay distinguishable to the caller.
func VerifyMarkerSignature(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	markerDigest string,
	policy workspaceconfig.RegistryConfig,
	origSigner *pullverify.Result,
) (*lockfile.VerifiedSignature, error) {
	sigRefs, err := oci.ListReferrers(ctx, target, markerDigest, signing.BundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("list marker signature referrers: %w", err)
	}
	if len(sigRefs) == 0 {
		return nil, signing.ErrNoSignatureFound()
	}

	markerPolicy := markerTrustPolicy(policy, origSigner)
	var lastErr error
	for _, ref := range sigRefs {
		bundle, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		res, verr := pullverify.VerifyBundle(wsCfg, bundle, markerDigest, markerPolicy)
		if verr == nil {
			return &lockfile.VerifiedSignature{
				Mode:         res.Mode,
				Identity:     res.Identity,
				Issuer:       res.Issuer,
				BundleDigest: res.BundleDigest,
			}, nil
		}
		lastErr = verr
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed()
	}
	return nil, lastErr
}

// VerifiedArtifactSigner recovers a published version's OWN verified signer from
// its signature referrers, best-effort, returning nil when none verifies.
//
// The dependency-resolution discovery pass gets this for free: a signature pass
// has already run over the same artifacts and recorded each verified signer. The
// index build has not, and it needs the same fact for the same reason — a keyless
// registry honors a marker signed by the artifact's own publisher, so without it a
// publisher withdrawing their own artifact would go unhonored and the withdrawal
// would never be recorded.
//
// Every step is best-effort and nil is a complete answer: it narrows the honored
// signer set to the registry's configured deprecation identities rather than
// widening it, so a failure here can only under-record a withdrawal, never invent
// one.
func VerifiedArtifactSigner(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	subjectRef string,
	policy workspaceconfig.RegistryConfig,
) *pullverify.Result {
	// Referrers are listed against the subject REFERENCE, matching the discovery
	// pass, while the signature is verified against the resolved MANIFEST digest,
	// which is what a bundle commits to. The two are deliberately different inputs:
	// listing by reference is what the sibling path does, and it does not require
	// the target to resolve a bare digest.
	digest, exists, err := oci.Resolve(ctx, target, subjectRef)
	if err != nil || !exists {
		return nil
	}
	sigRefs, err := oci.ListReferrers(ctx, target, subjectRef, signing.BundleMediaType)
	if err != nil || len(sigRefs) == 0 {
		return nil
	}
	for _, ref := range sigRefs {
		bundle, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			continue
		}
		if res, verr := pullverify.VerifyBundle(wsCfg, bundle, digest, policy); verr == nil {
			return res
		}
	}
	return nil
}

// markerTrustPolicy derives the trust policy a deprecation marker's signature
// verifies against from the registry policy and the original artifact's verified
// signer. A keyed registry pins one public key: the same key IS the same signer,
// so the policy passes through unchanged (same-signer is automatic). A keyless
// registry accepts the marker when its certificate matches the registry's
// trusted_deprecation_identities OR the original signer's verified
// identity/issuer — so the marker policy's trusted-identity OR-set is the
// deprecation set with the original keyless signer appended.
func markerTrustPolicy(policy workspaceconfig.RegistryConfig, origSigner *pullverify.Result) workspaceconfig.RegistryConfig {
	if policy.PublicKey != "" {
		return policy
	}
	markerPolicy := policy
	ids := make([]workspaceconfig.TrustedIdentity, 0, len(policy.TrustedDeprecationIdentities)+1)
	ids = append(ids, policy.TrustedDeprecationIdentities...)
	if origSigner != nil && origSigner.Mode == "keyless" && origSigner.Identity != "" {
		ids = append(ids, workspaceconfig.TrustedIdentity{
			Identity: origSigner.Identity,
			Issuer:   origSigner.Issuer,
		})
	}
	markerPolicy.TrustedIdentities = ids
	return markerPolicy
}
