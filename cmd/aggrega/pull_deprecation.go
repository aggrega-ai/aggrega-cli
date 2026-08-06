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

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
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
	signer, err := verifyMarkerSignature(ctx, wsCfg, target, marker.Digest.String(), policy, origSigner)
	if err != nil {
		return nil, err
	}
	// The recorded diagnostic falls back to the reason when a marker carries no
	// separate message, so the build-time notice is never blank.
	message := decoded.Message
	if message == "" {
		message = decoded.Reason
	}
	return &lockfile.DeprecationRecord{
		Message:  message,
		Severity: string(decoded.Severity),
		Signer:   signer,
	}, nil
}

// verifyMarkerSignature verifies a deprecation marker's OWN signature against the
// honored signer set and returns the verified signature to record. A marker is a
// signed artifact in its own right: its signature is a referrer of the marker's
// manifest digest, discovered and verified exactly as an artifact signature is,
// but against the deprecation-signer trust set rather than the artifact one.
//
// It is the single home for that verification. Two passes need it — the pull-time
// closure discovery that records a marker onto a lockfile row, and the index build
// that records a marker per published version — and a second copy would be free to
// drift on the one judgement that matters, which signer set is honored.
//
// origSigner is the original artifact's own verified signer, folded into the
// honored set for a keyless registry so a publisher can withdraw their own
// artifact; nil narrows the honored set to the registry's configured deprecation
// identities. Returns the no-signature sentinel for a marker with no signature at
// all, and the verification error for one whose signature does not verify — so an
// unsigned marker and an untrusted one stay distinguishable to the caller.
func verifyMarkerSignature(
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
		lastErr = signing.ErrSignatureVerificationFailed
	}
	return nil, lastErr
}

