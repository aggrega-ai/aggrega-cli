// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package registrytrust defines the advertised-trust contract a registry
// serves at its well-known trust endpoint, and the trust-on-first-use fetch
// the client runs when a registry is first registered.
//
// A registry MAY publish its signature trust policy as a static JSON document
// at `https://<host>/.well-known/aggrega-trust`. When an operator registers
// the registry, the client fetches that document once and PINS it into the
// local workspace config. Verification at pull time always runs against the
// pinned local policy, never a live re-fetch: a registry that could silently
// change the trust set every pull would be self-certifying, defeating the
// point of signature verification. Pinning at registration is the
// trust-on-first-use decision — the operator vouches for the advertised set
// the moment they add the registry, exactly as apt, apk, and the Go checksum
// database bootstrap trust on first contact.
//
// The advertised document is advisory input to that one-time pin, not an
// authority: manual identities passed at registration override the fetched
// set, and a registry that advertises nothing (a 404 or an unreachable
// endpoint) is not an error — the operator simply pins identities by hand.
package registrytrust

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// WellKnownPath is the path, relative to a registry's own origin, at which a
// registry advertises its trust policy. The full URL a client fetches is
// `https://<host>` + WellKnownPath (plain HTTP for a loopback host).
const WellKnownPath = "/.well-known/aggrega-trust"

// mediaType is the content-type a registry SHOULD serve the advertised trust
// document with. The fetch does not hard-require it — a static-asset host that
// serves application/octet-stream still decodes — but a well-behaved registry
// sets it so intermediaries treat the body as JSON.
const mediaType = "application/json"

// AdvertisedTrust is the wire shape a registry serves at WellKnownPath. It is
// the subset of a registry's signature trust policy the registry is willing to
// publish for trust-on-first-use bootstrap; the client pins these fields into
// the local per-registry config at registration.
//
// The field set deliberately mirrors the verify-side policy the client already
// holds per registry, so a pin is a direct field copy with no translation:
// RequiresSignatures gates unsigned artifacts, TrustedIdentities is the OR-set
// a keyless artifact signature is matched against, TrustedDeprecationIdentities
// is the additional OR-set accepted to sign a retroactive deprecation marker,
// and PublicKey pins a keyed-mode registry's cosign-compatible public key.
type AdvertisedTrust struct {
	// RequiresSignatures, when true, tells the client this registry expects
	// every artifact to be signed. Pinned onto the per-registry policy's signing
	// stance so a pull of an unsigned artifact from this registry halts. This is
	// the registry-advertised wire — a separate network contract a registry
	// serves about itself — not the workspace-local config lever; the pin copies
	// it onto the per-registry signing block, so the two are related but distinct
	// surfaces and are named independently.
	RequiresSignatures bool `json:"requires_signatures"`
	// TrustedIdentities is the OR-set of keyless signing identities the
	// registry advertises as acceptable for its artifacts. A pulled artifact's
	// signature verifies when its certificate matches any one entry.
	TrustedIdentities []AdvertisedIdentity `json:"trusted_identities"`
	// TrustedDeprecationIdentities is the OR-set of keyless identities the
	// registry advertises as additionally acceptable to sign a retroactive
	// deprecation marker — a registry owner or security team retracting an
	// artifact the original publisher can no longer reach. It never widens the
	// trust set for artifact signatures themselves.
	TrustedDeprecationIdentities []AdvertisedIdentity `json:"trusted_deprecation_identities"`
	// PublicKey is an optional inline cosign-compatible public-key PEM for a
	// keyed-mode registry. When set, the registry advertises that its
	// signatures are verified offline against this key rather than against
	// keyless identities.
	PublicKey string `json:"public_key,omitempty"`
}

// AdvertisedIdentity is one acceptable keyless signing identity in an
// advertised trust document: a certificate subject (Identity, an exact match)
// or IdentityRegexp (a regular-expression match), paired with the OIDC Issuer
// that vouched for it. Exactly one of Identity / IdentityRegexp is set per
// entry; Issuer is always required.
type AdvertisedIdentity struct {
	// Identity is the exact certificate subject alternative name — the OIDC
	// identity bound into the short-lived signing certificate.
	Identity string `json:"identity,omitempty"`
	// IdentityRegexp, when set, matches the certificate subject as a regular
	// expression instead of an exact string.
	IdentityRegexp string `json:"identity_regexp,omitempty"`
	// Issuer is the OIDC issuer URL that issued the identity token.
	Issuer string `json:"issuer"`
}

