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
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
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

	// ConfiguredKeyNotConsulted withholds the signing key this workspace's own
	// configuration names, so only a KeyPath this caller supplied can select the
	// keyed branch. See SignerRequest.ConfiguredKeyNotConsulted for what it
	// declares; false is every caller that owns the machine the key sits on.
	ConfiguredKeyNotConsulted bool

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
// credential boundary's input contract.
func (cfg SignConfig) signerRequest() SignerRequest {
	return SignerRequest{
		Workspace:                 cfg.Workspace,
		Signer:                    cfg.Signer,
		KeyPath:                   cfg.KeyPath,
		FulcioURL:                 cfg.FulcioURL,
		RekorURL:                  cfg.RekorURL,
		TimestampURL:              cfg.TimestampURL,
		IDToken:                   cfg.IDToken,
		EnvLookup:                 cfg.EnvLookup,
		ConfiguredKeyNotConsulted: cfg.ConfiguredKeyNotConsulted,
		AcquireIdentityToken:      interactiveAcquirer(cfg.TokenGetter, cfg.IsTTY),
		IdentitySupply:            cfg.IdentitySupply,
	}
}

// SignResult reports which references the batch signed, in the order they were
// given, as the facts themselves rather than as the status lines Sign also writes
// to cfg.Out. A reference found already carrying a signature its registry's trust
// policy verifies is reported here too, with the digest that signature commits to:
// it is signed, though this batch attached nothing to it.
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
// manifest, so signing is immutability-safe. A reference that already carries a
// signature its registry's trust policy verifies is reported signed and gets
// nothing attached; signRef states when that holds.
//
// One credential serves the whole batch, resolved before any reference is read at
// its registry: a single held key, or a single keyless identity — a static token,
// or a live source that mints a fresh token per signature — while a fresh
// short-lived certificate is issued per artifact by each reference's own signer,
// against the endpoints that reference's registry configures. References that
// would need different credentials — a key the configuration names for one
// registry and none for another, or two different keys — refuse the batch with
// nothing signed (signing.ErrMixedSigningCredentials). The keyed path resolves no
// identity.
//
// Signing is continue-and-report: a reference that fails to resolve, sign, or
// attach — or whose existing signatures could not be judged — is recorded and the
// batch proceeds to the next reference; after the loop, if any reference failed, a
// summary naming each failure is returned.
func Sign(ctx context.Context, cfg SignConfig) (SignResult, error) {
	if len(cfg.References) == 0 {
		return SignResult{}, fmt.Errorf("at least one reference required")
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	// The credential is resolved once, over every reference, through the one home
	// every signing verb shares. The identity identifies the signer, not the
	// destination, so a keyless batch asks for it a single time here.
	cred, err := resolveCredential(cfg.signerRequest(), cfg.References, nil)
	if err != nil {
		return SignResult{}, err
	}
	b := &batchSigner{cfg: cfg, cred: cred, out: out}

	var (
		failures []refFailure
		signed   []SignedArtifact
	)
	for _, ref := range cfg.References {
		// One interruption point per reference, which is this batch's seam: each
		// iteration resolves, signs and attaches one artifact at the registry. A
		// reference not yet reached is one never signed, and the check precedes the
		// attach rather than following it, because an attached signature is
		// published and cannot be withdrawn.
		if serr := stopped(ctx); serr != nil {
			return SignResult{Signed: signed}, serr
		}
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

// batchSigner carries the batch's once-resolved credential across the batch loop.
type batchSigner struct {
	cfg SignConfig

	// cred is the one credential every reference is signed with, resolved by Sign
	// over every reference before the loop.
	cred *credential

	// out is the resolved sink for the per-reference status line, set once by
	// Sign.
	out io.Writer
}

// signRef signs one reference and attaches its signature, returning the published
// digest the signature commits to. It asks the batch's credential for the
// reference's signer — never resolving a credential or an identity itself; the one
// resolution happened in Sign, over every reference. A keyless failure whose cause
// is a token without a usable signing subject is mapped to a precise diagnostic;
// every other failure is returned as-is so the batch continues and reports it.
//
// A reference that already carries a signature its registry's trust policy
// verifies — made by any identity — is not signed again. Before building a signer
// it asks the one signature walk, and where a signature verifies it writes who
// signed it and, where the transparency log timed it, when (the earliest such
// signature, the one a reader dates the version by), attaches nothing, and returns
// the digest, so the batch reports the reference signed. A second signature would
// add a transparency-log entry and a referrer and change nothing a reader takes
// from the version.
//
// A registry whose policy pins no trust anchor — no public key and no keyless
// identities — gives nothing to judge a signature against, so a reference there is
// signed as one carrying no signature is. And where the walk could not finish — the
// referrer listing could not be completed, or no signature verified while one could
// not be read or judged — the reference is reported as failed and nothing is
// signed: an unanswered question is not read as "unsigned", so a run never adds a
// signature on the strength of a registry that failed to answer.
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

	existing, err := b.existingSignature(ctx, target, ref, subjectRef, digest)
	if err != nil {
		return "", err
	}
	if existing != nil {
		fmt.Fprintf(b.out, "Already signed: %s (%s) by %s%s; nothing attached\n", ref, digest, existing.Identity, loggedClause(existing))
		return digest, nil
	}

	signer, err := b.cred.signerFor(ref)
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
		if b.cred.keyless() && keylessSubjectFailure(err) {
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

// existingSignature returns the earliest signature ref already carries that its
// registry's trust policy verifies, or nil where it carries none or the policy
// pins no trust anchor to judge one against. The policy is the one this workspace
// holds for the registry ref names. An error means the answer is not known, and it
// names the reference and that nothing was signed.
func (b *batchSigner) existingSignature(ctx context.Context, target oras.ReadOnlyTarget, ref, subjectRef, digest string) (*pullverify.Result, error) {
	rw, err := requireWorkspace(b.cfg.Workspace)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ref, err)
	}
	wsCfg := rw.Config()
	policy, _ := wsCfg.RegistryTrustPolicy(registryhost.Of(ref))
	res, _, err := verifyingSignature(ctx, wsCfg, target, subjectRef, digest, policy)
	if err != nil {
		return nil, fmt.Errorf("%s: whether it already carries a signature that verifies could not be read, so nothing was signed: %w", ref, err)
	}
	return res, nil
}

// loggedClause states when the transparency log recorded a signature, for the
// line naming a signature already attached; a signature the log did not time — a
// keyed one — has no clause.
func loggedClause(sig *pullverify.Result) string {
	if sig.LoggedAt.IsZero() {
		return ""
	}
	return ", logged " + oci.FormatPublishedAt(sig.LoggedAt)
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
