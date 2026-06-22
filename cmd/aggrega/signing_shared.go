// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sigstore/sigstore/pkg/oauthflow"
	"golang.org/x/term"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// signerParams carries the resolved signing inputs buildSigner constructs a
// signer from. The values are already resolved through the precedence chain
// (CLI flag > per-registry config > global config) by the caller; buildSigner
// applies only the public-good endpoint defaults on top. IDToken is never
// sourced from config — it is a short-lived secret supplied at runtime.
type signerParams struct {
	KeyPath      string
	IDToken      string
	FulcioURL    string
	RekorURL     string
	TimestampURL string
}

// firstNonEmpty returns the first non-empty string in order, implementing
// one precedence step: a CLI flag value wins over a config-resolved value.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveIDToken resolves the keyless OIDC identity token through its single
// precedence order — the --identity-token flag, then AGGREGA_IDENTITY_TOKEN,
// then SIGSTORE_ID_TOKEN — and returns the first non-empty value. The token is
// a short-lived secret and is never a config field, so it is resolved here at
// the signer-construction boundary rather than through the workspace-config
// overlay. Both push and sign call this so the ordering has one definition.
//
// SIGSTORE_ID_TOKEN is the sigstore-ecosystem interop alias. It is read ONLY
// here and is deliberately NOT an AGGREGA_-prefixed binding-table entry — it is
// a foreign alias the ecosystem already sets, not an Aggrega setting, so it
// must not be folded into the environment-overlay table. look reads the
// environment; pass os.Getenv in production.
func resolveIDToken(flagToken string, look func(string) string) string {
	return firstNonEmpty(flagToken, look(envIdentityToken), look(envSigstoreIDToken))
}

// isTerminal reports whether stdin is an interactive terminal. It is a
// package-level seam: tests replace it to drive the acquisition decision
// without a real TTY.
var isTerminal = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }

// acquireInteractiveIDToken acquires a short-lived OIDC identity token
// interactively through the public-good Sigstore OAuth flow. It opens a
// browser to complete the federated login and, when no browser is available,
// falls back to an out-of-band paste flow. The returned raw token is fed to
// the keyless signer; it is a runtime secret, never persisted.
func acquireInteractiveIDToken() (string, error) {
	tok, err := oauthflow.OIDConnect(signing.DefaultOIDCIssuer, signing.DefaultOIDCClientID, "", "", oauthflow.DefaultIDTokenGetter)
	if err != nil {
		return "", fmt.Errorf("acquire identity token: %w", err)
	}
	return tok.RawString, nil
}

// resolveOrAcquireIDToken returns the keyless OIDC identity token, choosing
// among three runtime paths in strict precedence: an explicit token (the
// --identity-token flag or AGGREGA_IDENTITY_TOKEN / SIGSTORE_ID_TOKEN, via the
// resolveIDToken primitive) wins; otherwise, when stdin is an interactive
// terminal, one is acquired interactively; otherwise it refuses loudly so a
// keyless sign never silently proceeds or hangs waiting on a browser in a
// non-interactive context. acquire and tty are injected seams for tests; the
// flag/env ordering is not duplicated here — it stays in resolveIDToken.
func resolveOrAcquireIDToken(flagToken string, look func(string) string, tty func() bool, acquire func() (string, error)) (string, error) {
	if t := resolveIDToken(flagToken, look); t != "" {
		return t, nil
	}
	if tty() {
		return acquire()
	}
	return "", fmt.Errorf("keyless signing needs an identity token and no interactive terminal is available; export %s or %s (for example a CI workflow OIDC token), or pass --identity-token", envIdentityToken, envSigstoreIDToken)
}

// tokenGetterOrDefault returns getter when non-nil, else the production
// interactive acquisition function. The one place the seam falls through to
// production, so both verbs default identically.
func tokenGetterOrDefault(getter func() (string, error)) func() (string, error) {
	if getter != nil {
		return getter
	}
	return acquireInteractiveIDToken
}

// ttyOrDefault returns tty when non-nil, else the production terminal check.
func ttyOrDefault(tty func() bool) func() bool {
	if tty != nil {
		return tty
	}
	return isTerminal
}

// buildSigner is the single signer-construction path for both `aggrega sign`
// and `aggrega push`. A non-empty KeyPath selects the offline keyed signer,
// reading the held PEM from disk; otherwise the live keyless signer is
// constructed. The public-good Sigstore endpoint defaults are applied here —
// the one home for endpoint defaulting — so an unset Fulcio/Rekor URL falls
// back to the community-operated instances. The RFC3161 timestamp authority is
// not defaulted: it is opt-in, set only when the caller supplies one, because
// keyless signing's trusted timestamp comes from Rekor's transparency-log
// inclusion rather than a separate timestamp authority.
func buildSigner(p signerParams) (signing.Signer, error) {
	if p.KeyPath != "" {
		pem, err := os.ReadFile(p.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %w", p.KeyPath, err)
		}
		return signing.LoadKeyedSigner(pem, nil)
	}

	fulcio := p.FulcioURL
	if fulcio == "" {
		fulcio = signing.DefaultFulcioURL
	}
	rekor := p.RekorURL
	if rekor == "" {
		rekor = signing.DefaultRekorURL
	}
	return &signing.KeylessSigner{
		IDToken:      p.IDToken,
		FulcioURL:    fulcio,
		RekorURL:     rekor,
		TimestampURL: p.TimestampURL,
	}, nil
}

// signDigest is the shared sign atom: it produces the signature bundle for a
// manifest digest. Both verbs call it — `sign` against an already-published
// digest, `push` against the digest it is about to publish — so the signature
// is computed through one path. The digest string is the only thing committed
// to; the bundle is identical whether produced before or after the bytes land.
func signDigest(ctx context.Context, signer signing.Signer, digest string) (bundle []byte, artifactType string, err error) {
	return signer.Sign(ctx, digest)
}

// attachBundle is the shared attach atom: it attaches a signature bundle as an
// OCI referrer of the artifact published under subjectRef. The artifact itself
// is untouched — the referrer is a sibling manifest pointing back at it, so
// attaching never re-pushes or mutates published bytes. Both verbs call it;
// `sign` calls it immediately after signing, `push` calls it after the push.
func attachBundle(ctx context.Context, target oras.Target, subjectRef, artifactType string, bundle []byte) error {
	if _, err := oci.AttachReferrer(ctx, target, subjectRef, artifactType, bundle); err != nil {
		return err
	}
	return nil
}
