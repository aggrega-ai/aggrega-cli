// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"context"
	"io"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// This file gathers the parts of the credential and signing boundary that a
// caller outside this package legitimately reaches for. They are collected in one
// place so the layer's outward surface is readable at a glance rather than spread
// through the files that implement it.
//
// The interactive rung is deliberately NOT among them. It reaches this layer only
// as SignerRequest.AcquireIdentityToken, which every caller with nobody watching
// leaves nil, and the acquisition itself lives in the command line where it cannot
// be imported.

// ComponentCategoryLabel is the metadata.labels key whose value the standard
// catalog convention sets to an entity's category. Its value is stamped as the
// searchable category annotation at publication, so a registry index can filter
// by category without fetching the entity body — which is why the key is named
// here, on the side that publishes it, rather than restated by each side that
// reads the annotation back.
const ComponentCategoryLabel = componentCategoryLabel

// ResolveDestination returns the destination registry a publication will use and
// refuses when the caller requested none and the workspace names no default,
// naming what supply says this caller can set.
//
// A surface that wants to answer that refusal in its own error channel — an
// invalid-argument response rather than an operation failure — calls this at its
// own boundary and discards the registry. What it must not do is re-test the two
// values itself: the condition has one home, and this is it.
func ResolveDestination(requested, defaultRegistry string, supply DestinationSupply) (string, error) {
	return resolveDestination(requested, defaultRegistry, supply)
}

// CheckIDTokenSubject verifies an identity token carries a usable signing
// subject, mirroring the certificate authority's own subject derivation. It
// decodes the unverified payload as a local verification check, so a
// structurally subject-less token fails with a precise message rather than as an
// opaque downstream error.
func CheckIDTokenSubject(rawToken string) error {
	return checkIDTokenSubject(rawToken)
}

// KeylessSubjectFailure reports whether err is a keyless cert-issuance failure
// caused by the identity token lacking a usable subject. A true result means
// re-acquiring or re-exporting the token will not help.
func KeylessSubjectFailure(err error) bool {
	return keylessSubjectFailure(err)
}

// SignDigest is the shared sign atom: it produces the signature bundle for a
// manifest digest, so every verb computes a signature through one path. A
// transient signing failure (IsRetryablePublishError) is retried with the
// shared budget before its error is returned.
func SignDigest(ctx context.Context, signer signing.Signer, digest string) (bundle []byte, artifactType string, err error) {
	return signDigest(ctx, signer, digest)
}

// AttachBundle is the shared attach atom: it attaches a signature bundle as an
// OCI referrer of the artifact published under subjectRef. The artifact itself
// is untouched — the referrer is a sibling manifest pointing back at it.
func AttachBundle(ctx context.Context, target oras.Target, subjectRef, artifactType string, bundle []byte) error {
	return attachBundle(ctx, target, subjectRef, artifactType, bundle)
}

// DeprecateSigner resolves the signer a single deprecation marking would sign
// its marker with — through the one credential resolution every signing verb
// shares — without attaching anything. It reaches no registry: a caller can read
// which credential a marking would spend before it spends one.
func DeprecateSigner(cfg DeprecateConfig) (signing.Signer, error) {
	return resolveSigner(cfg.signerRequest(), cfg.Reference, nil)
}

// PushPlugin publishes a Plugin entity and its .wasm executable as one OCI
// manifest. Push dispatches to it on the Plugin kind; it is reachable directly
// for a caller that already knows the kind and has the entity bytes in hand.
// registry is the location published under — a registry host, optionally
// followed by a repository path — and its host is what the signing decisions
// are taken against.
func PushPlugin(ctx context.Context, out io.Writer, cfg PushConfig, registry string, wantSign bool, name, version, entityPath string, entityYAML []byte) (PushResult, error) {
	return pushPlugin(ctx, out, cfg, registryhost.LocationOf(registry), wantSign, name, version, entityPath, entityYAML)
}
