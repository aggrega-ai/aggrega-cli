// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// SignerRequest carries the credential inputs one signing posture needs. It is
// the single input contract for the credential boundary: publication, artifact
// publication, signing and deprecation marking all resolve their signer from
// one of these, so the precedence chain and the identity ladder are stated once
// rather than once per verb.
//
// Registry names the host whose per-registry signing configuration applies. Each
// verb derives it from what it is acting on — a publication from its destination,
// a signing or a deprecation from the reference it is amending — because the
// per-registry overlay is keyed on the host the credential will be used against.
type SignerRequest struct {
	// Workspace is the resolved workspace the caller threads in. Required: the
	// boundary refuses a nil rather than resolving one of its own, because
	// resolving here would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// Registry is the host whose per-registry signing overlay applies.
	Registry string

	// Signer overrides construction entirely. A caller that already holds a signer
	// supplies it and the precedence chain is not consulted.
	Signer signing.Signer

	// KeyPath selects the offline keyed signer when non-empty. Empty selects the
	// live keyless flow, which is the only branch that resolves an identity.
	KeyPath string

	// FulcioURL, RekorURL and TimestampURL are the keyless endpoint overrides.
	// An empty Fulcio or Rekor URL falls through to the per-registry then global
	// configuration and finally to the public-good instance. The timestamp
	// authority is never defaulted: it is opt-in.
	FulcioURL    string
	RekorURL     string
	TimestampURL string

	// IDToken is an explicitly supplied keyless identity token. It is never
	// sourced from configuration — it is a short-lived secret supplied at runtime.
	IDToken string

	// EnvLookup reads the environment for the ambient identity-token resolution.
	// Nil means the live process environment.
	EnvLookup func(string) string

	// AcquireIdentityToken is the one rung of the identity ladder that needs a
	// person: it acquires a token interactively when every supplied and ambient
	// input has declined. Nil is the unattended shape and is the default — the
	// shared ladder then produces its own refusal naming what to supply, so a
	// caller with nobody watching cannot hang on a flow nobody is watching.
	// Supplying it is the caller's declaration that a person is watching.
	AcquireIdentityToken signing.InteractiveIdentityTokenAcquirer

	// IdentitySupply carries the caller's own vocabulary into the ladder's
	// refusal, so a caller is told to supply something it can supply. Leaving it
	// zero yields a refusal naming only the inputs every caller shares.
	IdentitySupply signing.IdentityTokenSupply
}

// signerParams carries the resolved signing inputs buildSigner constructs a
// signer from. The values are already resolved through the precedence chain
// (caller-supplied value > per-registry config > global config) by the caller;
// buildSigner applies only the public-good endpoint defaults on top. The identity
// is never sourced from config — it is a short-lived secret supplied at runtime.
//
// The identity reaches buildSigner one of two ways. IDToken carries a token
// resolved once as a string (a supplied value, an environment variable, or a
// single interactive acquisition); buildSigner validates its subject at
// construction and wraps it as a constant source. TokenSource, when set, is a
// live source buildSigner uses directly — it is fetched fresh per signature and
// validates its own subject per fetch. Exactly one of the two is populated for
// the keyless path; both are empty for the keyed path.
type signerParams struct {
	KeyPath      string
	IDToken      string
	TokenSource  signing.IdentityTokenSource
	FulcioURL    string
	RekorURL     string
	TimestampURL string
	Config       workspaceconfig.Config
}

// firstNonEmpty returns the first non-empty string in order, implementing
// one precedence step: a caller-supplied value wins over a config-resolved value.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// requireWorkspace returns the workspace a caller threaded in from its own
// resolution chokepoint. A nil workspace is a wiring error — the call was reached
// without any caller having resolved one — so it is reported rather than
// repaired. Resolving here from the current directory would make the result
// depend on a directory no caller named, which is exactly the dependency routing
// every surface through one resolution point exists to remove.
func requireWorkspace(ws *workspaceconfig.ResolvedWorkspace) (*workspaceconfig.ResolvedWorkspace, error) {
	if ws == nil {
		return nil, fmt.Errorf("internal: no resolved workspace supplied — the caller must resolve one and pass it in")
	}
	return ws, nil
}

// egressClient derives the network-egress HTTP client for a resolved workspace
// configuration, refusing when the workspace is offline. The offline decision
// lives once at the gate; each caller reacts to the refusal in its own
// loud-or-silent way.
func egressClient(cfg workspaceconfig.Config) (*http.Client, error) {
	return egress.HTTPClient(cfg)
}

