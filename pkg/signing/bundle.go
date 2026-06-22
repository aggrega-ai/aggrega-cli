// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"context"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/sign"
)

// Sign produces a Sigstore bundle committing to artifactDigest, signed
// with the held key. The bundle carries a message signature over the
// digest and the verification public key — no certificate, transparency
// log, or timestamp, because the keyed path is fully offline. The
// returned artifactType is BundleMediaType.
func (k *KeyedSigner) Sign(ctx context.Context, artifactDigest string) (bundleBytes []byte, artifactType string, err error) {
	keypair, err := newHeldKeypair(k.signer)
	if err != nil {
		return nil, "", err
	}
	content := &sign.PlainData{Data: []byte(artifactDigest)}

	protoBundle, err := sign.Bundle(content, keypair, sign.BundleOptions{Context: ctx})
	if err != nil {
		return nil, "", fmt.Errorf("signing: build bundle: %w", err)
	}

	b, err := bundle.NewBundle(protoBundle)
	if err != nil {
		return nil, "", fmt.Errorf("signing: wrap bundle: %w", err)
	}
	out, err := b.MarshalJSON()
	if err != nil {
		return nil, "", fmt.Errorf("signing: marshal bundle: %w", err)
	}
	return out, BundleMediaType, nil
}

// LoadBundle parses Sigstore bundle bytes (as attached to an OCI
// referrer) back into a bundle for inspection or verification.
func LoadBundle(bundleBytes []byte) (*bundle.Bundle, error) {
	b := &bundle.Bundle{}
	if err := b.UnmarshalJSON(bundleBytes); err != nil {
		return nil, fmt.Errorf("signing: parse bundle: %w", err)
	}
	return b, nil
}
