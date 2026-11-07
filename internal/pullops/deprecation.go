// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
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
// (same-signer, recorded by the signature pass in sigFactByRow) OR by a member
// of the registry's trusted_deprecation_identities — so a registry owner or a
// security team can retract an artifact the original publisher can no longer
// reach, without being trusted to sign the artifacts themselves.
//
// verifiable are the questions the signature pass put to a registry, and
// sigFactByRow its answers, keyed by the row each question was asked for; a marker
// discovered for a question is recorded under the same row, so it reaches the rows
// the signature reached and no row of another kind sharing the reference.
//
// The original signer is the earliest of the artifact's signatures that verify,
// because that is the one the signature pass records (pullverify.EarlierSignature).
// The index build and the verdict-attachment discovery read the earliest through
// registryindex.VerifiedArtifactSigner, which ranks by the same function, so all
// three take one original signer from the signatures they read, and a signature
// the transparency log recorded later, by another trusted identity, makes that
// identity's markers count on none of them.
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
	verifiable []signatureQuestion,
	sigFactByRow map[lockfile.RowKey]pullverify.Result,
	wsCfg workspaceconfig.Config,
	targetFor pullverify.TargetFor,
	diagOut io.Writer,
) error {
	if wsCfg.SkipSignatureVerification || targetFor == nil {
		return nil
	}

	recordByRow := make(map[lockfile.RowKey]*lockfile.DeprecationRecord, len(verifiable))
	for _, q := range verifiable {
		e := q.entity
		host := e.Host
		if host == "" {
			host = registryhost.Of(e.URI)
		}
		policy, configured := wsCfg.RegistryTrustPolicy(host)
		if policy.SkipSignatureVerification {
			continue
		}

		var origSigner *pullverify.Result
		if r, ok := sigFactByRow[q.row]; ok {
			rr := r
			origSigner = &rr
		}

		rec, verr := discoverEntityDeprecation(ctx, wsCfg, e, policy, origSigner, targetFor)
		if verr != nil {
			if herr := honorDeprecationDiscoveryError(e, policy, configured, verr, diagOut); herr != nil {
				return herr
			}
			continue
		}
		if rec != nil {
			recordByRow[q.row] = rec
			fmt.Fprintf(diagOut, "  deprecation: %s %s by %s\n", e.URI, deprecation.Severity(rec.Severity).StatedAs(), deprecationSignerLabel(rec.Signer))
		}
	}

	// The fold walks the lockfile's own enumeration of marker-bearing rows, so a
	// discovered marker reaches every section that can record one without this pass
	// naming the sections, and it reaches every row pinning an artifact this pass
	// asked about, by the same join the signature fold uses: see verifiedJoin.
	markers := joinVerified(verifiable, recordByRow)
	for _, row := range lf.VerifiedFactRows() {
		if rec, ok := markers.answer(row); ok {
			row.RecordDeprecation(rec)
		}
	}
	return nil
}

// carryForwardVerifiedFacts copies each unchanged pin's recorded facts — its
// verified signature and its verified deprecation marker — from the prior
// lockfile into lf, so a re-pull that reads a dependency from the content cache
// rather than the registry records the same facts the pull that fetched it did.
// Neither fact is discovered again for an entry the cache served that carries its
// facts and whose signature a build accepts: the signature pass and the marker
// discovery read the registry, and such a cache hit does not touch it. Without the
// carry-forward a warm re-pull would therefore drop both facts, and the lockfile it
// writes would differ from the one the cold pull wrote over the same dependencies.
// It runs before the signature pass because that pass reads what it carried: a
// cache-served entry whose row still records no signature, where one is required,
// or records one a build does not accept, is the one the pass asks the registry
// about (VerifyClosureSignatures).
//
// A fact is carried only onto a row that records none of its own, so what this
// run discovered at the registry always wins, and only across an unchanged pin:
// the prior and new rows share the content digest, and their manifest digests
// agree wherever both record one. The manifest takes part because both facts are
// read off it — a signature and a deprecation marker are referrers of the
// manifest — so a pin whose manifest moved is a different artifact whose facts
// must be discovered again rather than inherited. A row that records no manifest
// digest on one side carries on the content digest alone, which is what keeps a
// lockfile written before cache hits recorded their manifest from losing a
// recorded deprecation on its next pull.
//
// Both sides are read from the lockfile's own enumeration of fact-bearing rows,
// so every section that can record a fact is carried forward. A section covered
// by discovery but not by this carry-forward would look correct on the pull that
// found the fact and lose it on the next warm-cache re-pull — a section-shaped
// gap that no single pull can show.
//
// A nil prior is a no-op: the first pull has nothing to carry forward, and
// neither does a full refresh, which deliberately reads no prior pin set.
func carryForwardVerifiedFacts(lf *lockfile.Lockfile, prior *lockfile.Lockfile) {
	if prior == nil {
		return
	}
	priorRows := prior.VerifiedFactRows()
	priorByKey := make(map[lockfile.RowKey]lockfile.VerifiedFactRow, len(priorRows))
	for _, row := range priorRows {
		priorByKey[row.Key()] = row
	}
	for _, row := range lf.VerifiedFactRows() {
		p, ok := priorByKey[row.Key()]
		if !ok || !samePin(p, row) {
			continue
		}
		if row.Signature() == nil && p.Signature() != nil {
			row.RecordSignature(p.Signature())
		}
		if row.Deprecation() == nil && p.Deprecation() != nil {
			row.RecordDeprecation(p.Deprecation())
		}
	}
}

