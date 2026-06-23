// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

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
	// Local pre-sign verification check: the same subject the certificate authority
	// derives from the token's claims must be present before the token is
	// handed off, so a token that structurally lacks a usable subject fails
	// fast here with a precise diagnostic instead of as an opaque downstream
	// "no subject found in claims". This decodes the token's claims without
	// verifying its signature — it is a verification check, not a trust decision;
	// the certificate authority still performs the real verification.
	if err := checkIDTokenSubject(p.IDToken); err != nil {
		return nil, err
	}
	return &signing.KeylessSigner{
		IDToken:      p.IDToken,
		FulcioURL:    fulcio,
		RekorURL:     rekor,
		TimestampURL: p.TimestampURL,
	}, nil
}

// keylessSubjectFailure reports whether err is a keyless cert-issuance failure
// caused by the identity token lacking a usable subject — either no verified
// email and no subject claim at all, or an email present but not marked
// verified by its identity provider. The certificate authority surfaces these
// as plain string-wrapped errors with no typed signal, so the only available
// classification is a defensive substring match on the wrapped text. A true
// result means re-acquiring or re-exporting the token will not help: the
// problem is the identity the OIDC flow returned, not the token's freshness.
func keylessSubjectFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "no subject found in claims") ||
		strings.Contains(msg, "not verified by identity provider")
}

// tokenSubjectClaims is the minimal claims subset that decides whether an OIDC
// identity token carries a usable signing subject. It mirrors exactly the
// claims the certificate authority consults — email, email_verified, sub — and
// nothing more, so the local check neither under- nor over-claims relative to
// the authority's own subject derivation.
type tokenSubjectClaims struct {
	Email    string       `json:"email"`
	Verified tolerantBool `json:"email_verified"`
	Subject  string       `json:"sub"`
}

// tolerantBool decodes a JSON boolean that some identity providers emit as a
// string rather than a bare literal. It accepts the bare and quoted forms of
// true/false in either case, matching the certificate authority's own lenient
// decoding so a provider that stringifies email_verified is not falsely read
// as unverified. Any other value decodes to false rather than erroring, so a
// malformed claim degrades to "not verified" rather than failing the decode.
type tolerantBool bool

func (b *tolerantBool) UnmarshalJSON(data []byte) error {
	switch string(data) {
	case "true", `"true"`, "True", `"True"`:
		*b = true
	default:
		*b = false
	}
	return nil
}

// checkIDTokenSubject decodes the identity token's claims payload and verifies
// a usable signing subject is present, mirroring the certificate authority's
// subject derivation: a verified email is a subject; otherwise a non-empty sub
// claim is a subject; an email present but unverified, or neither claim, has no
// subject. The token's signature is NOT verified — this decodes the unverified
// payload as a local verification check so a structurally subject-less token fails at
// the command layer with a precise message rather than as an opaque downstream
// error. It returns nil when a subject is present.
func checkIDTokenSubject(rawToken string) error {
	parts := strings.Split(rawToken, ".")
	if len(parts) < 2 {
		return fmt.Errorf("the identity token is not a well-formed JWT (expected header.payload.signature) — it cannot be used for keyless signing")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("the identity token's payload is not valid base64url and cannot be decoded — it is not a usable JWT for keyless signing: %w", err)
	}
	var c tokenSubjectClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return fmt.Errorf("the identity token's payload is not valid JSON and cannot be read for a signing subject: %w", err)
	}
	if c.Email != "" {
		if !c.Verified {
			return fmt.Errorf("the identity token carries an email that the OIDC identity provider did not mark verified — keyless signing needs a verified email; configure the OIDC flow/client to return a verified email-scoped identity")
		}
		return nil
	}
	if c.Subject == "" {
		return fmt.Errorf("the identity token carries no verified email and no sub claim — the OIDC flow/client did not return an email-scoped identity (re-exporting or re-acquiring the token will not help); configure the identity provider/client/scope to return one")
	}
	return nil
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
