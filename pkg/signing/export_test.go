// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"crypto"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/sign"
)

// The seams this package's external tests reach, compiled into its tests only.
// Each is a part the product functions compose, exposed on its own so a test can
// hold one part to another: the parity between the toolchain's reading of which
// artifact a signature was made for and the verification library's own binding
// is only observable against the library alone, because the product verifiers
// already refuse a bundle where the two disagree.

// VerifyKeyedWithLibrary is VerifyKeyedBundle's verification by the library
// alone, without the toolchain's own reading asked anything.
func VerifyKeyedWithLibrary(bundleBytes, pubPEM []byte, artifactDigest string) error {
	return verifyKeyedWithLibrary(bundleBytes, pubPEM, artifactDigest)
}

// VerifyKeylessWithLibrary is VerifyKeylessBundle's verification by the library
// alone, without the toolchain's own reading asked anything.
func VerifyKeylessWithLibrary(bundleBytes []byte, trustedMaterial root.TrustedMaterial, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	return verifyKeylessWithLibrary(bundleBytes, trustedMaterial, identities, artifactDigest)
}

// HoldToTheArtifactVerified is the check both verifiers run once the library
// has verified a bundle, on its own.
func HoldToTheArtifactVerified(bundleBytes []byte, artifactDigest string) error {
	return holdToTheArtifactVerified(bundleBytes, artifactDigest)
}

// NewHeldKeypair is the keypair adapter both of the toolchain's signers build a
// bundle with.
func NewHeldKeypair(signer crypto.Signer) (sign.Keypair, error) {
	keypair, err := newHeldKeypair(signer)
	if err != nil {
		return nil, err
	}
	return keypair, nil
}
