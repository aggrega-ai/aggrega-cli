// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"context"
	"fmt"
	"io"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// recordPublicationDate states, beside a publication that has just landed, when
// it landed — unless the signature attached to it already carries a time a
// transparency log attests.
//
// A manifest's creation annotation is pinned so its digest is a function of what
// is published, so the date travels as a published-at referrer of the manifest
// rather than on it: the entity's digest and tag are untouched. A keyless
// signature's log entry already records when it was logged, with the log's signed
// promise of that time, and a reader verifying the signature reads the date from
// there; a keyed signature carries no time, and an unsigned publication carries no
// signature. signatureBundle is the bundle this publication attached, or nil when
// it attached none. Whether it carries a log time is read without verifying it,
// because the question is only whether this publication must state a date of its
// own; a reader takes the log's time from its own verification.
//
// The time is now's reading in UTC, to the second — the publishing machine's own
// clock, which nothing attests, and which a reader records as the publisher's
// statement. A nil now reads the live clock. The line announcing the date is
// written to out, in the form the signature line takes.
//
// The clock is read once, before the attach, so the attach's own retry of a
// transient registry failure sends the same date on every attempt and lands one
// referrer. An attachment still failing after those attempts fails the
// publication's command, as a failed signature attachment does: the entity is
// published, and the error names what was not.
//
// It reports whether it attached a date, which is false where the signature
// carries the log's time and so no date of its own was owed.
func recordPublicationDate(ctx context.Context, out io.Writer, target oras.Target, reference, tag string, signatureBundle []byte, now func() time.Time) (bool, error) {
	if signing.CarriesLogTime(signatureBundle) {
		return false, nil
	}
	clock := now
	if clock == nil {
		clock = time.Now
	}
	at := clock()
	if _, err := oci.AttachPublishedAt(ctx, target, tag, at); err != nil {
		return false, fmt.Errorf("%s:%s is published, but its publication date could not be attached: %w", reference, tag, err)
	}
	fmt.Fprintf(out, "Publication date attached: %s:%s (%s)\n", reference, tag, oci.FormatPublishedAt(at))
	return true, nil
}
