// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/sign"
)

// KeylessSigner signs with an ephemeral key whose public half is bound to
// an OIDC identity by a short-lived Fulcio certificate, with the
// signature recorded in a Rekor transparency log and stamped by a
// timestamp authority. The private key is discarded after signing, so
// trust derives from the identity and the transparency record rather than
// from a held key.
//
// LIVE-ONLY: every Sign call contacts a live OIDC identity provider (for
// the IDToken), a live Fulcio (FulcioURL), and a live Rekor (RekorURL).
// There is no offline mode and no simulated mode — the certificate
// issuance, transparency-log inclusion, and timestamp are all network
// operations against running services. This path cannot be exercised
// without that infrastructure; correctness is provable only against live
// Fulcio/Rekor/OIDC, not in an offline unit test.
type KeylessSigner struct {
	// IDToken is the OIDC identity token presented to Fulcio for
	// certificate issuance. In a CI publish it is the workflow identity
	// token; interactively it is obtained from an OIDC flow.
	IDToken string
	// FulcioURL is the certificate authority that issues the short-lived
	// signing certificate bound to the OIDC identity.
	FulcioURL string
	// RekorURL is the transparency log that records the signature.
	RekorURL string
	// TimestampURL is an RFC3161 timestamp authority. Optional; when
	// empty no signed timestamp is requested.
	TimestampURL string
}

// Sign performs the keyless flow: generate an ephemeral key, obtain a
// Fulcio certificate for the OIDC identity, sign artifactDigest, record
// the signature in Rekor, and return a Sigstore bundle. The ephemeral
// key is discarded.
//
// LIVE-ONLY: this contacts Fulcio, Rekor, and (transitively) the OIDC
// provider. It returns ErrKeylessLive when no identity token is configured,
// and otherwise depends entirely on those live services — it has no offline
// path and no test exercises its network behaviour. The Fulcio and Rekor
// URLs are optional here: a caller that leaves them empty is expected to
// have defaulted them to the public-good Sigstore instances before
// constructing the signer.
func (k *KeylessSigner) Sign(ctx context.Context, artifactDigest string) (bundleBytes []byte, artifactType string, err error) {
	// Defensive backstop for a signer constructed directly with no token. The
	// operator-facing token resolution and the helpful refuse message live at
	// the command layer; this terse guard is a distinct internal surface.
	if k.IDToken == "" {
		return nil, "", fmt.Errorf("%w: an identity token is required", ErrKeylessLive)
	}

	ephemeral, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("signing: ephemeral key: %w", err)
	}
	keypair, err := newHeldKeypair(ephemeral)
	if err != nil {
		return nil, "", err
	}

	opts := sign.BundleOptions{
		Context:                    ctx,
		CertificateProvider:        sign.NewFulcio(&sign.FulcioOptions{BaseURL: k.FulcioURL}),
		CertificateProviderOptions: &sign.CertificateProviderOptions{IDToken: k.IDToken},
		TransparencyLogs:           []sign.Transparency{sign.NewRekor(&sign.RekorOptions{BaseURL: k.RekorURL})},
	}
	if k.TimestampURL != "" {
		opts.TimestampAuthorities = []*sign.TimestampAuthority{
			sign.NewTimestampAuthority(&sign.TimestampAuthorityOptions{URL: k.TimestampURL}),
		}
	}

	content := &sign.PlainData{Data: []byte(artifactDigest)}
	protoBundle, err := sign.Bundle(content, keypair, opts)
	if err != nil {
		return nil, "", fmt.Errorf("signing: keyless bundle: %w", err)
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