// refuseIfOffline is the pre-flight offline refusal for a publication whose own
// work is a network round trip: it names what was refused and which layer set
// the posture, before the operation bundles anything.
//
// It sits beside egressClient because both are this package's only route to the
// one gate, so neither the late refusal nor the early one can come to read the
// offline decision from anywhere else. A publication with an offline-safe form
// — a dry run, which prints the reference it would publish and constructs no
// target — does not call it.
func refuseIfOffline(rw *workspaceconfig.ResolvedWorkspace, act string) error {
	return egress.RefuseIfOffline(rw, act)
}

// envLookup returns look when non-nil, else the live process environment. It is a
// nil-default rather than a resolution rule: the ladder that decides which
// environment variables carry an identity belongs to the signing package.
func envLookup(look func(string) string) func(string) string {
	if look != nil {
		return look
	}
	return os.Getenv
}

// resolveIdentity resolves the keyless identity for one signing posture through
// the shared precedence ladder, as either a static token or a live source. It is
// the single identity-resolution site for every verb in this layer.
//
// The precedence itself, and the refusal that ends it, belong to the signing
// package. What this adds is only the caller's optional interactive rung, which
// sits below every supplied and ambient input, so a token that is already
// available is always preferred over asking. A caller that supplies none — every
// caller with nobody watching — leaves the ladder to refuse.
func resolveIdentity(req SignerRequest, cfg workspaceconfig.Config) (idToken string, source signing.IdentityTokenSource, err error) {
	ambientClient, _ := egressClient(cfg)
	return signing.ResolveIdentityToken(req.IDToken, envLookup(req.EnvLookup), checkIDTokenSubject, ambientClient, req.AcquireIdentityToken, req.IdentitySupply)
}

