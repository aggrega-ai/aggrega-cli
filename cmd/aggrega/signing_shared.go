// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"

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

// buildSigner is the single signer-construction path for both `aggrega sign`
// and `aggrega push`. A non-empty KeyPath selects the offline keyed signer,
// reading the held PEM from disk; otherwise the live keyless signer is
// constructed. The public-good Sigstore endpoint defaults are applied here —
// the one home for endpoint defaulting — so an unset Fulcio/Rekor/timestamp
// URL falls back to the community-operated instances.
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
	timestamp := p.TimestampURL
	if timestamp == "" {
		timestamp = signing.DefaultTimestampURL
	}
	return &signing.KeylessSigner{
		IDToken:      p.IDToken,
		FulcioURL:    fulcio,
		RekorURL:     rekor,
		TimestampURL: timestamp,
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
