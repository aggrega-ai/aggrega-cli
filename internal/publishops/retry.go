// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// fulcioTokenTransientMarker is the substring the keyless certificate
// authority emits when it transiently fails to process an identity token. The
// authority surfaces this as a plain string-wrapped 400 with no typed signal,
// so the only available classification is a defensive substring match — the
// same approach the subject-failure classification uses. A match means a fresh
// token acquisition on the next attempt can succeed, so the step is retryable.
const fulcioTokenTransientMarker = "There was an error processing the identity token"

// signBundle packs the two success values of a signing call — the bundle bytes
// and the referrer artifactType — into one value so the signing retry can flow
// through the single-value generic retry loop.
type signBundle struct {
	bytes        []byte
	artifactType string
}

// stopped reports that the caller no longer wants the operation, as an error
// that errors.Is matches against context.Canceled and context.DeadlineExceeded.
// It returns nil while the caller is still waiting, which is every ordinary run.
//
// It is one function rather than an inline check written at each interruption
// point so the sentence a stop produces is stated once in this package. The
// cause is wrapped rather than replaced because it is the part a caller acts on.
//
// Every interruption point in this package sits AHEAD of the first byte that
// reaches a registry, which is what the sentence says: publication is not
// reversible, so a withdrawal observed afterwards would withhold the reply while
// the bytes were already published.
func stopped(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("aggrega publish: stopped before anything was published: %w", err)
	}
	return nil
}

// IsRetryablePublishError classifies a signing failure as transient (worth
// another attempt) or terminal (fail fast). The signing retry in signDigest
// consults it.
//
// It is oci.IsTransientRegistryError — the one answer to which registry and
// network failures are transient, which every registry write in pkg/oci retries
// on — plus the keyless authority's transient token-processing failure. So the
// signing retry and the registry-write retry never disagree about a registry or
// network error, and the only failure this adds is one the authority, not a
// registry, reports.
//
// A rate-limited or server-error answer from the certificate authority, the
// transparency log or the timestamp authority is not classified here. The keyless
// signer's own client for each service retries that request inside the signing
// call, so such an answer arrives here only after those attempts are spent, as a
// failure this reports terminal. The transparency log's client also retries an
// upload whose connection failed; a network failure it met on every attempt
// arrives here wrapped, and is classified as any network failure is.
//
// Terminal (returns false): a nil error; a caller cancellation; and every error
// oci.IsTransientRegistryError reports terminal that is not the authority's
// token-processing failure.
func IsRetryablePublishError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if oci.IsTransientRegistryError(err) {
		return true
	}
	return strings.Contains(err.Error(), fulcioTokenTransientMarker)
}
