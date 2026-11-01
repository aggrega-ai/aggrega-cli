// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"context"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
)

// The sources a recorded publication date can come from. The set is closed, and
// a reader tells an attested time from a stated one by it.
const (
	// PublishedAtFromTransparencyLog is the time a transparency log recorded the
	// version's keyless signature, as the verified log entry attests it — of
	// several that verify, the earliest. For a publication signed as it was
	// published, that is its publication; for one first signed afterwards, it is
	// when that first signature was made.
	PublishedAtFromTransparencyLog = "transparency-log"
	// PublishedAtFromPublisher is the time the publication's own published-at
	// referrer states: the publishing machine's clock, which nothing attests.
	PublishedAtFromPublisher = "publisher"
)

// PublicationDate returns the publication date to record for one published
// version, as an RFC 3339 UTC timestamp, and the source it came from; both are
// empty when no source gives one.
//
// verified is the version's own signature as this host's trust policy verified
// it, or nil when none verified or none was judged. Of several that verify it is
// the earliest, as FindVerifiedSigner chooses it, so the date and the recorded
// signer always come from one signature, and a signature attached later never
// moves the date. Where it carries a log time — a keyless signature whose verified
// log entry records when it was logged — that time is the date: it is the one
// attested source, and it wins wherever it exists.
// Otherwise the version's published-at referrers are read, and the earliest time
// they state is the date. That read needs no trust anchor, since the date is the
// publisher's statement rather than a trust decision, so it runs on every host
// whatever its policy.
//
// It is best-effort, as the signer read beside it is: a referrer listing that
// cannot be completed, or a payload that does not decode, yields no date rather
// than a guessed one. subjectRef is any reference target resolves to the
// version's manifest — its tag. No entity content is read.
func PublicationDate(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string, verified *pullverify.Result) (at, source string) {
	at, source, _ = ReadPublicationDate(ctx, target, subjectRef, verified)
	return at, source
}

// ReadPublicationDate is PublicationDate for a caller that must know whether the
// answer is complete. The date and source are the same; err is non-nil where the
// published-at referrers could not all be read (oci.ReadPublishedAtFully), and the
// date returned beside it is then only the best of what was read. The index walk
// reads it so a version whose date read did not finish is never recorded with a
// date a passing failure chose: nothing of its repository is recorded from that
// walk.
func ReadPublicationDate(ctx context.Context, target oras.ReadOnlyTarget, subjectRef string, verified *pullverify.Result) (at, source string, err error) {
	if verified != nil && !verified.LoggedAt.IsZero() {
		return oci.FormatPublishedAt(verified.LoggedAt), PublishedAtFromTransparencyLog, nil
	}
	stated, found, err := oci.ReadPublishedAtFully(ctx, target, subjectRef)
	if !found {
		return "", "", err
	}
	return oci.FormatPublishedAt(stated), PublishedAtFromPublisher, err
}
