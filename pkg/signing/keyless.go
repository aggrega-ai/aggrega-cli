// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"time"

	rekorclient "github.com/sigstore/rekor/pkg/client"
	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/sign"
	sigstoreutil "github.com/sigstore/sigstore-go/pkg/util"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// signingServiceRetries is how many times the certificate authority, the
// transparency log and the timestamp authority clients retry one request after a
// rate-limited or server-error answer. It is derived from the one retry budget
// every signing call and registry write shares (retry.MaxAttempts), so each of
// those requests is attempted no more often than any other publication step: the
// first attempt plus this many retries.
const signingServiceRetries = retry.MaxAttempts - 1

// transparencyLogFirstWait and transparencyLogLongestWait bound the transparency
// log client's waits between attempts. Its backoff waits the first bound doubled
// once per retry already made, capped at the second, so with signingServiceRetries
// retries it waits 1 s before the second attempt and 2 s before the third — the
// waits the certificate authority and timestamp authority clients take. A 429 or
// 503 carrying a Retry-After header is waited out for as long as the header asks
// instead, which the cap does not shorten.
const (
	transparencyLogFirstWait   = time.Second
	transparencyLogLongestWait = 2 * time.Second
)

// transparencyLogClient builds the client the transparency-log upload is made
// through: the signing library's own client for that log, with the retry count
// and the waits set rather than left to the library. Left to the library, the
// client gets the retry count it is handed but no wait at all, so it would re-ask a
// rate-limited log at once.
func transparencyLogClient(rekorURL string) (sign.RekorClient, error) {
	c, err := rekorclient.GetRekorClient(rekorURL,
		rekorclient.WithUserAgent(sigstoreutil.ConstructUserAgent()),
		rekorclient.WithRetryCount(signingServiceRetries),
		rekorclient.WithRetryWaitMin(transparencyLogFirstWait),
		rekorclient.WithRetryWaitMax(transparencyLogLongestWait),
	)
	if err != nil {
		return nil, err
	}
	return c.Entries, nil
}

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
// network operations against running services. A test drives Sign against
// loopback stand-ins for the certificate authority, the transparency log and the
// timestamp authority, which shows the requests it makes, the retry of a
// rate-limited answer and the bundle it assembles from the answers; whether the
// public-good services accept those requests, and whether what they return
// verifies against their trust root, is shown only against the live services.
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
	// Config is the resolved workspace configuration. Sign derives the offline
	// decision from it through the network-egress gate: an offline workspace
	// refuses with ErrOfflineEgress before any certificate, transparency-log, or
	// timestamp request, and an online workspace supplies the gate's transport to
	// the Fulcio and timestamp-authority clients. It carries no caller-asserted
	// boolean; only a resolved configuration reaches the gate.
	Config workspaceconfig.Config
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
// live services — it has no offline path. Its network behaviour is exercised
// against loopback stand-ins only (see KeylessSigner). The Fulcio and Rekor URLs
// are optional here: a caller that leaves them empty is expected to have
// defaulted them to the public-good Sigstore instances before constructing the
// signer.
//
// Each of the three services is asked through the signing library's own client,
// with signingServiceRetries set on it: a rate-limited (429) or server-error
// (5xx, but for the transparency log not 501) answer is retried by that client,
// so only the limited request is repeated and each is attempted at most three
// times. Every client waits 1 s before the second attempt and 2 s before the
// third. The certificate authority and timestamp authority clients also wait 4 s
// more after a third limited answer before giving up; the transparency-log client
// instead waits out a Retry-After header on a 429 or 503 for as long as it asks,
// and it also retries a request whose connection failed. Every wait ends when ctx
// does. A failure that survives those attempts is returned like any other.
func (k *KeylessSigner) Sign(ctx context.Context, artifactDigest string) (bundleBytes []byte, artifactType string, err error) {
	// Defensive backstop for a signer constructed directly with no source. The
	// operator-facing token resolution and the helpful refuse message live at
	// the command layer; this terse guard is a distinct internal surface.
	if k.TokenSource == nil {
		return nil, "", fmt.Errorf("%w: an identity token source is required", ErrKeylessLive())
	}
	// The keyless flow is irreducibly live — certificate issuance, transparency-log
	// inclusion, and timestamping are all network operations with no offline mode.
	// Refuse an offline workspace here, before the token fetch and any dial, so the
	// failure is the specific egress refusal rather than an opaque network error
	// deep inside a certificate or transparency-log request.
	transport, err := egress.RoundTripper(k.Config)
	if err != nil {
		return nil, "", fmt.Errorf("signing: keyless: %w", err)
	}
	idToken, err := k.TokenSource(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("signing: identity token: %w", err)
	}
	if idToken == "" {
		return nil, "", fmt.Errorf("%w: an identity token is required", ErrKeylessLive())
	}

	ephemeral, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("signing: ephemeral key: %w", err)
	}
	keypair, err := newHeldKeypair(ephemeral)
	if err != nil {
		return nil, "", err
	}

	// The certificate authority and timestamp authority take the egress gate's
	// transport for dependency injection, so their network flows through the one
	// gate-constructed transport. The transparency-log client exposes no transport
	// option in this release; its egress is governed by the offline refusal above,
	// which returns before this bundle flow runs.
	logClient, err := transparencyLogClient(k.RekorURL)
	if err != nil {
		return nil, "", fmt.Errorf("signing: transparency log client: %w", err)
	}
	opts := sign.BundleOptions{
		Context: ctx,
		CertificateProvider: sign.NewFulcio(&sign.FulcioOptions{
			BaseURL: k.FulcioURL, Transport: transport, Retries: signingServiceRetries,
		}),
		CertificateProviderOptions: &sign.CertificateProviderOptions{IDToken: idToken},
		// RekorOptions.Retries is left unset: the library reads it only when it
		// builds the client itself, and the client handed in here carries the
		// retry count and the waits.
		TransparencyLogs: []sign.Transparency{sign.NewRekor(&sign.RekorOptions{
			BaseURL: k.RekorURL, Client: logClient,
		})},
	}
	if k.TimestampURL != "" {
		opts.TimestampAuthorities = []*sign.TimestampAuthority{
			sign.NewTimestampAuthority(&sign.TimestampAuthorityOptions{
				URL: k.TimestampURL, Transport: transport, Retries: signingServiceRetries,
			}),
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
