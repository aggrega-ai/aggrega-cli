// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"net/http"
	"strings"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// bearerScheme is the credential scheme this deployment accepts, spelled as the
// authorization header spells it. The comparison against a request is
// case-insensitive, because the header's scheme is.
const bearerScheme = "Bearer"

// queryTokenParameter is the name a token carried in the address rather than in
// the header would arrive under. Nothing is ever read from it: a request
// carrying it is refused, because a token in an address is written into every
// proxy log, browser history and referrer it passes through, and having been
// exposed that way it should not go on being accepted.
const queryTokenParameter = "access_token"

// accessTokenType is the type an access token states for itself, in both the
// short form and the media-type form the same registration allows.
//
// A token stating a type is held to stating one of these. That is what stops a
// token an authorization server minted for something else — an identity token
// for a client to read about its own user, say — from being spent here as
// though it had been issued for this deployment.
var accessTokenTypes = []string{"at+jwt", "application/at+jwt"}

// signatureAlgorithms are the algorithms a token may be signed with.
//
// Every one is asymmetric, and that is the whole list rather than a starting
// point. An unsigned token names an algorithm that is not here and is refused
// at the parse. So is a token naming a symmetric algorithm, which matters more
// than it looks: verifying one takes the same bytes to make a signature as to
// check it, so a deployment accepting a symmetric algorithm against a set of
// PUBLIC keys would accept a signature anyone holding those public keys could
// have produced.
var signatureAlgorithms = []jose.SignatureAlgorithm{
	jose.RS256, jose.RS384, jose.RS512,
	jose.PS256, jose.PS384, jose.PS512,
	jose.ES256, jose.ES384, jose.ES512,
	jose.EdDSA,
}

// tokenClaims are the claims read from a token beyond the registered ones the
// JWT library already models, in both spellings each is published under.
type tokenClaims struct {
	// ClientID and AuthorizedParty each name the client a token was issued to.
	// Which one an authorization server writes varies, so both are read and
	// whichever is present is recorded.
	ClientID        string `json:"client_id"`
	AuthorizedParty string `json:"azp"`

	// Scope is the space-separated form, ScopeList the array form. An
	// authorization server publishes one or the other.
	Scope     string   `json:"scope"`
	ScopeList []string `json:"scp"`
}

// bearerValidator admits a request carrying a token this deployment's
// authorization server issued for it, and refuses everything else.
//
// It is a resource server and nothing more: it reads a token a caller
// presented, decides whether that token was issued by the configured
// authorization server for this deployment, and forgets it. It never obtains a
// token, never exchanges one, never sends one anywhere, and never writes one
// down — so a token that is refused leaves no trace beyond the request having
// been refused, and a token that is accepted leaves only what it asserted.
type bearerValidator struct {
	// resource is what this deployment publishes about itself, which is where
	// every challenge this composes comes from: a refused caller is pointed at
	// the same document a caller with nothing is pointed at.
	resource protectedResource

	// issuer and audience are what a token has to state to be accepted: the
	// authorization server it must have come from, and this deployment's own
	// published address, which its audience must name.
	issuer   string
	audience string

	// keys are the keys a token may have been signed with.
	keys verificationKeys
}

// authenticate reads the credential a request carries and reports who it is on
// behalf of.
//
// The order of the checks below is not an arrangement. A token in the address
// is refused before anything is read from the header, because a request
// carrying one has already leaked it wherever it passed and the point is to
// refuse it rather than to evaluate it. Everything after that is refused with
// one of two reasons and no finer detail, so a caller learns whether to obtain
// a new token and nothing about which check turned it away.
func (v *bearerValidator) authenticate(r *http.Request) (*http.Request, *authRefusal) {
	if r.URL.Query().Has(queryTokenParameter) {
		return nil, &authRefusal{
			status:    http.StatusBadRequest,
			challenge: v.resource.challengeFor(authErrorInvalidRequest),
			message: "this server takes a bearer token on the authorization header and nowhere else; a " +
				"token in the address is recorded by everything the request passes through, so one sent " +
				"that way is refused rather than used",
		}
	}

	presented := r.Header.Get(headerAuthorization)
	if presented == "" {
		return nil, &authRefusal{
			status:    http.StatusUnauthorized,
			challenge: v.resource.challenge,
			message: "this server answers a caller carrying a bearer token; the challenge names the " +
				"document that says where to obtain one",
		}
	}
	scheme, raw, split := strings.Cut(presented, " ")
	if !strings.EqualFold(scheme, bearerScheme) || strings.TrimSpace(raw) == "" || !split {
		return nil, &authRefusal{
			status:    http.StatusBadRequest,
			challenge: v.resource.challengeFor(authErrorInvalidRequest),
			message:   "the authorization header is not a bearer credential; it reads " + bearerScheme + " followed by a token",
		}
	}

	p, outcome := v.principalFor(r, strings.TrimSpace(raw))
	switch outcome {
	case credentialAccepted:
		return withPrincipal(r, p), nil
	case credentialUnverifiable:
		// No challenge: nothing the caller presented was found wanting, so
		// asking it to present something else would send it after a token it
		// already holds. The status is the one this transport already uses for
		// work it is momentarily unable to do.
		return nil, &authRefusal{
			status: http.StatusServiceUnavailable,
			message: "this server could not read the keys the authorization server it was configured " +
				"with publishes, so no token can be verified at the moment",
		}
	default:
		return nil, &authRefusal{
			status:    http.StatusUnauthorized,
			challenge: v.resource.challengeFor(authErrorInvalidToken),
			message: "the bearer token presented was not accepted; obtain one from the authorization " +
				"server the challenge names, for the resource it names",
		}
	}
}

