// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// completion is what a publication that finds its content already published
// needs in order to attach what that publication still lacks.
type completion struct {
	target    oras.Target
	reference string
	tag       string
	// digest is the manifest digest the tag already resolves to, which is what a
	// signature commits to.
	digest string
	// sign makes this publication's signature over a digest. It is nil where this
	// publication does not sign, and is called only when a signature is owed.
	sign func(ctx context.Context, digest string) (bundle []byte, artifactType string, err error)
	// wsCfg and policy are the workspace configuration and the destination
	// registry's trust policy a signature already attached is judged against. Only
	// a publication that signs reads them.
	wsCfg  workspaceconfig.Config
	policy workspaceconfig.RegistryConfig
	// now is the clock an owed date is read from; nil reads the live clock.
	now func() time.Time
}

// completed is what a completion attached: a signature, a publication date, both,
// or neither where the publication was already complete.
type completed struct {
	signed bool
	dated  bool
}

// completePublication attaches, to a version whose content is already published,
// what that publication still lacks and nothing else, and reports what it
// attached. A publication that failed after its content landed — its
// signature or its date could not be attached — is therefore completed by running
// it again, and a publication that is already complete gets nothing attached.
//
// The signature is owed only where this publication signs, and only when the
// version carries no signature that counts as its own. Where the destination's
// trust policy pins an anchor — a public key, or keyless identities — a signature
// counts when it verifies under that policy, asked through the one walk that
// answers it (registryindex.FindVerifiedSigner). Where the policy pins neither,
// no signature could verify, so verification cannot tell an attached signature
// from a missing one: any signature referrer the version carries counts, and a
// signature is owed only by a version that carries none. Judging by verification
// there would read every signature as missing and attach another on every run.
//
// The date is owed by the rule recordPublicationDate applies to a first
// publication: unless the signature that dates the version carries a time a
// transparency log attests. That signature is the one this call attaches, or else
// the one that already counts — under a policy pinning no anchor, any present
// signature carrying such a time. A publication that does not sign is dated as an
// unsigned one is, whatever signatures the version carries. An owed date is
// attached only when the version carries no published-at referrer at all; its
// presence is read from the referrer listing rather than from the dates the
// referrers state, so a referrer whose payload cannot be fetched at that moment
// still counts and a run never states a second date. The date states the clock of
// this run, read as recordPublicationDate reads it: the time the publication was
// completed, since nothing the registry serves records when the content landed.
//
// Every read comes before every write. A listing or a fetch that cannot be
// completed is returned as an error with nothing attached, and is never read as
// "none"; and the signature is made before anything is attached, so a signing
// failure attaches nothing either. An attach that still fails after the write
// layer's retries fails the call naming what was not attached, as it fails a first
// publication, and each attachment is announced in the line a first publication
// writes for it.
func completePublication(ctx context.Context, out io.Writer, c completion) (completed, error) {
	var (
		signNow      bool
		bundle       []byte
		artifactType string
		datedBy      []byte
	)
	if c.sign != nil {
		counted, owed, err := countedSignature(ctx, c)
		if err != nil {
			return completed{}, err
		}
		if owed {
			bundle, artifactType, err = c.sign(ctx, c.digest)
			if err != nil {
				return completed{}, fmt.Errorf("%s:%s is already published, but the signature it lacks could not be made: %w",
					c.reference, c.tag, err)
			}
			signNow, datedBy = true, bundle
		} else {
			datedBy = counted
		}
	}

	dateOwed := !signing.CarriesLogTime(datedBy)
	if dateOwed {
		dates, err := oci.ListReferrers(ctx, c.target, c.tag, oci.MediaTypePublishedAt)
		if err != nil {
			return completed{}, fmt.Errorf("%s:%s is already published, but whether it states a publication date could not be read: %w",
				c.reference, c.tag, err)
		}
		dateOwed = len(dates) == 0
	}

	var done completed
	if signNow {
		if err := attachBundle(ctx, c.target, c.tag, artifactType, bundle); err != nil {
			return completed{}, fmt.Errorf("attach signature to %s:%s: %w", c.reference, c.tag, err)
		}
		fmt.Fprintf(out, "Signature attached: %s:%s\n", c.reference, c.tag)
		done.signed = true
	}
	if dateOwed {
		dated, err := recordPublicationDate(ctx, out, c.target, c.reference, c.tag, datedBy, c.now)
		if err != nil {
			return done, err
		}
		done.dated = dated
	}
	return done, nil
}

// countedSignature returns the signature the version already carries that counts
// as this publication's, for the date rule to read, and whether a signature is
// owed because none counts. completePublication states which signature counts
// under which policy.
func countedSignature(ctx context.Context, c completion) (counted []byte, owed bool, err error) {
	if res, judged, ferr := verifyingSignature(ctx, c.wsCfg, c.target, c.tag, c.digest, c.policy); judged {
		if ferr != nil {
			return nil, false, fmt.Errorf("%s:%s is already published, but whether it carries a signature that verifies could not be read: %w",
				c.reference, c.tag, ferr)
		}
		if res == nil {
			return nil, true, nil
		}
		return res.Bundle, false, nil
	}

	refs, lerr := oci.ListReferrers(ctx, c.target, c.tag, signing.BundleMediaType)
	if lerr != nil {
		return nil, false, fmt.Errorf("%s:%s is already published, but whether it carries a signature could not be read: %w",
			c.reference, c.tag, lerr)
	}
	if len(refs) == 0 {
		return nil, true, nil
	}
	for _, ref := range refs {
		payload, ferr := oci.FetchReferrerPayload(ctx, c.target, ref)
		if ferr != nil {
			return nil, false, fmt.Errorf("%s:%s is already published, but a signature it carries could not be read: %w",
				c.reference, c.tag, ferr)
		}
		if signing.CarriesLogTime(payload) {
			return payload, false, nil
		}
	}
	return nil, false, nil
}

// verifyingSignature asks whether the version tag resolves to, at digest, already
// carries a signature its destination's trust policy verifies, and returns the
// earliest that does, through the one signature walk
// (registryindex.FindVerifiedSigner). It is the question a publication completing
// itself and a signing batch both ask before they sign, answered in one place so
// the two cannot come to count different signatures.
//
// judged is false where the policy pins no trust anchor — no public key and no
// keyless identities — because no signature could verify against it, so the
// question has no answer there and each caller decides what an unjudged version
// gets. Where judged is true, the walk's own contract holds: a nil result with a
// nil error means every signature was read and none verifies, and an error means
// the answer is not known and must not be read as "none".
func verifyingSignature(ctx context.Context, wsCfg workspaceconfig.Config, target oras.ReadOnlyTarget, tag, digest string, policy workspaceconfig.RegistryConfig) (res *pullverify.Result, judged bool, err error) {
	if registryops.TrustOf(policy).Anchor == "" {
		return nil, false, nil
	}
	res, err = registryindex.FindVerifiedSigner(ctx, wsCfg, target, tag, digest, policy)
	return res, true, err
}
