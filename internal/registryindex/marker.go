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
	"oras.land/oras-go/v2/errdef"

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

// unreadError marks a failure as one to READ rather than a verdict: the registry
// did not answer, so what was not read says nothing about what is there. It reads
// as the failure it wraps, so a caller that only reports the failure reports the
// same words either way.
type unreadError struct{ err error }

func (e unreadError) Error() string { return e.err.Error() }

func (e unreadError) Unwrap() error { return e.err }

// IsUnread reports whether a failure from this package's reads is a failure to
// read — a listing, a referrer or a payload the registry did not deliver — rather
// than a verdict on what was read. A caller that keeps an answer, such as an index,
// must not keep one a passing failure shaped, and reads this to tell the two
// apart: a marker that did not verify is an answer; a marker whose signature could
// not be fetched is not.
func IsUnread(err error) bool {
	var u unreadError
	return errors.As(err, &u)
}

// IsAbsent reports whether a failed read is the registry's answer that it holds
// nothing at the address asked: a manifest, a blob or a repository it does not
// have, the condition oci.Resolve reports as a tag that does not exist. That is an
// answer, not a failure to read — asking again gets the same answer — so a caller
// telling the two apart treats it as what is there, never as IsUnread.
func IsAbsent(err error) bool {
	return errors.Is(err, errdef.ErrNotFound) || errors.Is(err, oci.ErrFetchNotFound())
}

// unreadUnlessAbsent marks err as a failure to read (IsUnread) unless the registry
// answered that nothing is at the address asked, which is returned as it is.
func unreadUnlessAbsent(err error) error {
	if IsAbsent(err) {
		return err
	}
	return unreadError{err}
}

// LatestMarker fetches and decodes each candidate marker referrer and returns
// the descriptor and decoded marker carrying the newest issuedOn. A blank or
// unparseable issuedOn sorts oldest; the marker manifest digest is the
// deterministic tiebreak when two markers share a timestamp, so the selection is
// independent of the order the registry lists referrers in. Markers that fail to
// fetch or decode are skipped; the last such error surfaces only when no marker
// decoded at all.
func LatestMarker(ctx context.Context, target oras.ReadOnlyTarget, markers []ocispec.Descriptor) (ocispec.Descriptor, deprecation.Marker, error) {
	desc, marker, _, err := latestMarker(ctx, target, markers)
	return desc, marker, err
}

// LatestMarkerFully is LatestMarker for a caller that keeps the answer until the
// referrers change, and so must not keep one a passing failure shaped. It selects
// the same marker, and returns a failure to read (IsUnread) instead wherever a
// candidate marker's payload could not be fetched: the newest of the markers that
// were read may then not be the newest attached. A marker that was fetched and does
// not decode is still passed over, since reading it again decodes the same bytes
// the same way, and so is one the registry answers it does not hold (IsAbsent).
func LatestMarkerFully(ctx context.Context, target oras.ReadOnlyTarget, markers []ocispec.Descriptor) (ocispec.Descriptor, deprecation.Marker, error) {
	desc, marker, unfetched, err := latestMarker(ctx, target, markers)
	if unfetched != nil {
		return ocispec.Descriptor{}, deprecation.Marker{}, unreadError{unfetched}
	}
	return desc, marker, err
}