// samePin reports whether two rows for one URI pin the same artifact, which is
// the condition a recorded fact is carried across: the same content digest, and
// manifest digests that agree wherever both rows record one.
func samePin(prior, now lockfile.VerifiedFactRow) bool {
	if prior.Digest != now.Digest {
		return false
	}
	return prior.ManifestDigest == "" || now.ManifestDigest == "" || prior.ManifestDigest == now.ManifestDigest
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

// honorDeprecationDiscoveryError decides what a failed marker discovery means for
// the run: an error to halt on, or nil to leave the artifact un-deprecated and
// carry on. A diagnostic is written on the nil path, so a marker that was dropped
// is never dropped silently.
//
// It is the single home of that decision, and it is shared rather than repeated
// because the two axes discovery runs over — the closure's verifiable references
// and the verdict attachments recorded beside the lockfile — must not come to
// disagree about when an unhonored marker is fatal. Two copies would be free to
// drift on exactly the judgement that matters, and the drift would show only on a
// workspace whose registry policy sits in the narrow band where they differ.
//
// The rule itself: a missing marker is not an error at all, because most artifacts
// are not deprecated. A marker that IS present and does not verify is a policy
// decision — fatal under a registry that both requires signatures and is not
// advisory, reported and skipped otherwise. A lookup that never finished asking
// whether a marker is attached falls under the same policy, because nothing was
// learned that could stand in for the check: it halts where a marker that does not
// verify halts, and is reported and skipped where that one is. Its report is its
// own, though, and says the lookup did not complete rather than that a marker was
// found, since the reader would otherwise go looking for a withdrawal that may not
// exist.
func honorDeprecationDiscoveryError(
	e pullverify.Entity,
	policy workspaceconfig.RegistryConfig,
	configured bool,
	verr error,
	diagOut io.Writer,
) error {
	if registryindex.IsNoMarker(verr) {
		return nil
	}
	if configured && policy.SigningRequiredForKind(e.Kind) && !policy.Advisory {
		return fmt.Errorf("verifying the deprecation marker published for %s: %w", e.URI, verr)
	}
	var unfinished *unfinishedMarkerLookup
	if errors.As(verr, &unfinished) {
		fmt.Fprintf(diagOut, "  deprecation: %s: marker lookup did not complete (%v); nothing recorded\n", e.URI, verr)
		return nil
	}
	fmt.Fprintf(diagOut, "  deprecation: %s: marker present but not honored (%v); not recorded\n", e.URI, verr)
	return nil
}

// unfinishedMarkerLookup marks a discovery that never learned whether a marker is
// attached: the artifact's registry target could not be obtained, or the listing
// of its deprecation referrers did not complete. It is how
// honorDeprecationDiscoveryError tells that apart from a marker that was listed and
// then not honored.
//
// The mark is set where the lookup fails rather than read off the cause, because
// the cause cannot tell the two apart: a failed referrer listing carries the same
// condition whether it was the listing of the artifact's markers or the listing of
// a listed marker's own signatures, and only the first leaves the marker's
// presence unknown. Its text is the wrapped error's own, so a halt on it reads
// exactly as it would without the mark, and the wrapped causes stay reachable.
type unfinishedMarkerLookup struct{ err error }

func (u *unfinishedMarkerLookup) Error() string { return u.err.Error() }
func (u *unfinishedMarkerLookup) Unwrap() error { return u.err }

// discoverVerdictPluginDeprecation runs the same marker discovery over ONE
// verdict attachment's plugin artifact that the closure pass runs over a pinned
// reference, and returns the verified withdrawal to record beside the lockfile, or
// nil when there is none to record.
//
// It exists because the closure pass structurally cannot reach these artifacts. Its
// input is the closure's verifiable axes, and a plugin attached at a verdict role is
// cache-not-pin: it never enters the closure and has no lockfile row, so a marker on
// it was discoverable by nothing. That is the gap this closes, and it is a gap in
// what is REACHED rather than in what the closure pass walks.
//
// Everything that decides whether a marker is honored is shared with that pass
// rather than re-derived: discoverEntityDeprecation verifies the marker's own
// signature against the honored signer set, and honorDeprecationDiscoveryError
// decides what a marker that does not verify means. So the two axes can differ in
// WHAT they walk and never in what they trust.
//
// The artifact's own verified signer is recovered rather than inherited, through the
// same best-effort read the index build uses for the same reason: a keyless registry
// honors a marker signed by the artifact's own publisher, so without it a publisher
// withdrawing their own plugin would go unhonored. The closure pass gets this fact
// free from the signature pass that ran ahead of it; this pass has no such pass to
// read from. A nil answer narrows the honored set to the registry's configured
// deprecation identities, so a failure there can only under-record a withdrawal.
//
// A workspace or registry that skips signature verification discovers nothing, which
// matches the closure pass exactly: a marker is honored on the strength of its
// signature, so where signatures are not checked there is no basis to honor one.
func discoverVerdictPluginDeprecation(
	ctx context.Context,
	wsCfg workspaceconfig.Config,
	e pullverify.Entity,
	targetFor pullverify.TargetFor,
	diagOut io.Writer,
) (*lockfile.DeprecationRecord, error) {
	if wsCfg.SkipSignatureVerification || targetFor == nil || e.ManifestDigest == "" {
		return nil, nil
	}
	policy, configured := wsCfg.RegistryTrustPolicy(e.Host)
	if policy.SkipSignatureVerification {
		return nil, nil
	}

	var origSigner *pullverify.Result
	if target, subjectRef, terr := targetFor(e); terr == nil && target != nil {
		origSigner = registryindex.VerifiedArtifactSigner(ctx, wsCfg, target, subjectRef, policy)
	}

	rec, verr := discoverEntityDeprecation(ctx, wsCfg, e, policy, origSigner, targetFor)
	if verr != nil {
		return nil, honorDeprecationDiscoveryError(e, policy, configured, verr, diagOut)
	}
	if rec != nil {
		fmt.Fprintf(diagOut, "  deprecation: %s %s by %s\n", e.URI, deprecation.Severity(rec.Severity).StatedAs(), deprecationSignerLabel(rec.Signer))
	}
	return rec, nil
}

// discoverEntityDeprecation lists one artifact's deprecation referrers and, for
// a present marker, decodes it and verifies its own signature against the
// honored signer set (the original artifact signer plus the registry's trusted
// deprecation identities). It returns registryindex.NoMarker() when no marker is
// attached; an unfinishedMarkerLookup when the target could not be obtained or the
// marker listing did not complete, so whether a marker is attached is unknown; the
// error of a marker that was listed and could not be read or does not verify; and
// the verified DeprecationRecord otherwise.
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
		return nil, &unfinishedMarkerLookup{err: fmt.Errorf("resolve target: %w", err)}
	}
	if target == nil {
		return nil, registryindex.NoMarker()
	}

	markers, err := oci.ListReferrers(ctx, target, subjectRef, deprecation.ArtifactType)
	if err != nil {
		return nil, &unfinishedMarkerLookup{err: fmt.Errorf("list deprecation referrers: %w", err)}
	}
	if len(markers) == 0 {
		return nil, registryindex.NoMarker()
	}

	// The most recent marker wins when more than one is attached: each marker's
	// payload is decoded and the one carrying the newest issuedOn is the
	// authoritative one, a blank or unparseable issuedOn sorting oldest. The
	// marker manifest digest is the deterministic tiebreak when two markers
	// share the same issuedOn, so the selection does not depend on the order the
	// registry lists referrers in. A marker that fails to decode is skipped, so
	// one malformed sibling does not mask a well-formed newer marker.
	marker, decoded, err := registryindex.LatestMarker(ctx, target, markers)
	if err != nil {
		return nil, err
	}

	// The marker is a signed artifact: its signature is a referrer of the
	// MARKER's own manifest digest, discovered and verified the same way an
	// artifact signature is — but against the deprecation-signer trust set.
	signer, err := registryindex.VerifyMarkerSignature(ctx, wsCfg, target, marker.Digest.String(), policy, origSigner)
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
