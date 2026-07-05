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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	// every artifact to be signed. Pinned onto the per-registry policy so a
	// pull of an unsigned artifact from this registry halts.
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
		return AdvertisedTrust{}, fmt.Errorf("%w: %v", ErrMalformedTrust, err)
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
			return fmt.Errorf("%w: %s[%d]: an identity entry must set identity or identity_regexp", ErrMalformedTrust, field, i)
		}
		if id.Issuer == "" {
			return fmt.Errorf("%w: %s[%d]: an identity entry must set issuer", ErrMalformedTrust, field, i)
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
var ErrNoTrustAdvertised = errors.New("registrytrust: registry advertises no trust config")

// ErrMalformedTrust reports that a registry served a trust document that
// fetched successfully but failed to decode or validate. Unlike a 404 or a
// transport failure, this is a hard error a caller should surface: the
// endpoint exists and answered, but its content is not a usable trust policy,
// which signals a real misconfiguration rather than an absent policy. Both
// Decode and Fetch wrap this sentinel on a decode/validation failure.
var ErrMalformedTrust = errors.New("registrytrust: advertised trust config is malformed")

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
// host is the registry authority (e.g. "hub.aggrega.ai" or "localhost:5000"),
// as produced by pkg/registryhost.Of — no scheme, no path. The client passed
// in transports the request; callers inject an httptest client in tests. A nil
// client uses a default client bounded by fetchTimeout.
func Fetch(ctx context.Context, client *http.Client, host string) (AdvertisedTrust, error) {
	if host == "" {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: a registry host is required")
	}
	if client == nil {
		client = &http.Client{Timeout: fetchTimeout}
	}

	scheme := "https"
	if registryhost.IsLoopback(host) {
		scheme = "http"
	}
	endpoint := (&url.URL{Scheme: scheme, Host: host, Path: WellKnownPath}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: build request for %s: %w", host, err)
	}
	req.Header.Set("Accept", mediaType)

	resp, err := client.Do(req)
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: fetch trust config from %s: %w", host, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return AdvertisedTrust{}, ErrNoTrustAdvertised
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: fetch trust config from %s: HTTP %d", host, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return AdvertisedTrust{}, fmt.Errorf("registrytrust: read trust config from %s: %w", host, err)
	}
	return Decode(body)
}

// maxBodyBytes caps the advertised-trust body the fetch reads. A trust
// document is a small list of identities; the cap keeps a misbehaving or
// hostile endpoint from streaming an unbounded body into memory.
const maxBodyBytes = 1 << 20 // 1 MiB