// buildSigner is the single signer-construction path for every verb in this
// layer. A non-empty KeyPath selects the offline keyed signer, reading the held
// PEM from disk; otherwise the live keyless signer is constructed. The
// public-good Sigstore endpoint defaults are applied here — the one home for
// endpoint defaulting — so an unset Fulcio/Rekor URL falls back to the
// community-operated instances. The RFC3161 timestamp authority is not
// defaulted: it is opt-in, set only when the caller supplies one, because
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
	source := p.TokenSource
	if source == nil {
		// Static-token path: the identity is a fixed string resolved once. The
		// same subject the certificate authority derives from the token's claims
		// must be present before the token is handed off, so a token that
		// structurally lacks a usable subject fails fast here with a precise
		// diagnostic instead of as an opaque downstream "no subject found in
		// claims". This decodes the token's claims without verifying its
		// signature — it is a verification check, not a trust decision; the
		// certificate authority still performs the real verification. A live
		// source validates its own freshly-fetched token per fetch instead, so
		// this construction-time check applies only to the static path.
		if err := checkIDTokenSubject(p.IDToken); err != nil {
			return nil, err
		}
		source = signing.StaticIdentityToken(p.IDToken)
	}
	return &signing.KeylessSigner{
		TokenSource:  source,
		FulcioURL:    fulcio,
		RekorURL:     rekor,
		TimestampURL: p.TimestampURL,
		Config:       p.Config,
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
// the credential boundary with a precise message rather than as an opaque downstream
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
// manifest digest. Every verb calls it — signing against an already-published
// digest, publication against the digest it is about to publish — so the
// signature is computed through one path. The digest string is the only thing
// committed to; the bundle is identical whether produced before or after the
// bytes land.
func signDigest(ctx context.Context, signer signing.Signer, digest string) (bundle []byte, artifactType string, err error) {
	return signer.Sign(ctx, digest)
}

// attachBundle is the shared attach atom: it attaches a signature bundle as an
// OCI referrer of the artifact published under subjectRef. The artifact itself
// is untouched — the referrer is a sibling manifest pointing back at it, so
// attaching never re-pushes or mutates published bytes.
func attachBundle(ctx context.Context, target oras.Target, subjectRef, artifactType string, bundle []byte) error {
	if _, err := oci.AttachReferrer(ctx, target, subjectRef, artifactType, bundle); err != nil {
		return err
	}
	return nil
}

// resolvePublishSigner constructs the publish-time signer ONCE: the caller's
// supplied signer when present, otherwise one built from the config-resolved
// keyed/keyless inputs through the precedence chain. The single construction is
// what guarantees that when one publication signs more than one artifact, both
// signatures come from the SAME signer — a per-artifact construction would, for
// keyless, acquire a fresh ephemeral identity per call and silently split the
// signer. It is called BEFORE any bytes reach the registry, so a signer that
// cannot be constructed refuses the publication with nothing published.
//
// Its failures wrap the unsigned-publication refusal sentinel, because on this
// path a signer that cannot be built means the artifact would land unsigned.
func resolvePublishSigner(req SignerRequest) (signing.Signer, error) {
	if req.Signer != nil {
		return req.Signer, nil
	}
	rw, werr := requireWorkspace(req.Workspace)
	if werr != nil {
		return nil, werr
	}
	resolved := rw.Config().ResolveSigning(registryhost.Of(req.Registry))
	keyPath := firstNonEmpty(req.KeyPath, resolved.KeyPath)
	// Only the keyless branch resolves an identity; the keyed path never
	// reaches acquisition. Resolve the identity as a SOURCE before constructing
	// the signer so a missing token refuses BEFORE any bytes reach the
	// registry. A static resolution comes back as idToken (validated once at
	// construction); the ambient resolution comes back as a live source that
	// stays fresh per signature. The single construction below is what
	// guarantees both signatures of a multi-artifact publication share one source.
	var idToken string
	var tokenSource signing.IdentityTokenSource
	if keyPath == "" {
		var terr error
		idToken, tokenSource, terr = resolveIdentity(req, rw.Config())
		if terr != nil {
			return nil, fmt.Errorf("%w: %w", terr, signing.ErrUnsignedPushRefused())
		}
	}
	signer, berr := buildSigner(signerParams{
		KeyPath:      keyPath,
		IDToken:      idToken,
		TokenSource:  tokenSource,
		FulcioURL:    firstNonEmpty(req.FulcioURL, resolved.FulcioURL),
		RekorURL:     firstNonEmpty(req.RekorURL, resolved.RekorURL),
		TimestampURL: firstNonEmpty(req.TimestampURL, resolved.TimestampURL),
		Config:       rw.Config(),
	})
	if berr != nil {
		return nil, fmt.Errorf("construct signer: %w: %w", berr, signing.ErrUnsignedPushRefused())
	}
	return signer, nil
}

// signPublishDigestWith signs proposedDigest with an already-resolved signer,
// mapping a keyless-subject failure to a precise diagnostic and wrapping
// every signing failure with the unsigned-publication refusal so the publication
// is refused with nothing published. It is the sign half of the publish-time
// signing atom; callers signing more than one artifact resolve the signer once
// via resolvePublishSigner and call this per digest so both signatures share the
// signer. reference and tag name the artifact in any rejection message.
func signPublishDigestWith(ctx context.Context, signer signing.Signer, reference, tag, proposedDigest string) (bundle []byte, artifactType string, err error) {
	// Retry the raw signing call on a transient certificate-authority or
	// network failure so a long publication is not lost to a single flaky token
	// mint. The retry wraps signDigest BEFORE the refusal mapping below, so the
	// classifier sees the raw underlying error; the subject-failure mapping and
	// terminal wrapping act on the error that survives retry exhaustion.
	res, err := RetryTransient(ctx, func() (signBundle, error) {
		b, at, serr := signDigest(ctx, signer, proposedDigest)
		return signBundle{bytes: b, artifactType: at}, serr
	}, IsRetryablePublishError)
	bundle, artifactType = res.bytes, res.artifactType
	if err != nil {
		// A token without a usable signing subject is misdiagnosed by a generic
		// rejection message; name the real cause — the identity the OIDC flow
		// returned — so the operator fixes the provider/client/scope rather than
		// re-exporting a token that cannot help.
		if keylessSubjectFailure(err) {
			return nil, "", fmt.Errorf("sign %s:%s: the identity token carries no verified email or subject claim — the OIDC flow/client did not return an email-scoped identity (re-exporting or re-acquiring the token will not help); configure the identity provider/client/scope to return one: %w: %w", reference, tag, err, signing.ErrUnsignedPushRefused())
		}
		return nil, "", fmt.Errorf("sign %s:%s: %w: %w", reference, tag, err, signing.ErrUnsignedPushRefused())
	}
	return bundle, artifactType, nil
}

// signPublishDigest is the shared publish-time signing atom for a single
// artifact: it resolves a signer and signs proposedDigest, returning the
// signature bundle and its artifact type. It is called BEFORE any bytes reach
// the registry, so a signer that cannot be constructed or a signing failure
// refuses the publication with nothing published. reference and tag name the
// artifact in any rejection message.
func signPublishDigest(ctx context.Context, req SignerRequest, reference, tag, proposedDigest string) (bundle []byte, artifactType string, err error) {
	signer, err := resolvePublishSigner(req)
	if err != nil {
		return nil, "", err
	}
	return signPublishDigestWith(ctx, signer, reference, tag, proposedDigest)
}