// credentialOutcome separates the two things a token that does not admit its
// bearer can mean, because the two owe a caller different answers: a token this
// deployment read and would not accept, and a token it could not read at all
// because the keys were out of reach.
type credentialOutcome int

const (
	credentialAccepted credentialOutcome = iota
	credentialRefused
	credentialUnverifiable
)

// principalFor verifies one token and reads who it says presented it.
//
// Nothing about why a token failed leaves this function. Every way of failing
// comes back as the same refusal, and the caller turns it into one answer, so
// the checks below can be as specific as they need to be without any of that
// specificity reaching a caller. The token itself is never returned, logged, or
// placed in anything the caller receives.
func (v *bearerValidator) principalFor(r *http.Request, raw string) (*principal, credentialOutcome) {
	parsed, err := jwt.ParseSigned(raw, signatureAlgorithms)
	if err != nil {
		return nil, credentialRefused
	}
	if len(parsed.Headers) != 1 {
		return nil, credentialRefused
	}
	header := parsed.Headers[0]
	if declared, stated := header.ExtraHeaders[jose.HeaderType].(string); stated &&
		!statesAccessTokenType(declared) {
		return nil, credentialRefused
	}

	candidates, err := v.keys.keysFor(r.Context(), header.KeyID)
	if err != nil {
		return nil, credentialUnverifiable
	}
	if len(candidates) == 0 {
		return nil, credentialRefused
	}

	var registered jwt.Claims
	var extra tokenClaims
	verified := false
	for i := range candidates {
		if err := parsed.Claims(candidates[i].Key, &registered, &extra); err == nil {
			verified = true
			break
		}
	}
	if !verified {
		return nil, credentialRefused
	}

	// A token with no expiry never stops being usable, and one with no subject
	// asserts nothing about who presented it. The claim validation below skips
	// a claim that is absent, so both are required here rather than left to it.
	if registered.Expiry == nil || registered.Subject == "" {
		return nil, credentialRefused
	}
	if err := registered.Validate(jwt.Expected{
		Issuer:      v.issuer,
		AnyAudience: jwt.Audience{v.audience},
	}); err != nil {
		return nil, credentialRefused
	}

	return &principal{
		Subject:  registered.Subject,
		Issuer:   registered.Issuer,
		ClientID: firstNonEmpty(extra.ClientID, extra.AuthorizedParty),
		Scopes:   extra.scopes(),
	}, credentialAccepted
}

// statesAccessTokenType reports whether a token declaring a type declares one
// of the forms an access token is registered under. The comparison is
// case-insensitive, as the registration is.
func statesAccessTokenType(declared string) bool {
	for _, accepted := range accessTokenTypes {
		if strings.EqualFold(declared, accepted) {
			return true
		}
	}
	return false
}

// scopes reads what a token says its bearer may ask for, from whichever of the
// two forms the authorization server published.
//
// The space-separated form wins where both are present rather than the two
// being merged: a server publishing both is publishing one fact twice, and
// taking the union of two spellings of one fact would grant whatever the wider
// of them said.
func (c tokenClaims) scopes() []string {
	if c.Scope != "" {
		return strings.Fields(c.Scope)
	}
	return c.ScopeList
}

// firstNonEmpty is the "whichever of these was published" rule the client
// identifier needs, kept as one expression so the preference between the two
// spellings is stated once.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
