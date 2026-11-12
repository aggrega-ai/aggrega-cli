// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package publishops

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// credential is what one signing operation signs with: a single held key, or a
// single keyless identity, decided for every reference the operation names before
// anything is signed.
//
// It is produced by resolveCredential, which is the one place this package turns
// a caller's signing inputs and a workspace's configuration into signers. Every
// verb that signs reaches it — a publication, a signing batch, and a deprecation
// marking, single or batched, on the command line, the daemon and the tool
// protocol alike — so the precedence chain, the configured-key withholding, the
// identity ladder and the one-credential rule are each stated once rather than
// once per verb.
type credential struct {
	// supplied is the signer the caller handed in. It stands in for everything
	// below: nothing is resolved, and every reference is signed with it.
	supplied signing.Signer

	// keyPath is the held key every reference is signed with. Empty is keyless.
	keyPath string

	// idToken and tokenSource are the keyless identity, resolved once for the
	// whole operation: a static token, or a live source that mints a fresh token
	// per signature. Exactly one is set on the keyless path; neither on the keyed.
	idToken     string
	tokenSource signing.IdentityTokenSource

	// endpoints is the signing configuration each reference's registry applies,
	// as this caller may see it, read once when the credential was resolved. A
	// reference absent from it was not part of the operation.
	endpoints map[string]workspaceconfig.SigningConfig

	req SignerRequest
	cfg workspaceconfig.Config

	// refusal, when set, is the condition a signer that cannot be built is
	// reported under beside its own cause.
	refusal error
}

// keyless reports whether the credential signs through the keyless flow.
func (c *credential) keyless() bool { return c.keyPath == "" }

// resolveCredential resolves the one credential a signing operation signs every
// reference in references with, before anything is signed and before any
// registry is read.
//
// Each reference's signing configuration is read through resolveSigning, so a
// caller that withheld the configured key sees none on any registry. The held key
// that applies to a reference is the caller's own key path when it supplied one —
// for every reference alike — and otherwise the key its registry's configuration
// names. When the references do not all resolve to the same key, or to keyless
// alike, the operation is refused, naming each credential and the registries that
// would be signed with it, and nothing further is resolved: no identity is asked
// for, so nobody is prompted for an operation that is not going to run. When they
// agree on keyless, the identity is resolved here, once, for every reference.
//
// refusal, when non-nil, is the condition a failure to resolve a signer is
// reported under beside its own cause. A publication passes its
// unsigned-publication refusal, because there a signer that cannot be had means
// the artifact would land unsigned. The refusal of a mixed operation is never
// wrapped: it names a condition of its own.
//
// A supplied signer (SignerRequest.Signer) is the whole credential: nothing is
// resolved, no identity is asked for, and every reference is signed with it.
func resolveCredential(req SignerRequest, references []string, refusal error) (*credential, error) {
	if req.Signer != nil {
		return &credential{supplied: req.Signer, keyPath: req.KeyPath}, nil
	}
	if len(references) == 0 {
		return nil, fmt.Errorf("internal: no reference to resolve a signing credential for")
	}
	rw, err := requireWorkspace(req.Workspace)
	if err != nil {
		return nil, err
	}
	cfg := rw.Config()

	c := &credential{
		req:       req,
		cfg:       cfg,
		refusal:   refusal,
		endpoints: make(map[string]workspaceconfig.SigningConfig, len(references)),
	}
	var sides []credentialSide
	for _, ref := range references {
		configured := resolveSigning(cfg, ref, req.ConfiguredKeyNotConsulted)
		c.endpoints[ref] = configured
		sides = withReference(sides, firstNonEmpty(req.KeyPath, configured.KeyPath), registryhost.Of(ref))
	}
	if len(sides) > 1 {
		return nil, mixedCredentialsRefusal(cfg, sides)
	}
	c.keyPath = sides[0].keyPath

	if c.keyless() {
		idToken, source, ierr := resolveIdentity(req, cfg)
		if ierr != nil {
			if refusal != nil {
				return nil, fmt.Errorf("%w: %w", ierr, refusal)
			}
			return nil, ierr
		}
		c.idToken, c.tokenSource = idToken, source
	}
	return c, nil
}

