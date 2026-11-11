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

	"github.com/aggrega-ai/aggrega-cli/internal/opprogress"
	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// SignerRequest carries a caller's credential inputs. It is the single input
// contract for the credential boundary: publication, signing and deprecation
// marking all resolve their signers from one of these through resolveCredential,
// so the precedence chain and the identity ladder are stated once rather than
// once per verb.
//
// It names no registry. What a verb acts on — a publication's destination, the
// references a signing or a deprecation amends — is handed to resolveCredential
// beside it, because the per-registry overlay is keyed on the host each
// credential will be used against, and an operation can name several.
type SignerRequest struct {
	// Workspace is the resolved workspace the caller threads in. Required: the
	// boundary refuses a nil rather than resolving one of its own, because
	// resolving here would make the result depend on a directory no caller named.
	Workspace *workspaceconfig.ResolvedWorkspace

	// Signer overrides construction entirely. A caller that already holds a signer
	// supplies it, and neither the precedence chain nor the identity ladder is
	// consulted.
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

	// ConfiguredKeyNotConsulted withholds the signing key the workspace's own
	// configuration names, so only a key path this caller supplied can select the
	// keyed branch. False is every caller that owns the machine the key sits on,
	// and is the default.
	//
	// It is a declaration about the CALLER rather than about the key. A key path
	// supplied on the call is a file the caller named; a key path the served
	// workspace's configuration names is a secret the server holds, and a caller
	// who merely reached the server proves nothing by causing it to be spent. A
	// caller for whom that distinction matters sets this, and the signature is
	// then either attributable to an identity the caller supplied or refused.
	ConfiguredKeyNotConsulted bool

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

// resolveSigning resolves the signing configuration that applies to the registry
// host reference names, as this layer is allowed to see it.
//
// It is the ONE route from a workspace's configuration to a signing input in this
// package, and resolveCredential is its one caller. Two things are single-sourced
// here rather than at each verb: which host the per-registry overlay is keyed on,
// and whether the key path that configuration names is reachable at all.
//
// A caller that withheld the configured key gets a view that names none. The key
// is removed from the VALUE rather than left in it for each reader to remember
// not to use: a later reader of this configuration has no key to reach for, so
// the withholding does not depend on anyone downstream knowing about it. Nothing
// else is withheld — the endpoint URLs name services rather than secrets, and a
// caller that cannot select a key still signs against the endpoints the
// deployment configured.
func resolveSigning(cfg workspaceconfig.Config, reference string, configuredKeyNotConsulted bool) workspaceconfig.SigningConfig {
	resolved := cfg.ResolveSigning(registryhost.Of(reference))
	if configuredKeyNotConsulted {
		resolved.KeyPath = ""
	}
	return resolved
}

// resolveIdentity resolves the keyless identity for one credential through the
// shared precedence ladder, as either a static token or a live source. Its one
// caller is resolveCredential, which asks once per operation.
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
// layer, reached through (*credential).signerFor. A non-empty KeyPath selects the
// offline keyed signer, reading the held
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
//
// It is also the one place a whole signing call is retried. A transient failure
// of the network or the certificate authority's token-processing failure
// (IsRetryablePublishError) is retried here, with the shared budget and backoff,
// so a single flaky token mint loses neither a publication nor one reference of a
// batch. A rate-limited or server-error answer from the certificate authority,
// the transparency log or the timestamp authority is not retried here: the
// keyless signer's own clients retry that one request inside the call, so it
// reaches this classifier only once those attempts are spent. The transparency
// log's client also retries an upload whose connection failed, so a network
// failure there is retried inside the call before this retry repeats the whole
// call. The classifier sees
// the signer's raw error; whatever a caller maps it to acts on the error that
// survives the last attempt.
func signDigest(ctx context.Context, signer signing.Signer, digest string) (bundle []byte, artifactType string, err error) {
	res, err := retry.Transient(ctx, func() (signBundle, error) {
		b, at, serr := signer.Sign(ctx, digest)
		return signBundle{bytes: b, artifactType: at}, serr
	}, IsRetryablePublishError)
	if err != nil {
		return nil, "", err
	}
	return res.bytes, res.artifactType, nil
}

// attachBundle is the shared attach atom: it attaches a signature bundle as an
// OCI referrer of the artifact published under subjectRef. The artifact itself
// is untouched — the referrer is a sibling manifest pointing back at it, so
// attaching never re-pushes or mutates published bytes.
func attachBundle(ctx context.Context, target oras.Target, subjectRef, artifactType string, bundle []byte) error {
	end := opprogress.Begin(ctx, "attaching the signature")
	_, err := oci.AttachReferrer(ctx, target, subjectRef, artifactType, bundle)
	end(err)
	return err
}

// resolvePublished is oci.Resolve reported as a step the publication waits on:
// the digest the registry has for tag, and whether it has one. artifact names
// what is looked up, as the step names it.
func resolvePublished(ctx context.Context, target oras.ReadOnlyTarget, tag, artifact string) (string, bool, error) {
	end := opprogress.Begin(ctx, "checking whether "+artifact+" is published")
	digest, exists, err := oci.Resolve(ctx, target, tag)
	end(err)
	return digest, exists, err
}

// resolvePublishSigner resolves the publish-time signer for the destination
// registry ONCE, through the one credential home every verb shares. The single
// resolution is what guarantees that when one publication signs more than one
// artifact, both signatures come from the SAME signer — a per-artifact resolution
// would, for keyless, acquire a fresh ephemeral identity per call and silently
// split the signer. It is called BEFORE any bytes reach the registry, so a signer
// that cannot be resolved refuses the publication with nothing published.
//
// Its failures wrap the unsigned-publication refusal sentinel, because on this
// path a signer that cannot be had means the artifact would land unsigned. That
// wrapping is the one thing publication passes the home that no other verb does.
func resolvePublishSigner(req SignerRequest, registry string) (signing.Signer, error) {
	return resolveSigner(req, registry, signing.ErrUnsignedPushRefused())
}

// signPublishDigestWith signs proposedDigest with an already-resolved signer,
// mapping a keyless-subject failure to a precise diagnostic and wrapping
// every signing failure with the unsigned-publication refusal so the publication
// is refused with nothing published. It is the sign half of the publish-time
// signing atom; callers signing more than one artifact resolve the signer once
// via resolvePublishSigner and call this per digest so both signatures share the
// signer. reference and tag name the artifact in any rejection message.
func signPublishDigestWith(ctx context.Context, signer signing.Signer, reference, tag, proposedDigest string) (bundle []byte, artifactType string, err error) {
	// signDigest retries a transient signing failure itself, so the mapping below
	// acts on the error that survives its last attempt.
	end := opprogress.Begin(ctx, "signing "+reference+":"+tag)
	bundle, artifactType, err = signDigest(ctx, signer, proposedDigest)
	end(err)
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
// artifact: it resolves a signer for the destination registry and signs
// proposedDigest, returning the signature bundle and its artifact type. It is
// called BEFORE any bytes reach the registry, so a signer that cannot be
// constructed or a signing failure refuses the publication with nothing
// published. reference and tag name the artifact in any rejection message.
func signPublishDigest(ctx context.Context, req SignerRequest, registry, reference, tag, proposedDigest string) (bundle []byte, artifactType string, err error) {
	signer, err := resolvePublishSigner(req, registry)
	if err != nil {
		return nil, "", err
	}
	return signPublishDigestWith(ctx, signer, reference, tag, proposedDigest)
}