// Decode parses raw advertised-trust JSON and validates its shape, returning
// the decoded document. It rejects a document with an identity entry that
// names neither identity nor identity_regexp, or that omits its issuer, since
// such an entry can never match a signature and would silently weaken the pin.
// Unknown top-level fields are rejected so a typo in the advertised document
// surfaces at fetch time rather than being silently dropped.
func Decode(raw []byte) (AdvertisedTrust, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	var t AdvertisedTrust
	if err := dec.Decode(&t); err != nil {
		return AdvertisedTrust{}, fmt.Errorf("%w: %v", ErrMalformedTrust(), err)
	}
	if err := validateIdentities("trusted_identities", t.TrustedIdentities); err != nil {
		return AdvertisedTrust{}, err
	}
	if err := validateIdentities("trusted_deprecation_identities", t.TrustedDeprecationIdentities); err != nil {
		return AdvertisedTrust{}, err
	}
	return t, nil
}

func validateIdentities(field string, ids []AdvertisedIdentity) error {
	for i, id := range ids {
		if id.Identity == "" && id.IdentityRegexp == "" {
			return fmt.Errorf("%w: %s[%d]: an identity entry must set identity or identity_regexp", ErrMalformedTrust(), field, i)
		}
		if id.Issuer == "" {
			return fmt.Errorf("%w: %s[%d]: an identity entry must set issuer", ErrMalformedTrust(), field, i)
		}
	}
	return nil
}

// ErrNoTrustAdvertised reports that a registry does not publish a trust
// document — it answered the well-known request with a 404. It is a soft,
// expected outcome, not a fetch failure: the caller falls back to manual
// identity pinning. Distinct from a transport error (DNS/TCP/TLS), which the
// caller may also tolerate but which signals a reachability problem rather
// than a deliberate "no policy advertised".
func ErrNoTrustAdvertised() error { return errNoTrustAdvertised }

var errNoTrustAdvertised = cerrors.Register("no-trust-advertised", cerrors.Meta{
	ShortCode: "E0148",
	Message:   "registrytrust: registry advertises no trust config",
	RulePlace: cerrors.NotARule("A registry answered the well-known trust request with a not-found, so it publishes no trust " +
		"document. It reports the outcome of asking a registry a question rather than a rule refusing " +
		"anything."),
})

// ErrMalformedTrust reports that a registry served a trust document that
// fetched successfully but failed to decode or validate. Unlike a 404 or a
// transport failure, this is a hard error a caller should surface: the
// endpoint exists and answered, but its content is not a usable trust policy,
// which signals a real misconfiguration rather than an absent policy. Both
// Decode and Fetch wrap this sentinel on a decode/validation failure.
func ErrMalformedTrust() error { return errMalformedTrust }

var errMalformedTrust = cerrors.Register("malformed-trust-policy", cerrors.Meta{
	ShortCode: "E0147",
	Message:   "registrytrust: advertised trust config is malformed",
	RulePlace: cerrors.NotARule("A registry served a trust document that fetched successfully and then failed to decode or " +
		"validate. The document is fetched in order to be decoded, and what it reports is a decode " +
		"over fetched content that did not yield a usable policy."),
})

// ErrOfflineRegistrationWithoutTrustPolicy reports that a registry other than
// the official hub was asked to be registered while the workspace is offline,
// with no trust policy supplied by hand.
//
// Registration is the one moment a registry's trust is pinned: every later pull
// verifies against the pinned copy and never re-fetches it. Offline, the
// advertised policy cannot be fetched, so an entry written anyway would trust
// nothing while nothing said so — the pin trust-on-first-use exists to take would
// simply not have been taken. The registration therefore refuses rather than
// write that entry. The official hub is not refused, because its entry carries
// the built-in baseline whether or not the fetch runs; and a registration that
// supplies identities or a key pins exactly those and does not fetch at all.
//
// The remedies are in the sentence rather than supplied per raise site, because
// every path that reaches this condition offers the same two: register once while
// online, so the advertised policy is fetched and pinned, or supply the policy by
// hand.
func ErrOfflineRegistrationWithoutTrustPolicy() error {
	return errOfflineRegistrationWithoutTrustPolicy
}

var errOfflineRegistrationWithoutTrustPolicy = cerrors.Register("offline-registration-without-trust-policy", cerrors.Meta{
	ShortCode: "E0310",
	Message: "a registry other than the official hub is not registered offline without a trust policy supplied by " +
		"hand, because the policy it advertises cannot be fetched; register it once while online, or pin its trust " +
		"by hand with `aggrega registry add <host> --identity <identity>=<issuer>` or `--key <public-key-file>`",
	RulePlace: cerrors.RuleNoStageReaches("A configuration write evaluates the rule, as a registration composes a registry " +
		"entry while the workspace is offline, with no identity or key supplied and the advertised policy refused " +
		"by the offline posture before any dial. The write edits a file rather than compiling anything, so no " +
		"declared stage of a compilation holds the request or the posture it reads."),
})

