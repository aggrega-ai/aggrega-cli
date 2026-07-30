// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// discoverClosureDeprecations runs the retroactive deprecation-marker discovery
// pass over the same registry-resolved artifacts the signature verify pass
// covers, folding a verified marker into the lockfile as a DeprecationRecord. It
// runs at the one registry-touching moment (pull/lock); build reads the recorded
// fact and never re-discovers (the ahead-of-time boundary).
//
// A deprecation marker is a signed OCI referrer of the artifact it deprecates.
// For each verifiable artifact this lists the artifact's deprecation referrers,
// and for a present marker verifies the marker's OWN signature. The marker's
// signature is honored when it was produced by the ORIGINAL artifact's signer
// (same-signer, recorded by the signature pass in sigFactByURI) OR by a member
// of the registry's trusted_deprecation_identities — so a registry owner or a
// security team can retract an artifact the original publisher can no longer
// reach, without being trusted to sign the artifacts themselves.
//
// A marker with no signature, or one whose signature does not verify against the
// honored set, is NOT recorded. Under a require-signatures (non-advisory) policy
// an unsigned or untrusted marker is treated as an untrusted signature would be:
// it HALTS the pull rather than being silently dropped. Under an advisory or
// non-requiring policy it is warned about and skipped, leaving the row
// un-deprecated. Verification warnings write to diagOut.
func discoverClosureDeprecations(
	ctx context.Context,
	lf *lockfile.Lockfile,
	verifiable []pullverify.Entity,
	sigFactByURI map[string]pullverify.Result,
	wsCfg workspaceconfig.Config,
	targetFor pullverify.TargetFor,
	diagOut io.Writer,
) error {
	if wsCfg.SkipSignatureVerification || targetFor == nil {
		return nil
	}

	recordByURI := make(map[string]*lockfile.DeprecationRecord, len(verifiable))
	for _, e := range verifiable {
		host := e.Host
		if host == "" {
			host = registryhost.Of(e.URI)
		}
		policy, configured := wsCfg.RegistryTrustPolicy(host)
		if policy.SkipSignatureVerification {
			continue
		}

		var origSigner *pullverify.Result
		if r, ok := sigFactByURI[e.URI]; ok {
			rr := r
			origSigner = &rr
		}

		rec, verr := discoverEntityDeprecation(ctx, wsCfg, e, policy, origSigner, targetFor)
		if verr != nil {
			// A missing marker is not an error — most artifacts are not
			// deprecated. Only an untrusted/unsigned MARKER (one that exists but
			// does not verify) is a policy decision.
			if errors.Is(verr, errNoDeprecationMarker) {
				continue
			}
			requireSignature := configured && policy.SigningRequiredForKind(e.Kind)
			if requireSignature && !policy.Advisory {
				return fmt.Errorf("pullverify: %s: %w", e.URI, verr)
			}
			fmt.Fprintf(diagOut, "  deprecation: %s: marker present but not honored (%v); not recorded\n", e.URI, verr)
			continue
		}
		if rec != nil {
			recordByURI[e.URI] = rec
			fmt.Fprintf(diagOut, "  deprecation: %s deprecated (%s) by %s\n", e.URI, rec.Severity, deprecationSignerLabel(rec.Signer))
		}
	}

	for i := range lf.Entities {
		if rec, ok := recordByURI[lf.Entities[i].URI]; ok {
			lf.Entities[i].Deprecation = rec
		}
	}
	for i := range lf.Plugins {
		if rec, ok := recordByURI[lf.Plugins[i].URI]; ok {
			lf.Plugins[i].Deprecation = rec
		}
	}
	return nil
}

