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

// CarriesLogTime reports whether a signature bundle carries a transparency-log
// entry that records when the log integrated it, together with the log's
// signed promise of that time — the entry a keyless verification reads a
// signature's logged time from. A keyless bundle carries one; a keyed bundle
// carries no log entry at all, and bytes that are not a bundle carry nothing.
//
// The bundle is read, not verified. The answer is for a publisher deciding
// whether it must state its own publication date beside what it signed; a
// reader that records a date takes the log's time only from a verification
// against its own trust policy, never from this.
func CarriesLogTime(bundleBytes []byte) bool {
	if len(bundleBytes) == 0 {
		return false
	}
	b, err := LoadBundle(bundleBytes)
	if err != nil {
		return false
	}
	entries, err := b.TlogEntries()
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IntegratedTime().IsZero() && entry.HasInclusionPromise() {
			return true
		}
	}
	return false
}
