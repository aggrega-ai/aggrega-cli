// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// TestSignPushDigestRetriesTransientThenSucceeds proves the push-time sign
// atom retries a transient certificate-authority failure and succeeds on a
// later attempt, so a single flaky token mint does not lose the publish.
func TestSignPushDigestRetriesTransientThenSucceeds(t *testing.T) {
	// Not parallel: mutates the process-global publishRetryBaseDelay seam other tests read.
	shrinkRetryDelay(t)
	attempts := 0
	fake := &signingtest.FakeSigner{
		FailOn: func(string) error {
			attempts++
			if attempts == 1 {
				return errors.New(`signing: keyless bundle: Fulcio returned 400: {"code":3,"message":"There was an error processing the identity token"}`)
			}
			return nil
		},
	}
	bundle, artifactType, err := signPushDigestWith(context.Background(), fake, "example.com/component/core/api-server", "1.0.0", "sha256:abc")
	if err != nil {
		t.Fatalf("signPushDigestWith: unexpected error %v", err)
	}
	if len(bundle) == 0 {
		t.Error("bundle is empty after a successful retry")
	}
	if artifactType != signing.BundleMediaType {
		t.Errorf("artifactType = %q, want %q", artifactType, signing.BundleMediaType)
	}
	if attempts != 2 {
		t.Errorf("signer called %d times, want 2", attempts)
	}
}

// TestSignPushDigestTerminalNotRetriedStillRefused proves a terminal signing
// failure is not retried and is still wrapped with ErrUnsignedPushRefused so
// the push is refused with nothing published.
func TestSignPushDigestTerminalNotRetriedStillRefused(t *testing.T) {
	// Not parallel: mutates the process-global publishRetryBaseDelay seam other tests read.
	shrinkRetryDelay(t)
	fake := &signingtest.FakeSigner{Err: errors.New("permanent signing failure")}
	_, _, err := signPushDigestWith(context.Background(), fake, "example.com/component/core/api-server", "1.0.0", "sha256:abc")
	if !errors.Is(err, signing.ErrUnsignedPushRefused()) {
		t.Fatalf("error = %v, want wrapped ErrUnsignedPushRefused", err)
	}
	if len(fake.Signed) != 1 {
		t.Errorf("signer called %d times, want 1 (terminal error not retried)", len(fake.Signed))
	}
}