// signerFor returns the signer one reference is signed with: the supplied signer,
// or one built from the credential's key or identity and the endpoints that
// reference's registry configures, so references on different registries keep
// their own certificate authority and transparency log while sharing the one
// credential.
//
// A reference the credential was not resolved for is refused rather than resolved
// late: the one-credential rule holds only over the references it was checked
// against, so a signer handed out for any other would sign outside it.
func (c *credential) signerFor(reference string) (signing.Signer, error) {
	if c.supplied != nil {
		return c.supplied, nil
	}
	endpoints, resolved := c.endpoints[reference]
	if !resolved {
		return nil, fmt.Errorf("internal: %s is not among the references its signing credential was resolved for", reference)
	}
	signer, err := buildSigner(signerParams{
		KeyPath:      c.keyPath,
		IDToken:      c.idToken,
		TokenSource:  c.tokenSource,
		FulcioURL:    firstNonEmpty(c.req.FulcioURL, endpoints.FulcioURL),
		RekorURL:     firstNonEmpty(c.req.RekorURL, endpoints.RekorURL),
		TimestampURL: firstNonEmpty(c.req.TimestampURL, endpoints.TimestampURL),
		Config:       c.cfg,
	})
	if err != nil {
		if c.refusal != nil {
			return nil, fmt.Errorf("construct signer: %w: %w", err, c.refusal)
		}
		return nil, err
	}
	return signer, nil
}

// resolveSigner resolves the credential of an operation that signs one reference
// and returns that reference's signer.
func resolveSigner(req SignerRequest, reference string, refusal error) (signing.Signer, error) {
	c, err := resolveCredential(req, []string{reference}, refusal)
	if err != nil {
		return nil, err
	}
	return c.signerFor(reference)
}

// credentialSide is one credential some of an operation's references would be
// signed with, and the registries those references name, each once, in the order
// the references first name them.
type credentialSide struct {
	keyPath string
	hosts   []string
}

// withReference records that a reference on host would be signed with the held
// key at keyPath, or keyless where keyPath is empty. Sides keep the order in which
// the references first reach them, so a refusal names them as the caller wrote
// them.
func withReference(sides []credentialSide, keyPath, host string) []credentialSide {
	for i := range sides {
		if sides[i].keyPath != keyPath {
			continue
		}
		for _, known := range sides[i].hosts {
			if registryhost.Same(known, host) {
				return sides
			}
		}
		sides[i].hosts = append(sides[i].hosts, host)
		return sides
	}
	return append(sides, credentialSide{keyPath: keyPath, hosts: []string{host}})
}

// mixedCredentialsRefusal composes the refusal of an operation whose references
// would be signed with different credentials: the condition's sentence, then each
// credential with the registries it would sign for, then the remedy.
//
// A held key is named by the setting that selects it and never by its path:
// where a key sits is information about this machine, while the setting is what
// the operator wrote and can change.
//
// Where a registry in the operation requires signatures, the remedy says so: the
// refusal then holds back signing a registry needs, and the reader is told it is
// the combination being refused rather than either credential.
func mixedCredentialsRefusal(cfg workspaceconfig.Config, sides []credentialSide) error {
	values := make([]string, 0, len(sides))
	parts := make([]string, 0, len(sides))
	keyedSeen := false
	var required []string
	for _, side := range sides {
		hosts := strings.Join(side.hosts, " and ")
		verb := "signs"
		if len(side.hosts) > 1 {
			verb = "sign"
		}
		switch {
		case side.keyPath == "":
			values = append(values, hosts+" "+verb+" keyless: no signing.key_path applies")
		case !keyedSeen:
			values = append(values, hosts+" "+verb+" with the held key signing.key_path names")
			keyedSeen = true
		default:
			values = append(values, hosts+" "+verb+" with a different held key signing.key_path names")
		}
		parts = append(parts, "the references on "+hosts)
		for _, host := range side.hosts {
			if requiresSignatures(cfg, host) {
				required = append(required, host)
			}
		}
	}
	advice := "remediation: sign them in separate runs, one per credential: " + strings.Join(parts, ", then ") +
		"; each run signs as it would on its own"
	if len(required) > 0 {
		verb := "requires"
		if len(required) > 1 {
			verb = "require"
		}
		advice += ", and " + strings.Join(required, " and ") + " " + verb + " signatures, so this refusal holds " +
			"back signing that would otherwise have gone ahead rather than signing that cannot be done"
	}
	return &mixedCredentialsError{
		message: cerrors.ComposeMessage(signing.ErrMixedSigningCredentials(), values, advice),
	}
}

// requiresSignatures reports whether host's trust stance requires a signature on
// whatever it serves: the workspace-wide signing policy, or the host's own — its
// registry entry's signing block, or the hub's baseline. No kind is known for a
// reference here, so a stance scoped to named kinds is not read as covering it.
func requiresSignatures(cfg workspaceconfig.Config, host string) bool {
	if cfg.SigningRequiredForKind("") {
		return true
	}
	policy, _ := cfg.RegistryTrustPolicy(host)
	return policy.SigningRequiredForKind("")
}

// mixedCredentialsError is the refusal resolveCredential returns for an operation
// whose references need different credentials. It renders the composed message
// and unwraps to the condition, so the condition's identifiers are read off it
// however many frames a caller wraps around it.
type mixedCredentialsError struct {
	message string
}

func (e *mixedCredentialsError) Error() string { return e.message }

func (e *mixedCredentialsError) Unwrap() error { return signing.ErrMixedSigningCredentials() }
