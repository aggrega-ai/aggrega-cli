// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package signingtest provides a fake Signer for exercising the plumbing
// around signing — referrer attachment, push integration, the
// strict-refuse guardrail, command wiring — without any crypto or
// network. It is a test helper, not a production signer.
package signingtest

import (
	"context"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// FakeSigner returns a canned bundle for any digest, recording the
// digests it was asked to sign. It implements signing.Signer so callers
// can prove the wiring around a signer (the bundle reaches the referrer,
// the guardrail sees a signature, the command resolves the right digest)
// without exercising real cryptography.
type FakeSigner struct {
	// Bundle is the canned bundle bytes returned by Sign. When nil, a
	// deterministic placeholder derived from the digest is returned.
	Bundle []byte
	// ArtifactType is the referrer artifactType returned by Sign. When
	// empty, signing.BundleMediaType is returned.
	ArtifactType string
	// Err, when non-nil, is returned by Sign instead of a bundle on every
	// call. For per-call control — failing one digest while others succeed, or
	// failing the first attempt then succeeding — use FailOn.
	Err error

	// FailOn, when non-nil, is consulted per Sign call before Err: a non-nil
	// return fails that call with the returned error. The closure receives the
	// digest, so a test can fail exactly one reference, or fail the first
	// attempt of a reference and succeed on retry by counting its own calls.
	FailOn func(digest string) error

	// Signed records every digest passed to Sign, in call order.
	Signed []string
}

// Sign records the digest and returns the canned bundle (or a configured
// error: FailOn for per-call control, otherwise Err for every call).
func (f *FakeSigner) Sign(_ context.Context, artifactDigest string) ([]byte, string, error) {
	f.Signed = append(f.Signed, artifactDigest)
	if f.FailOn != nil {
		if err := f.FailOn(artifactDigest); err != nil {
			return nil, "", err
		}
	}
	if f.Err != nil {
		return nil, "", f.Err
	}
	b := f.Bundle
	if b == nil {
		b = []byte(fmt.Sprintf(`{"fake-signature-over":%q}`, artifactDigest))
	}
	at := f.ArtifactType
	if at == "" {
		at = signing.BundleMediaType
	}
	return b, at, nil
}

var _ signing.Signer = (*FakeSigner)(nil)
