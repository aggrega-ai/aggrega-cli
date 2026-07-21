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
// the identity token), a live Fulcio (FulcioURL), and a live Rekor
// (RekorURL). There is no offline mode and no simulated mode — the
// certificate issuance, transparency-log inclusion, and timestamp are all
// network operations against running services. This path cannot be exercised
// without that infrastructure; correctness is provable only against live
// Fulcio/Rekor/OIDC, not in an offline unit test.
type KeylessSigner struct {
	// TokenSource yields the OIDC identity token presented to Fulcio for
	// certificate issuance. It is consulted once per Sign, so a source
	// backed by a live provider supplies a fresh token for each signature —
	// a long publish job never presents a token that expired since it began.
	// In a CI publish it draws on the workflow identity; interactively it is
	// a token obtained once from an OIDC flow.
	TokenSource IdentityTokenSource
	// FulcioURL is the certificate authority that issues the short-lived
	// signing certificate bound to the OIDC identity.
	FulcioURL string
	// RekorURL is the transparency log that records the signature.
	RekorURL string
	// TimestampURL is an RFC3161 timestamp authority. Optional; when
	// empty no signed timestamp is requested.
	TimestampURL string
}

// Sign performs the keyless flow: fetch a fresh OIDC identity token from the
// token source, generate an ephemeral key, obtain a Fulcio certificate for
// that identity, sign artifactDigest, record the signature in Rekor, and
// return a Sigstore bundle. The token is fetched anew on every call, so a
// long-running signing job cannot present a token that expired since it
// started. The ephemeral key is discarded.
//
// LIVE-ONLY: this contacts Fulcio, Rekor, and (transitively) the OIDC
// provider. It returns ErrKeylessLive when no token source is configured or
// the source yields an empty token, and otherwise depends entirely on those
// live services — it has no offline path and no test exercises its network
// behaviour. The Fulcio and Rekor URLs are optional here: a caller that leaves
// them empty is expected to have defaulted them to the public-good Sigstore
// instances before constructing the signer.
func (k *KeylessSigner) Sign(ctx context.Context, artifactDigest string) (bundleBytes []byte, artifactType string, err error) {
	// Defensive backstop for a signer constructed directly with no source. The
	// operator-facing token resolution and the helpful refuse message live at
	// the command layer; this terse guard is a distinct internal surface.
	if k.TokenSource == nil {
		return nil, "", fmt.Errorf("%w: an identity token source is required", ErrKeylessLive)
	}
	idToken, err := k.TokenSource(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("signing: identity token: %w", err)
	}
	if idToken == "" {
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
		CertificateProviderOptions: &sign.CertificateProviderOptions{IDToken: idToken},
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
