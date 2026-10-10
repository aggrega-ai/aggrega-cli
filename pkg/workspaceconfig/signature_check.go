// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// SignatureSkip names what turns signature verification off for an artifact. Its
// zero value, SignatureSkipNone, is the ordinary case: the check runs.
type SignatureSkip int

const (
	// SignatureSkipNone is the ordinary case: nothing turns the check off.
	SignatureSkipNone SignatureSkip = iota
	// SignatureSkipGlobal is the top-level skip_signature_verification switch,
	// which turns the check off for every artifact from every registry, the
	// official hub included.
	SignatureSkipGlobal
	// SignatureSkipRegistry is skip_signature_verification on the artifact's own
	// registries entry. It turns the check off for that one host, except for an
	// artifact the top-level signing policy requires a signature for: that
	// requirement is defeated only by the global switch.
	SignatureSkipRegistry
)

// SignatureCheck is what the trust configuration asks of one artifact's signature,
// read for the registry host the artifact is acquired from and the catalog kind it
// publishes under. It is the one reading of that question, and the reason it is a
// value rather than a handful of reads at each site is the halt decision it
// carries: whether an artifact for which no signature verifies stops the operation
// is decided by HaltsWithoutSignature and nowhere else. A pull asks it of every
// artifact it fetches; a build asks it of every artifact it reads from the content
// cache whose lockfile row records no verified signature, so the settings that
// admit an artifact at a pull — the skips and an advisory registry — admit it at a
// build too, and a setting that requires one requires it at both.
//
// A check is built by Config.SignatureCheckFor; the zero value describes a host
// nothing is configured for and requires nothing.
type SignatureCheck struct {
	// Host is the registry host the check was read for.
	Host string
	// Kind is the catalog kind the check was read for — "Component", "Plugin" —
	// or empty where the kind could not be determined.
	Kind string
	// Policy is the trust policy a signature from Host is verified under: the
	// host's registries entry, the official hub's built-in baseline, or, for a
	// host neither covers, a policy carrying only the host.
	Policy RegistryConfig
	// Configured reports whether Policy came from a registries entry or the
	// built-in baseline, rather than being the empty policy of a host nothing
	// covers.
	Configured bool
	// Skip names the switch that turns the check off, or SignatureSkipNone.
	Skip SignatureSkip
	// RequiredByWorkspace reports that the top-level signing policy requires a
	// signature for Kind: signing.required with Kind in signing.kinds, or with
	// Kind undetermined, which an active policy never silently exempts.
	RequiredByWorkspace bool
	// RequiredByRegistry reports that the host's own trust stance requires a
	// signature for Kind: signing.required on its registries entry, or the
	// official hub's built-in requirement where no entry overrides it. An
	// undetermined Kind under an active stance is required here too.
	RequiredByRegistry bool
	// Advisory reports that the host's registries entry marks it advisory: a
	// failed check, a missing signature included, is reported and does not halt.
	Advisory bool

	// fromBaseline records that Policy is the built-in baseline rather than a
	// registries entry, which is all RequirementSource needs to tell the two
	// apart.
	fromBaseline bool
}

// SignatureVerificationDisabled reports whether the top-level
// skip_signature_verification switch turns every signature check off. It is the
// one reading of that switch: SignatureCheckFor reads it through here, and so
// does a pass that announces the switch once rather than once per artifact.
func (c Config) SignatureVerificationDisabled() bool {
	return c.SkipSignatureVerification
}

// SignatureCheckFor returns what the trust configuration asks of the signature of
// an artifact of kind acquired from host.
//
// host may be a registry host or a reference naming one; it is compared with
// each configured host through registryhost.Same, as every per-registry lookup
// is. kind is the canonical catalog kind, or empty where it could not be
// determined — and an empty kind under an active requirement counts as required
// rather than exempt, at both scopes, so an artifact nothing could classify is
// never let through for that reason.
func (c Config) SignatureCheckFor(host, kind string) SignatureCheck {
	policy, configured := c.RegistryTrustPolicy(host)
	check := SignatureCheck{
		Host:       host,
		Kind:       kind,
		Policy:     policy,
		Configured: configured,
		Advisory:   policy.Advisory,
	}
	check.fromBaseline = configured && !c.hasRegistryEntry(host)

	// The two scopes are one trust stance read twice: the top-level block forces
	// a signature on an in-scope kind from every host, even one whose own entry
	// requires none, and a host's own stance requires one for that host alone.
	check.RequiredByWorkspace = c.SigningRequiredForKind(kind) || (c.Signing.Required && kind == "")
	check.RequiredByRegistry = configured &&
		(policy.SigningRequiredForKind(kind) || (policy.Signing.Required && kind == ""))

	switch {
	case c.SignatureVerificationDisabled():
		check.Skip = SignatureSkipGlobal
	case policy.SkipSignatureVerification && !check.RequiredByWorkspace:
		// A host's own break-glass switch does not defeat the top-level policy:
		// an in-scope artifact is still checked on this host. It does defeat the
		// host's own stance — requiring and skipping on one entry is the
		// allowed-but-perverse combination, and the skip wins on the verify side.
		check.Skip = SignatureSkipRegistry
	}
	return check
}

// hasRegistryEntry reports whether a registries entry names host.
func (c Config) hasRegistryEntry(host string) bool {
	for _, r := range c.Registries {
		if registryhost.Same(r.Host, host) {
			return true
		}
	}
	return false
}

// Required reports whether a signature is required for the artifact, at either
// scope. Whether a missing one halts is a further question — see
// HaltsWithoutSignature.
func (s SignatureCheck) Required() bool {
	return s.RequiredByWorkspace || s.RequiredByRegistry
}

// HaltsWithoutSignature reports whether an artifact for which no signature
// verifies stops the operation: none is attached, or none the registry could be
// asked about, or — at a build — none is recorded for it. It holds exactly when
// the check runs, a signature is required, and the host is not advisory.
//
// It is the whole halt decision a pull makes over a missing signature, and the
// whole decision a build makes over a lockfile row recording none, so the two
// cannot disagree about which artifacts a requirement covers.
func (s SignatureCheck) HaltsWithoutSignature() bool {
	return s.Skip == SignatureSkipNone && s.Required() && !s.Advisory
}

// RequirementSource names, in the words of the configuration file, the settings
// that require the artifact's signature — the top-level signing.required, the
// host's own registries entry, the official hub's built-in requirement — joined
// where both scopes require it. It is empty where nothing does.
func (s SignatureCheck) RequirementSource() string {
	var sources []string
	if s.RequiredByWorkspace {
		sources = append(sources, "`signing.required` at the top level of the workspace configuration")
	}
	if s.RequiredByRegistry {
		if s.fromBaseline {
			sources = append(sources, fmt.Sprintf("the official hub's built-in signature requirement for %q", s.Host))
		} else {
			sources = append(sources, fmt.Sprintf("`signing.required` on the `registries` entry for %q", s.Host))
		}
	}
	return strings.Join(sources, " and ")
}