// carryForwardDeprecation copies each unchanged pin's deprecation record from
// the prior lockfile into lf, so a warm-cache (Tier-2) re-pull that does not
// re-touch the registry preserves a deprecation discovered on an earlier
// registry-touching pull. A record is carried forward only when the new row
// already carries none (a fresh Tier-3 re-discovery wins) and the prior and new
// rows share the same content digest (an unchanged pin — a changed pin is a
// different artifact whose deprecation status must be re-discovered, not
// inherited).
//
// A nil prior is a no-op: the first pull has nothing to carry forward, and
// neither does a full refresh, which deliberately reads no prior pin set.
func carryForwardDeprecation(lf *lockfile.Lockfile, prior *lockfile.Lockfile) {
	if prior == nil {
		return
	}
	type priorPin struct {
		digest string
		rec    *lockfile.DeprecationRecord
	}
	priorByURI := make(map[string]priorPin, len(prior.Entities)+len(prior.Plugins))
	for i := range prior.Entities {
		priorByURI[prior.Entities[i].URI] = priorPin{prior.Entities[i].Digest, prior.Entities[i].Deprecation}
	}
	for i := range prior.Plugins {
		priorByURI[prior.Plugins[i].URI] = priorPin{prior.Plugins[i].Digest, prior.Plugins[i].Deprecation}
	}
	for i := range lf.Entities {
		if lf.Entities[i].Deprecation != nil {
			continue
		}
		if p, ok := priorByURI[lf.Entities[i].URI]; ok && p.rec != nil && p.digest == lf.Entities[i].Digest {
			lf.Entities[i].Deprecation = p.rec
		}
	}
	for i := range lf.Plugins {
		if lf.Plugins[i].Deprecation != nil {
			continue
		}
		if p, ok := priorByURI[lf.Plugins[i].URI]; ok && p.rec != nil && p.digest == lf.Plugins[i].Digest {
			lf.Plugins[i].Deprecation = p.rec
		}
	}
}

// carryForwardExternalInputs copies the prior lockfile's recorded external-input
// pins into lf. External inputs are render-time remote-input pins written by
// build (after render) and by acquire — they are not part of the catalog
// closure, so buildLockfile produces none. Without this carry-forward a pull
// would rewrite the lockfile with an empty external-inputs list, wiping the pins
// a prior build or acquire recorded and forcing the next build to re-acquire
// from the network. The list is copied wholesale (buildLockfile leaves it empty)
// with each entry's RequiredBy slice deep-copied so lf never aliases the prior
// lockfile's backing arrays.
//
// A nil prior is a no-op: the first pull has nothing to carry forward, and a
// bare --upgrade is a full refresh that reads no prior pin set, so it carries
// nothing and the pins refresh on the next build.
func carryForwardExternalInputs(lf *lockfile.Lockfile, prior *lockfile.Lockfile) {
	if prior == nil {
		return
	}
	if len(prior.ExternalInputs) == 0 {
		return
	}
	carried := make([]lockfile.ExternalInput, len(prior.ExternalInputs))
	for i, x := range prior.ExternalInputs {
		x.RequiredBy = append([]string(nil), x.RequiredBy...)
		carried[i] = x
	}
	lf.ExternalInputs = carried
}

// errNoDeprecationMarker signals that an artifact carries no deprecation
// referrer — the common, non-error case. discoverClosureDeprecations skips a
// row that returns it, distinguishing "not deprecated" from "deprecated by a
// marker that does not verify".
var errNoDeprecationMarker = errors.New("no deprecation marker")

// latestDeprecationMarker fetches and decodes each candidate marker referrer
// and returns the descriptor and decoded marker carrying the newest issuedOn. A
// blank or unparseable issuedOn sorts oldest; the marker manifest digest is the
// deterministic tiebreak when two markers share a timestamp, so the selection is
// independent of the order the registry lists referrers in. Markers that fail to
// fetch or decode are skipped; the last such error surfaces only when no marker
// decoded at all.
func latestDeprecationMarker(ctx context.Context, target oras.ReadOnlyTarget, markers []ocispec.Descriptor) (ocispec.Descriptor, deprecation.Marker, error) {
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
		if !found || laterMarker(hasTime, t, mk.Digest.String(), bestHasTime, bestTime, bestDesc.Digest.String()) {
			bestDesc, bestMarker, bestTime, bestHasTime, found = mk, decoded, t, hasTime, true
		}
	}
	if !found {
		if lastErr != nil {
			return ocispec.Descriptor{}, deprecation.Marker{}, lastErr
		}
		return ocispec.Descriptor{}, deprecation.Marker{}, errNoDeprecationMarker
	}
	return bestDesc, bestMarker, nil
}

// laterMarker reports whether a candidate marker (aHasTime/aTime/aDigest) ranks
// after the current best (bHasTime/bTime/bDigest) in the newest-issuedOn order.
// A marker with a timestamp beats one without; among timestamped markers the
// newer wins; ties (and two timestamp-less markers) break on the higher digest
// string, a stable order the registry's referrer listing does not provide.
func laterMarker(aHasTime bool, aTime time.Time, aDigest string, bHasTime bool, bTime time.Time, bDigest string) bool {
	if aHasTime != bHasTime {
		return aHasTime
	}
	if aHasTime && !aTime.Equal(bTime) {
		return aTime.After(bTime)
	}
	return aDigest > bDigest
}