// latestMarker is the one walk over candidate markers both readings share.
// unfetched is the last payload fetch that failed without an answer, if any.
func latestMarker(ctx context.Context, target oras.ReadOnlyTarget, markers []ocispec.Descriptor) (ocispec.Descriptor, deprecation.Marker, error, error) {
	var (
		bestDesc    ocispec.Descriptor
		bestMarker  deprecation.Marker
		bestTime    time.Time
		bestHasTime bool
		found       bool
		lastErr     error
		unfetched   error
	)
	for _, mk := range markers {
		payload, ferr := oci.FetchReferrerPayload(ctx, target, mk)
		if ferr != nil {
			lastErr = fmt.Errorf("fetch deprecation marker payload: %w", ferr)
			if !IsAbsent(ferr) {
				unfetched = lastErr
			}
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
			return ocispec.Descriptor{}, deprecation.Marker{}, unfetched, lastErr
		}
		return ocispec.Descriptor{}, deprecation.Marker{}, unfetched, errNoMarker
	}
	return bestDesc, bestMarker, unfetched, nil
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
//
// Where no signature verifies and the answer is not known — the signature listing
// could not be read, or a signature could not be fetched, or could not be judged
// for a reason other than the bundle itself (as FindVerifiedSigner tells them
// apart) — the error is a failure to read (IsUnread): the marker is not shown to
// be untrusted, only not shown to be honored. The registry answering that it does
// not hold the marker's manifest, or a listed signature (IsAbsent), is an answer
// and not a failure to read: the marker is then not honored, as it would be with
// no signature at all.
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
		return nil, unreadUnlessAbsent(fmt.Errorf("list marker signature referrers: %w", err))
	}
	if len(sigRefs) == 0 {
		return nil, signing.ErrNoSignatureFound()
	}

	markerPolicy := markerTrustPolicy(policy, origSigner)
	var lastErr, unanswered error
	for _, ref := range sigRefs {
		bundle, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			lastErr = ferr
			if !IsAbsent(ferr) {
				unanswered = ferr
			}
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
		if !doesNotVerify(bundle, verr) {
			unanswered = verr
		}
	}
	if unanswered != nil {
		return nil, unreadError{unanswered}
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed()
	}
	return nil, lastErr
}

// VerifiedArtifactSigner recovers a published version's OWN verified signer from
// its signature referrers, best-effort, returning nil when none verifies. Where
// several verify, the signer is the earliest of them, as FindVerifiedSigner orders
// them, so a signature attached later — by any identity — never becomes the
// original signer whose withdrawals the version honors.
//
// The dependency-resolution discovery pass gets this for free: a signature pass
// has already run over the same artifacts and recorded each verified signer, the
// earliest by the same ordering (pullverify.EarlierSignature), so the two passes
// honor one original signer. The index build has not, and it needs the same fact
// for the same reason — a keyless registry honors a marker signed by the
// artifact's own publisher, so without it a publisher withdrawing their own
// artifact would go unhonored and the withdrawal would never be recorded. It reads
// the fact through ReadArtifactSigner, which says when the read did not finish.
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
	res, _ := ReadArtifactSigner(ctx, wsCfg, target, subjectRef, policy)
	return res
}

// ReadArtifactSigner is VerifiedArtifactSigner for a caller that must know whether
// the answer is complete. The signer is the same; err is non-nil, and a failure to
// read (IsUnread), where the version could not be resolved or FindVerifiedSigner
// could not finish the question. A caller judging a withdrawal against the honored
// set reads it so that a set narrowed by a passing failure is not mistaken for one
// that excludes the marker's signer.
func ReadArtifactSigner(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	subjectRef string,
	policy workspaceconfig.RegistryConfig,
) (*pullverify.Result, error) {
	// Referrers are listed against the subject REFERENCE, matching the discovery
	// pass, while the signature is verified against the resolved MANIFEST digest,
	// which is what a bundle commits to. The two are deliberately different inputs:
	// listing by reference is what the sibling path does, and it does not require
	// the target to resolve a bare digest.
	digest, exists, err := oci.Resolve(ctx, target, subjectRef)
	if err != nil {
		return nil, unreadError{err}
	}
	if !exists {
		return nil, nil
	}
	res, err := FindVerifiedSigner(ctx, wsCfg, target, subjectRef, digest, policy)
	if err != nil {
		return nil, unreadError{err}
	}
	return res, nil
}