// verifiedArtifactSigner recovers a published version's OWN verified signer from
// its signature referrers, best-effort, returning nil when none verifies.
//
// The closure discovery pass gets this for free: a signature pass has already run
// over the same artifacts and recorded each verified signer. The index build has
// not, and it needs the same fact for the same reason — a keyless registry honors
// a marker signed by the artifact's own publisher, so without it a publisher
// withdrawing their own artifact would go unhonored and the withdrawal would never
// be recorded.
//
// Every step is best-effort and nil is a complete answer: it narrows the honored
// signer set to the registry's configured deprecation identities rather than
// widening it, so a failure here can only under-record a withdrawal, never invent
// one.
func verifiedArtifactSigner(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	target oras.ReadOnlyTarget,
	subjectRef string,
	policy workspaceconfig.RegistryConfig,
) *pullverify.Result {
	// Referrers are listed against the subject REFERENCE, matching the closure
	// discovery pass, while the signature is verified against the resolved MANIFEST
	// digest, which is what a bundle commits to. The two are deliberately different
	// inputs: listing by reference is what the sibling path does, and it does not
	// require the target to resolve a bare digest.
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

// retractionScreen answers the registry-side discovery step from withdrawal facts
// gathered before selection began: which published versions of an artifact carry a
// signature-verified marker at the withdrawal severity, and therefore must not be
// selected by a reference that did not name them.
//
// Its facts are always already in hand — it performs no lookup of its own, which is
// the whole reason the exclusion can happen at selection time at all. A screen that
// reached a registry from inside the partition would put a network dependency inside
// a selection primitive every pull entrypoint shares, and would make the cost scale
// with the number of published versions at every one of those call sites.
//
// Two sources supply those facts, and they differ only in how current they are, so
// each screen carries a provenance clause naming its own source. The local index is
// a snapshot, and its staleness is not smoothed over: a version withdrawn since the
// snapshot was taken is not seen, so the clause states when the snapshot was taken
// and points at the command that refreshes it. A per-repository reading taken during
// the command has no such gap and says so. Either way the screen protects against
// selecting a version known to be withdrawn; only the second can speak to now.
//
// Only a signature-verified record screens a version. An unverified record carries
// no trust basis, and letting one change what the compiler resolves would make an
// unauthenticated registry-side claim load-bearing.
type retractionScreen struct {
	// withdrawnByURI maps an artifact's index URI to its withdrawn versions and
	// the reason recorded against each.
	withdrawnByURI map[string]map[string]string
	// provenance is the parenthesised clause every notice ends in, naming where this
	// screen's facts came from and how current they are. It is rendered at
	// construction rather than derived per notice, so one screen cannot describe its
	// own source two ways.
	provenance string
	out        io.Writer
}

// withdrawnVersions reduces one artifact's recorded deprecations to the versions a
// screen must exclude, mapped to the reason recorded against each.
//
// It is the one home of the honored-withdrawal rule, so every screen — whichever
// source supplied its facts — excludes on exactly the same grounds. A softer
// severity warns without changing what is selected, and an unverified record is not
// a trust decision; both are skipped, and the second is why an unsigned record can
// only ever fail to exclude rather than exclude on unauthenticated evidence.
func withdrawnVersions(deps []VersionDeprecation) map[string]string {
	var withdrawn map[string]string
	for _, d := range deps {
		if d.Signer == nil || d.Marker.Severity != deprecation.SeverityRetracted {
			continue
		}
		if withdrawn == nil {
			withdrawn = map[string]string{}
		}
		withdrawn[d.Version] = d.Marker.Reason
	}
	return withdrawn
}

// newRetractionScreen builds the discovery-step withdrawal screen for a registry
// host from that host's local index, or returns nil when no screen can be formed:
// an empty host, no writer to announce a skip on, no index built for the host, or
// an index recording no verified withdrawal.
//
// A nil screen leaves the discovery step unscreened, which is the honest posture
// rather than a degradation — with no local snapshot there is no verified fact to
// act on, and acting on an unverified one is exactly what this must not do.
func newRetractionScreen(rw *workspaceconfig.ResolvedWorkspace, host string, out io.Writer) catalog.RetractionScreen {
	if host == "" || out == nil {
		return nil
	}
	loaded, err := loadHostIndex(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		return nil
	}
	withdrawnByURI := map[string]map[string]string{}
	for _, rec := range loaded.Records {
		if withdrawn := withdrawnVersions(rec.Deprecations); withdrawn != nil {
			withdrawnByURI[rec.URI] = withdrawn
		}
	}
	if len(withdrawnByURI) == 0 {
		return nil
	}
	return &retractionScreen{
		withdrawnByURI: withdrawnByURI,
		provenance:     indexProvenance(loaded.LastUpdated, loaded.HasTimestamp),
		out:            out,
	}
}

// indexProvenance renders the provenance clause for facts read from the local
// registry index: when the snapshot was taken, so a reader can tell a fresh record
// from a stale one, plus the command that refreshes it. It never claims currency —
// the record says what the registry reported when the index was last built, not what
// it reports now — and it distinguishes an index carrying no build time from one
// whose build time happens to be the zero time.
func indexProvenance(observedAt time.Time, hasObservedAt bool) string {
	if !hasObservedAt {
		return " (recorded in the local registry index, which carries no build time; run `aggrega registry update` to refresh it)"
	}
	return fmt.Sprintf(" (recorded in the local registry index as of %s; run `aggrega registry update` to refresh it)",
		observedAt.UTC().Format(time.RFC3339))
}

// newLiveRetractionScreen builds the discovery-step withdrawal screen for ONE
// artifact from withdrawal facts read from the registry during this command, or
// returns nil when no screen can be formed: no writer to announce a skip on, or no
// verified withdrawal among the facts supplied.
//
// It is the counterpart to the index-backed screen for a reference the local
// snapshot does not carry. The facts arrive already gathered and already verified,
// so the screen itself still reaches no registry: the round trips were spent by the
// caller before selection began, which is what keeps the never-fetch contract every
// selection call site relies on.
//
// Its provenance clause carries no refresh hint, and that asymmetry is the point.
// Facts read during the command have no staleness to warn about, so pointing at the
// command that rebuilds the snapshot would misdescribe where this screen's evidence
// came from.
func newLiveRetractionScreen(reference string, deps []VersionDeprecation, out io.Writer) catalog.RetractionScreen {
	if out == nil {
		return nil
	}
	withdrawn := withdrawnVersions(deps)
	if withdrawn == nil {
		return nil
	}
	return &retractionScreen{
		withdrawnByURI: map[string]map[string]string{indexURIForReference(reference): withdrawn},
		provenance:     " (read from the registry just now)",
		out:            out,
	}
}

// ScreenVersions partitions an artifact's published tags into the versions the
// discovery step may select and the versions withdrawn from it.
//
// A withdrawn tag the discovery step could not have selected anyway — one that is
// not strict SemVer, or a pre-release, which the step skips — is left in the
// eligible list rather than reported as an exclusion. Screening it would change
// nothing about the outcome while turning a genuine no-conformant-version miss
// into a withdrawal that never happened. The eligibility test is the selection
// primitive itself rather than a re-derived rule, so the two cannot disagree about
// what "selectable" means.
func (s *retractionScreen) ScreenVersions(reference string, tags []string) ([]string, []catalog.VersionExclusion) {
	withdrawn, ok := s.withdrawnByURI[indexURIForReference(reference)]
	if !ok {
		return tags, nil
	}
	selectable := func(tag string) bool {
		return oci.SelectLatestVersion([]string{tag}, false /* includePreRelease */) != ""
	}
	eligible := make([]string, 0, len(tags))
	var excludedVersions []string
	for _, tag := range tags {
		if _, hit := withdrawn[tag]; hit && selectable(tag) {
			excludedVersions = append(excludedVersions, tag)
			continue
		}
		eligible = append(eligible, tag)
	}
	if len(excludedVersions) == 0 {
		return tags, nil
	}
	// Ascending published-version order, so the notice and any halt message are
	// byte-stable for the same registry state regardless of the order the registry
	// listed the tags in.
	excluded := make([]catalog.VersionExclusion, 0, len(excludedVersions))
	for _, v := range oci.SortedSemverTags(excludedVersions) {
		excluded = append(excluded, catalog.VersionExclusion{Version: v, Reason: withdrawn[v]})
	}
	return eligible, excluded
}

// ReportSelection names every version the screen withdrew, the reason recorded
// against it, and the version taken in its place. Announcing the substitution is
// what separates this from a quiet fallback: the reference did not name the
// version that was taken, and the evidence for skipping the higher one is not in
// the author's own files, so it is stated where they will read it.
func (s *retractionScreen) ReportSelection(reference, selected string, excluded []catalog.VersionExclusion) {
	for _, x := range excluded {
		if x.Reason == "" {
			fmt.Fprintf(s.out, "  retraction: %s:%s is withdrawn and was not selected\n", reference, x.Version)
			continue
		}
		fmt.Fprintf(s.out, "  retraction: %s:%s is withdrawn and was not selected: %s\n", reference, x.Version, x.Reason)
	}
	fmt.Fprintf(s.out, "  retraction: %s resolved to %s instead%s\n", reference, selected, s.asOfSuffix())
}

// ReportPinned announces that a reference named an exact version carrying a
// withdrawal record. Such a reference still resolves — naming the version is the
// override, and refusing it would break a build that already committed to that
// version — so this reports rather than refuses.
func (s *retractionScreen) ReportPinned(reference, version string) {
	withdrawn, ok := s.withdrawnByURI[indexURIForReference(reference)]
	if !ok {
		return
	}
	reason, hit := withdrawn[version]
	if !hit {
		return
	}
	if reason == "" {
		fmt.Fprintf(s.out, "  retraction: %s:%s is withdrawn; resolving it because the reference names it explicitly%s\n",
			reference, version, s.asOfSuffix())
		return
	}
	fmt.Fprintf(s.out, "  retraction: %s:%s is withdrawn (%s); resolving it because the reference names it explicitly%s\n",
		reference, version, reason, s.asOfSuffix())
}

// asOfSuffix renders where this screen's withdrawal facts came from and how current
// they are, so a reader can tell a fresh fact from a stale one. It is rendered once
// at construction: every notice a screen writes therefore names the same source,
// which is what stops one screen describing its own evidence two ways.
func (s *retractionScreen) asOfSuffix() string {
	return s.provenance
}

// indexURIForReference maps a bound OCI repository reference (`<host>/<path>`, the
// form the entity pull binds and fetches from) to the artifact URI the local index
// keys its records by. The two differ only by the scheme prefix, and deriving one
// from the other here keeps the single mapping out of every call site.
func indexURIForReference(reference string) string {
	return "oci://" + reference
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
