// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// SignConfig is the input contract for the signing workflow. Sign reads only
// from cfg, so concurrent callers cannot observe each other.
type SignConfig struct {
	References []string

	// Keyed path.
	KeyPath string

	// Keyless path.
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	IDToken      string

	// Target overrides the production remote-repository construction. A caller
	// may inject one in-memory oras.Target shared across all references. When
	// nil, Sign constructs a target per reference from that reference.
	Target oras.Target

	// SubjectRefs maps a reference to the subject resolved against Target. A
	// reference absent from the map (or a nil map) derives its subject from its
	// own tag/digest.
	SubjectRefs map[string]string

	// Signer overrides signer construction. When nil, Sign builds a keyed or
	// keyless signer from the resolved signing inputs.
	Signer signing.Signer

	// Workspace is the resolved workspace the caller threads in. Required: Sign
	// refuses a nil rather than resolving one of its own.
	Workspace *workspaceconfig.ResolvedWorkspace

	// EnvLookup reads the environment for the identity-token secret resolution.
	// Nil means the live process environment.
	EnvLookup func(string) string

	// TokenGetter acquires a keyless OIDC identity token interactively when no
	// explicit token is supplied. Nil is the unattended shape and is the
	// default: no acquisition is attempted and the shared identity ladder
	// refuses, naming what to supply.
	TokenGetter func() (string, error)

	// IsTTY reports whether a person is watching, gating the interactive rung.
	// Nil reports false, so the unattended default cannot reach a prompt.
	IsTTY func() bool

	// IdentitySupply carries the caller's own vocabulary into the identity
	// ladder's refusal, so a caller is told to supply something it can supply.
	// Zero yields a refusal naming only the inputs every caller shares.
	IdentitySupply signing.IdentityTokenSupply

	// Out is the sink for the per-reference status line. Nil defaults to
	// os.Stdout, so a caller that wants to capture the output passes a buffer
	// rather than mutating a process global.
	Out io.Writer
}

// signerRequest projects the batch's credential inputs onto the shared
// credential boundary's input contract for one registry host.
func (cfg SignConfig) signerRequest(registry string) SignerRequest {
	return SignerRequest{
		Workspace:            cfg.Workspace,
		Registry:             registry,
		Signer:               cfg.Signer,
		KeyPath:              cfg.KeyPath,
		FulcioURL:            cfg.FulcioURL,
		RekorURL:             cfg.RekorURL,
		TimestampURL:         cfg.TimestampURL,
		IDToken:              cfg.IDToken,
		EnvLookup:            cfg.EnvLookup,
		AcquireIdentityToken: interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:       cfg.IdentitySupply,
	}
}

// SignResult reports which references the batch signed, in the order they were
// given, as the facts themselves rather than as the status lines Sign also writes
// to cfg.Out.
//
// It reports the SUCCESSES only. A batch with any failure returns an error naming
// every one of them, so a caller never reads a partial batch as a whole one — and
// a result that carried failures alongside an absent error would be exactly that
// reading. The successes of a failed batch reach a caller through the status
// lines, as they always have.
type SignResult struct {
	Signed []SignedArtifact
}

// SignedArtifact is one reference the batch signed, with the published digest the
// signature commits to. The digest is what makes the answer checkable: a signature
// commits to a digest string, so naming the reference alone would not say what was
// signed.
type SignedArtifact struct {
	Reference string
	Digest    string
}