// VerifiedSignerAt is VerifiedArtifactSigner for a caller that has already
// resolved subjectRef to its manifest digest, so the resolve is not repeated. The
// signature is verified against that digest, which is what a bundle commits to,
// and it is the same best-effort answer: the earliest signature that verifies,
// nil when none does, and nil as well when FindVerifiedSigner could not finish the
// question.
func VerifiedSignerAt(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	subjectRef, digest string,
	policy workspaceconfig.RegistryConfig,
) *pullverify.Result {
	res, _ := FindVerifiedSigner(ctx, wsCfg, target, subjectRef, digest, policy)
	return res
}

// FindVerifiedSigner is the one walk over a published version's signature
// referrers. It asks whether any of them verifies under policy, against digest,
// the manifest digest a bundle commits to, and returns the verified signer of the
// EARLIEST that does. VerifiedSignerAt is this walk read best-effort.
//
// Earliest is decided by pullverify.EarlierSignature and never by the order the
// registry lists referrers in, which says nothing about when each was attached.
// So a version signed again — by the same identity or another — keeps the
// signature the transparency log recorded first, and every reader of this walk
// reads that same one: the index records its signer and its log time as the
// version's publication date, and the withdrawal check honors markers from its
// signer as the original one. The pull-time signature pass ranks a pulled
// artifact's signatures by the same function, so the signature a lockfile
// records, and the original signer a pull honors withdrawals from, are this one
// too. Every signature is therefore read and judged rather than the walk stopping
// at the first that verifies: a version carrying one more signature costs one more
// referrer fetch and one more verification.
//
// Unlike that reading it says when it could not finish: it returns nil and a nil
// error only when every signature referrer was read and judged and none verifies,
// which includes a version that carries none. It returns an error where the answer
// is not known — the referrer listing could not be completed, or no signature
// verified while one could not be fetched or could not be judged because something
// other than the bundle failed, such as a trust anchor that could not be obtained.
// A caller that acts on "none verifies" by attaching a signature of its own reads
// this rather than the best-effort answer, because to it a registry that failed to
// answer must not look like a version that carries no signature.
//
// Where one signature verifies and another could not be read or judged, the
// version carries a signature that verifies, so the answer is known and no error is
// returned: the earliest is taken among the signatures that were read, as the
// publisher's own date statements are (oci.ReadPublishedAt).
//
// A bundle that does not verify — a wrong signer, a broken or tampered signature,
// a policy admitting no signer, or a payload that is not a bundle at all — is an
// answer about that bundle, and the walk moves on to the next; it is never a
// candidate for the earliest.
func FindVerifiedSigner(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	subjectRef, digest string,
	policy workspaceconfig.RegistryConfig,
) (*pullverify.Result, error) {
	sigRefs, err := oci.ListReferrers(ctx, target, subjectRef, signing.BundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("list signature referrers: %w", err)
	}
	var (
		earliest   *pullverify.Result
		unanswered error
	)
	for _, ref := range sigRefs {
		bundle, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			unanswered = fmt.Errorf("fetch signature %s: %w", ref.Digest, ferr)
			continue
		}
		res, verr := pullverify.VerifyBundle(wsCfg, bundle, digest, policy)
		if verr != nil {
			if !doesNotVerify(bundle, verr) {
				unanswered = fmt.Errorf("verify signature %s: %w", ref.Digest, verr)
			}
			continue
		}
		if earliest == nil || pullverify.EarlierSignature(res, earliest) {
			earliest = res
		}
	}
	if earliest != nil {
		return earliest, nil
	}
	return nil, unanswered
}

// doesNotVerify reports whether a verification failure is a verdict on the bundle
// itself rather than a failure to judge it. The verification sentinels are matched
// through their accessors; a payload that does not parse as a bundle is a verdict
// too, since no policy and no anchor could ever verify it.
func doesNotVerify(bundle []byte, verr error) bool {
	if errors.Is(verr, signing.ErrSignatureVerificationFailed()) ||
		errors.Is(verr, signing.ErrUntrustedIdentity()) ||
		errors.Is(verr, signing.ErrNoTrustedIdentities()) {
		return true
	}
	_, perr := signing.LoadBundle(bundle)
	return perr != nil
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