// fetchTimeout bounds the one-time well-known GET so a hung endpoint cannot
// stall registration indefinitely.
const fetchTimeout = 15 * time.Second

// Fetch GETs the advertised trust document from host's own origin
// (`https://<host>` + WellKnownPath, or plain HTTP for a loopback host),
// decodes it, and returns the validated document. A 404 maps to
// ErrNoTrustAdvertised so the caller can distinguish "this registry advertises
// no policy" (a normal, tolerated outcome) from a transport failure (returned
// wrapped). Any non-2xx other than 404 is treated as a fetch failure.
//
// Every failure that leaves the registration proceeding without an advertised
// policy is worded as what happened to this registry's request, opening with the
// registry's name, because that sentence is what a registration reports as its
// reason — beside the policy it took instead, or the flags that supply one by
// hand. So it says what the registry answered, or why it was not asked, in the
// reader's terms: a registry that asks for credentials is said to, rather than
// reported by its status code alone.
//
// host is the registry authority (e.g. "hub.aggrega.ai" or "localhost:5000"),
// as produced by pkg/registryhost.Of — no scheme, no path. The client passed
// in transports the request, bounded by fetchTimeout; it comes from pkg/egress,
// and a nil client is that gate's offline refusal, reported before any dial.
func Fetch(ctx context.Context, client *http.Client, host string) (AdvertisedTrust, error) {
	if host == "" {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: a registry host is required")
	}
	if client == nil {
		// The client is obtained from the network-egress gate (pkg/egress); a nil
		// client means the workspace is offline, so the well-known trust fetch is
		// refused before any dial.
		return AdvertisedTrust{}, &noPolicyTaken{
			sentence:  fmt.Sprintf("%s was not asked for its trust policy, because the workspace is offline", host),
			condition: egress.ErrOfflineEgress(),
		}
	}
	client.Timeout = fetchTimeout

	scheme := "https"
	if registryhost.IsLoopback(host) {
		scheme = "http"
	}
	endpoint := (&url.URL{Scheme: scheme, Host: host, Path: WellKnownPath}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("%s could not be asked for its trust policy: %w", host, err)
	}
	req.Header.Set("Accept", mediaType)

	resp, err := client.Do(req)
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("%s did not return its trust policy: %w", host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return AdvertisedTrust{}, &noPolicyTaken{
			sentence:  fmt.Sprintf("%s publishes no trust policy (HTTP %d)", host, resp.StatusCode),
			condition: ErrNoTrustAdvertised(),
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AdvertisedTrust{}, statusRefusal(host, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("%s sent a trust policy that could not be read: %w", host, err)
	}
	return Decode(body)
}

// statusRefusal is a registry's answer to the trust-policy request with a status
// that is neither success nor not-found, said as what the answer means to the
// reader where the status has one meaning: an answer asking for credentials says
// so, and one refusing access says that. Any other status is reported as the
// status the registry answered with.
func statusRefusal(host string, status int) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("%s requires credentials to read its trust policy (HTTP %d)", host, status)
	case http.StatusForbidden:
		return fmt.Errorf("%s refuses access to its trust policy (HTTP %d)", host, status)
	}
	return fmt.Errorf("%s answered the request for its trust policy with HTTP %d", host, status)
}

// noPolicyTaken is a trust fetch that took no policy for a reason a registered
// condition names, said as what happened to this registry's request.
//
// It is the condition — errors.Is and the condition's short code read through it —
// but it does not repeat the condition's own sentence. That sentence describes
// the condition in general and, for the offline refusal, carries the remedy for
// work that needs the network; this one is read as the reason a registration took
// the policy it took, and the registration states the outcome and what to do
// about it itself — the built-in baseline it pinned, or the flags that supply a
// policy by hand — so the general remedy beside it would be advice the reader
// does not need.
type noPolicyTaken struct {
	sentence  string
	condition error
}

func (e *noPolicyTaken) Error() string { return e.sentence }
func (e *noPolicyTaken) Unwrap() error { return e.condition }

// maxBodyBytes caps the advertised-trust body the fetch reads. A trust
// document is a small list of identities; the cap keeps a misbehaving or
// hostile endpoint from streaming an unbounded body into memory.
const maxBodyBytes = 1 << 20 // 1 MiB