// Sign signs every reference in cfg.References, attaching each signature
// bundle as an OCI referrer of its already-published artifact. The published
// artifacts are never re-published or mutated; each referrer is a sibling
// manifest, so signing is immutability-safe.
//
// One authentication serves the whole batch: on the keyless path the identity
// is resolved once before the loop — a static token, or a live source that
// mints a fresh token per signature — and reused for every reference, while a
// fresh short-lived certificate is issued per artifact by the per-reference
// signer. The keyed path resolves no identity.
//
// Signing is continue-and-report: a reference that fails to resolve, sign, or
// attach is recorded and the batch proceeds to the next reference; after the
// loop, if any reference failed, a summary naming each failure is returned.
func Sign(cfg SignConfig) (SignResult, error) {
	if len(cfg.References) == 0 {
		return SignResult{}, fmt.Errorf("at least one reference required")
	}
	ctx := context.Background()
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	// Resolve the batch's signing posture once. The identity token is
	// registry-independent — it identifies the signer, not the destination —
	// so the keyless branch acquires it a single time here and threads it to
	// every per-reference signer. The identity resolution runs even when a Signer
	// is supplied so the acquire-once accounting holds for both paths.
	b := &batchSigner{cfg: cfg, out: out}
	if err := b.prepare(); err != nil {
		return SignResult{}, err
	}

	var (
		failures []refFailure
		signed   []SignedArtifact
	)
	for _, ref := range cfg.References {
		digest, err := b.signRef(ctx, ref)
		if err != nil {
			failures = append(failures, refFailure{ref: ref, err: err})
			continue
		}
		signed = append(signed, SignedArtifact{Reference: ref, Digest: digest})
	}

	if len(failures) > 0 {
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d of %d references failed:", len(failures), len(cfg.References))
		for _, f := range failures {
			fmt.Fprintf(&sb, "\n  %s: %v", f.ref, f.err)
		}
		return SignResult{}, errors.New(sb.String())
	}
	return SignResult{Signed: signed}, nil
}

// refFailure pairs a reference with the error that stopped it, so the batch can
// continue past it and report every failure together at the end.
type refFailure struct {
	ref string
	err error
}

// batchSigner carries the once-resolved signing posture across the batch loop:
// the identity resolved once as either a static token or a live source, reused
// for every reference's signer.
type batchSigner struct {
	cfg SignConfig

	// keyless reports whether the batch uses the keyless flow. The keyed path
	// never resolves an identity.
	keyless bool
	// idToken is the single static identity token, reused for every reference's
	// signer. Empty when the identity resolved to a live source instead.
	idToken string
	// tokenSource is the live identity source (the ambient GitHub Actions OIDC
	// endpoint) when the identity resolved to one, reused for every reference's
	// signer so the batch shares a single source. Nil on the static path.
	tokenSource signing.IdentityTokenSource

	// keyPath holds the workspace-config signing overlay for the batch. The
	// per-reference signer applies it together with the caller's inputs.
	keyPath string

	// out is the resolved sink for the per-reference status line, set once by
	// Sign.
	out io.Writer
}

// prepare resolves the batch's signing posture once: it decides keyless vs
// keyed and, on the keyless branch, resolves the identity a single time through
// the shared precedence — as a static token (a supplied value, the environment,
// or one interactive acquisition) or as a live source (the ambient GitHub Actions
// OIDC endpoint) — and stores it for reuse across every reference. When cfg.Signer
// is supplied the per-reference signer is that one, but the keyless
// resolution still runs so the interactive token getter is invoked at most once
// for the whole batch — the property the batch contract guarantees.
func (b *batchSigner) prepare() error {
	if b.cfg.Signer == nil {
		rw, err := requireWorkspace(b.cfg.Workspace)
		if err != nil {
			return err
		}
		// The key path is registry-independent in the common case (a single
		// supplied path or global config entry). Resolve it once from the first
		// reference's host to decide keyless vs keyed for the whole batch; a
		// per-registry key path is still applied per reference in signRef.
		resolved := rw.Config().ResolveSigning(registryhost.Of(b.cfg.References[0]))
		b.keyPath = firstNonEmpty(b.cfg.KeyPath, resolved.KeyPath)
		b.keyless = b.keyPath == ""
		// Carry the resolved workspace forward so signRef sources the egress
		// client from the same resolution.
		b.cfg.Workspace = rw
	} else {
		// A supplied signer stands in for whatever the inputs would build. The
		// keyless acquire-once block still runs unless a key path is supplied,
		// so the acquire-count assertion holds for that path too.
		b.keyless = b.cfg.KeyPath == ""
	}

	if b.keyless {
		rw, werr := requireWorkspace(b.cfg.Workspace)
		if werr != nil {
			return werr
		}
		idToken, source, err := resolveIdentity(b.cfg.signerRequest(""), rw.Config())
		if err != nil {
			return err
		}
		b.idToken = idToken
		b.tokenSource = source
	}
	return nil
}