// discoverEntityDeprecation lists one artifact's deprecation referrers and, for
// a present marker, decodes it and verifies its own signature against the
// honored signer set (the original artifact signer plus the registry's trusted
// deprecation identities). It returns errNoDeprecationMarker when no marker is
// attached; a wrapped signing error when a marker is present but does not
// verify; and the verified DeprecationRecord otherwise.
func discoverEntityDeprecation(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	e pullverify.Entity,
	policy workspaceconfig.RegistryConfig,
	origSigner *pullverify.Result,
	targetFor pullverify.TargetFor,
) (*lockfile.DeprecationRecord, error) {
	target, subjectRef, err := targetFor(e)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	if target == nil {
		return nil, errNoDeprecationMarker
	}

	markers, err := oci.ListReferrers(ctx, target, subjectRef, deprecation.ArtifactType)
	if err != nil {
		return nil, fmt.Errorf("list deprecation referrers: %w", err)
	}
	if len(markers) == 0 {
		return nil, errNoDeprecationMarker
	}

	// The most recent marker wins when more than one is attached: each marker's
	// payload is decoded and the one carrying the newest issuedOn is the
	// authoritative one, a blank or unparseable issuedOn sorting oldest. The
	// marker manifest digest is the deterministic tiebreak when two markers
	// share the same issuedOn, so the selection does not depend on the order the
	// registry lists referrers in. A marker that fails to decode is skipped, so
	// one malformed sibling does not mask a well-formed newer marker.
	marker, decoded, err := latestDeprecationMarker(ctx, target, markers)
	if err != nil {
		return nil, err
	}

	// The marker is a signed artifact: its signature is a referrer of the
	// MARKER's own manifest digest, discovered and verified the same way an
	// artifact signature is — but against the deprecation-signer trust set.
	markerDigest := marker.Digest.String()
	sigRefs, err := oci.ListReferrers(ctx, target, markerDigest, signing.BundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("list marker signature referrers: %w", err)
	}
	if len(sigRefs) == 0 {
		return nil, signing.ErrNoSignatureFound
	}

	markerPolicy := deprecationMarkerPolicy(policy, origSigner)
	var lastErr error
	for _, ref := range sigRefs {
		bundle, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		if ferr != nil {
			lastErr = ferr
			continue
		}
		res, verr := pullverify.VerifyBundle(wsCfg, bundle, markerDigest, markerPolicy)
		if verr == nil {
			// The recorded diagnostic falls back to the reason when a marker
			// carries no separate message, so the build-time notice is never blank.
			message := decoded.Message
			if message == "" {
				message = decoded.Reason
			}
			return &lockfile.DeprecationRecord{
				Message:  message,
				Severity: string(decoded.Severity),
				Signer: &lockfile.VerifiedSignature{
					Mode:         res.Mode,
					Identity:     res.Identity,
					Issuer:       res.Issuer,
					BundleDigest: res.BundleDigest,
				},
			}, nil
		}
		lastErr = verr
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed
	}
	return nil, lastErr
}

// deprecationMarkerPolicy derives the trust policy a deprecation marker's
// signature verifies against from the registry policy and the original
// artifact's verified signer. A keyed registry pins one public key: the same
// key IS the same signer, so the policy passes through unchanged (same-signer is
// automatic). A keyless registry accepts the marker when its certificate
// matches the registry's trusted_deprecation_identities OR the original signer's
// verified identity/issuer — so the marker policy's trusted-identity OR-set is
// the deprecation set with the original keyless signer appended.
func deprecationMarkerPolicy(policy workspaceconfig.RegistryConfig, origSigner *pullverify.Result) workspaceconfig.RegistryConfig {
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

// deprecationSignerLabel renders a recorded marker signer for a diagnostic
// notice: the identity (and issuer, when present) that authored the marker.
func deprecationSignerLabel(s *lockfile.VerifiedSignature) string {
	if s == nil {
		return "unknown signer"
	}
	if s.Issuer != "" {
		return fmt.Sprintf("%q (issuer %q)", s.Identity, s.Issuer)
	}
	return fmt.Sprintf("%q", s.Identity)
}
