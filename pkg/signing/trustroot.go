// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
)

// TrustedMaterialFromRootPath loads a pinned Sigstore trust-root bundle
// from a filesystem path and returns it as a TrustedMaterial usable as a
// keyless verification anchor — fully offline, no network. This is the
// air-gapped seam: an operator distributes the public-good (or an
// enterprise) trust root as a file, pins it per-registry or via
// --trusted-root, and verification anchors in it without ever contacting
// a TUF repository.
//
// Returns a wrapped ErrTrustRootLoad when the file is missing, malformed,
// or rejected by the trust-root parser.
func TrustedMaterialFromRootPath(path string) (root.TrustedMaterial, error) {
	tr, err := root.NewTrustedRootFromPath(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrTrustRootLoad, path, err)
	}
	return tr, nil
}

// VerifyKeylessLive verifies a keyless bundle against the REAL public-good
// Sigstore trust root, fetched and cached via TUF from the embedded
// anchor. It is the only verification path that depends on live
// infrastructure: the TUF fetch of the operational trust root is a network
// operation.
//
// LIVE-ONLY: this contacts the public-good TUF repository to obtain the
// operational trust root, then verifies against it. The verification LOGIC
// is identical to VerifyKeylessBundle — the difference is solely the trust
// ANCHOR (the real fetched root rather than a pinned file or an in-memory
// test root). No offline unit test exercises this path; its correctness
// against the real Sigstore is provable only against live TUF/Fulcio/Rekor.
// Offline callers pin a trust root via TrustedMaterialFromRootPath and use
// VerifyKeylessBundle instead.
func VerifyKeylessLive(bundleBytes []byte, identities []TrustedIdentity, artifactDigest string) (KeylessIdentity, error) {
	opts := tuf.DefaultOptions()
	live, err := root.NewLiveTrustedRoot(opts)
	if err != nil {
		return KeylessIdentity{}, fmt.Errorf("%w: live TUF fetch: %v", ErrTrustRootLoad, err)
	}
	return VerifyKeylessBundle(bundleBytes, live, identities, artifactDigest)
}