// signRef signs one reference and attaches its signature, returning the published
// digest the signature commits to. It builds the per-reference signer from the
// batch's once-resolved identity (keyless) or the resolved key path (keyed) —
// never resolving an identity itself; the single resolution lives in prepare. A
// keyless failure whose cause is a token without a usable signing subject is
// mapped to a precise diagnostic; every other failure is returned as-is so the
// batch continues and reports it.
func (b *batchSigner) signRef(ctx context.Context, ref string) (string, error) {
	target := b.cfg.Target
	if target == nil {
		rw, werr := requireWorkspace(b.cfg.Workspace)
		if werr != nil {
			return "", fmt.Errorf("%s: %w", ref, werr)
		}
		httpClient, err := egressClient(rw.Config())
		if err != nil {
			return "", fmt.Errorf("%s: %w", ref, err)
		}
		repo, err := oci.NewRemoteRepository(ref, httpClient)
		if err != nil {
			return "", fmt.Errorf("%s: %w", ref, err)
		}
		target = repo
	}

	subjectRef := b.cfg.SubjectRefs[ref]
	if subjectRef == "" {
		subjectRef = referenceTag(ref)
	}

	// Resolve the published digest. signRef never re-publishes content — it signs
	// whatever digest the registry already holds for the reference.
	digest, exists, err := oci.Resolve(ctx, target, subjectRef)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", ref, err)
	}
	if !exists {
		return "", fmt.Errorf("%s is not published; nothing to sign", ref)
	}

	signer, err := b.buildRefSigner(ref)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ref, err)
	}

	bundleBytes, artifactType, err := signDigest(ctx, signer, digest)
	if err != nil {
		// A token that structurally lacks a usable signing subject cannot be
		// fixed by re-exporting or re-acquiring it: the identity the OIDC flow
		// returned — not the token's freshness — is the problem. Name that cause
		// precisely so the operator is pointed at the identity provider/client/
		// scope rather than told to re-export a token that cannot help.
		if b.keyless && keylessSubjectFailure(err) {
			return "", fmt.Errorf("%s: signing failed; the identity token carries no verified email or subject claim — the OIDC flow/client did not return an email-scoped identity (re-exporting or re-acquiring the token will not help); configure the identity provider/client/scope to return one: %w", ref, err)
		}
		return "", fmt.Errorf("%s: %w", ref, err)
	}

	if err := attachBundle(ctx, target, subjectRef, artifactType, bundleBytes); err != nil {
		return "", fmt.Errorf("attach signature to %s: %w", ref, err)
	}

	fmt.Fprintf(b.out, "Signature attached: %s (%s)\n", ref, digest)
	return digest, nil
}

// buildRefSigner returns the signer for one reference. A supplied signer is
// reused as-is across the batch; otherwise a fresh signer is built per
// reference from that reference's resolved endpoint URLs and the batch's
// once-resolved identity — the static token or the live source (keyless) or key
// path (keyed). It never resolves an identity — the single keyless resolution is
// owned by prepare.
func (b *batchSigner) buildRefSigner(ref string) (signing.Signer, error) {
	if b.cfg.Signer != nil {
		return b.cfg.Signer, nil
	}
	rw, err := requireWorkspace(b.cfg.Workspace)
	if err != nil {
		return nil, err
	}
	resolved := rw.Config().ResolveSigning(registryhost.Of(ref))
	return buildSigner(signerParams{
		KeyPath:      firstNonEmpty(b.cfg.KeyPath, resolved.KeyPath),
		TokenSource:  b.tokenSource,
		IDToken:      b.idToken,
		FulcioURL:    firstNonEmpty(b.cfg.FulcioURL, resolved.FulcioURL),
		RekorURL:     firstNonEmpty(b.cfg.RekorURL, resolved.RekorURL),
		TimestampURL: firstNonEmpty(b.cfg.TimestampURL, resolved.TimestampURL),
		Config:       rw.Config(),
	})
}

// referenceTag returns the tag or digest portion of an OCI reference.
// A digest reference (…@sha256:…) yields the digest; otherwise the tag
// after the path's last ":" is returned, taking care not to mistake a
// host:port colon for a tag separator. A reference with no tag returns
// the reference unchanged so the target's own resolution decides.
func referenceTag(reference string) string {
	if i := strings.LastIndexByte(reference, '@'); i >= 0 {
		return reference[i+1:]
	}
	tail := reference
	if slash := strings.LastIndexByte(reference, '/'); slash >= 0 {
		tail = reference[slash+1:]
	}
	if c := strings.LastIndexByte(tail, ':'); c >= 0 {
		return tail[c+1:]
	}
	return reference
}
